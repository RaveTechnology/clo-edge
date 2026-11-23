"""Reconcile a validated HAProxy generation; never manipulate VMs or addresses."""
import fcntl
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import stat
import subprocess
import sys
import time

ROOT = Path('/var/lib/clo-ingress/edge')
CONFIG = Path('/etc/haproxy/haproxy.cfg')


def require(ok, code):
    if not ok:
        raise ValueError(code)


def read(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as f:
        info = os.fstat(f.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1
                and not info.st_mode & 0o022 and info.st_size <= 1024 * 1024, 'unsafe-host-file')
        return f.read(1024 * 1024 + 1)


def atomic(path, content, mode=0o600):
    require(not path.is_symlink() and str(path.parent.resolve()) == str(path.parent), 'unsafe-destination')
    tmp = path.with_name('.' + path.name + '.clo-ingress-' + str(os.getpid()))
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(content)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if tmp.exists():
            tmp.unlink()


def command(args):
    q = subprocess.run(args, capture_output=True, timeout=20)
    require(q.returncode == 0, 'haproxy-command-failed')
    return q.stdout.decode()


def probe(ip):
    require(ipaddress.IPv4Address(ip).is_private, 'nonprivate-backend')
    connection = http.client.HTTPConnection(ip, 9000, timeout=2)
    try:
        connection.request('GET', '/ping')
        response = connection.getresponse()
        return response.status == 200 and response.read(4097) == b'OK'
    except (OSError, http.client.HTTPException):
        return False
    finally:
        connection.close()


def live(generation, targets):
    require(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9-]{0,63}', generation), 'invalid-generation')
    path = Path('/run/haproxy/clo-ingress-' + generation + '.sock')
    info = path.lstat()
    require(stat.S_ISSOCK(info.st_mode) and info.st_uid == 0 and info.st_mode & 0o777 == 0o600, 'unsafe-runtime-socket')
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(2)
        connection.connect(str(path))
        connection.sendall(b'show servers state\n')
        output = bytearray()
        while True:
            part = connection.recv(65536)
            if not part:
                break
            output.extend(part)
            require(len(output) <= 1024 * 1024, 'oversized-runtime-state')
    value = output.decode()
    return verify_runtime_state(value, targets)


def verify_runtime_state(value, targets):
    lines = value.strip().splitlines()
    require(len(lines) >= 2 and lines[0] == '1', 'invalid-runtime-version')
    header = lines[1].lstrip('# ').split()
    be, address, name = [header.index(column) for column in ('be_name', 'srv_addr', 'srv_name')]
    expected = set(targets)
    require(len(expected) == len(targets), 'duplicate-target')
    found = {'http-ingresses': set(), 'tls-ingresses': set()}
    seen = set()
    for line in lines[2:]:
        if not line or line.startswith('#'):
            continue
        row = line.split()
        require(len(row) == len(header) and row[be] in found and row[address] in expected, 'runtime-target-mismatch')
        require(row[address] not in found[row[be]] and (row[be], row[name]) not in seen, 'duplicate-runtime-target')
        found[row[be]].add(row[address])
        seen.add((row[be], row[name]))
    require(all(actual == expected for actual in found.values()), 'incomplete-runtime-state')
    return value


def candidate_content(c):
    content = c['config'].encode()
    digest = hashlib.sha256(content).hexdigest()
    require(digest == c['config_sha256'] and len(content) < 128 * 1024, 'config-hash-mismatch')
    require(content.startswith(('# Managed by clo-ingress; generation ' + c['generation'] + '\n').encode()), 'unmanaged-candidate')
    return content


def wait_live(generation, targets):
    deadline = time.monotonic() + 10
    while True:
        try:
            return live(generation, targets)
        except (OSError, ValueError):
            require(time.monotonic() < deadline, 'new-runtime-not-confirmed')
            time.sleep(0.1)


def config_targets(content):
    pools={'http-ingresses': [], 'tls-ingresses': []};backend=None
    for line in content.decode().splitlines():
        if line.startswith('backend '):backend=line.split()[1]
        match=re.match(r'\s+server ingress-\d+ ([0-9.]+):\d+ ',line)
        if match:
            require(backend in pools,'unexpected-config-backend')
            ip=str(ipaddress.IPv4Address(match[1]));require(ip not in pools[backend],'duplicate-config-target');pools[backend].append(ip)
    require(set(pools['http-ingresses'])==set(pools['tls-ingresses']),'asymmetric-config-targets')
    return pools['http-ingresses']


def subset_config(content, generation, targets):
    require(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9-]{0,63}',generation),'invalid-subgeneration')
    old=content.decode().splitlines()[0].removeprefix('# Managed by clo-ingress; generation ')
    output=[]
    for line in content.decode().splitlines():
        match=re.match(r'\s+server ingress-\d+ ([0-9.]+):\d+ ',line)
        if match and match[1] not in targets:continue
        output.append(line.replace('generation '+old,'generation '+generation).replace('clo-ingress-'+old+'.sock','clo-ingress-'+generation+'.sock'))
    result=('\n'.join(output)+'\n').encode();require(set(config_targets(result))==set(targets),'incomplete-subset-config');return result


def prune_files(current):
    groups={}
    for path in ROOT.glob('*.cfg'):
        match=re.fullmatch(r'(candidate|before)-([a-zA-Z0-9][a-zA-Z0-9-]{0,63})\.cfg',path.name)
        if not match or path.is_symlink():continue
        info=path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid!=os.getuid() or info.st_nlink!=1:continue
        groups.setdefault(match[2],[]).append(path)
    newest=sorted((g for g in groups if g!=current),key=lambda g:max(p.stat().st_mtime_ns for p in groups[g]),reverse=True)
    for generation in newest[8:]:
        for path in groups[generation]:path.unlink()


def install_generation(content, generation, targets, c):
    digest=hashlib.sha256(content).hexdigest();candidate=ROOT/('candidate-'+generation+'.cfg')
    if candidate.exists():require(read(candidate)==content,'generation-reused-with-different-config')
    else:atomic(candidate,content)
    command(['haproxy','-c','-f',str(candidate)])
    current=read(CONFIG);receipt=ROOT/'receipt.json';prior=json.loads(read(receipt)) if receipt.exists() else {}
    intent=ROOT/'intent.json';pending=json.loads(read(intent)) if intent.exists() else {}
    current_digest=hashlib.sha256(current).hexdigest()
    allowed={c['adoption_sha256'],prior.get('config_sha256'),pending.get('previous_sha256'),pending.get('config_sha256'),digest}
    # A restored, previously owned generation is recoverable; an arbitrary edit is not.
    if current_digest not in allowed:
        require(any(not p.is_symlink() and hashlib.sha256(read(p)).hexdigest()==current_digest for p in ROOT.glob('candidate-*.cfg')),'edge-config-changed-outside-manager')
    deadline=time.monotonic()+15
    while command(['systemctl','show','haproxy.service','-p','Job','--value']).strip() not in ('','0'):
        require(time.monotonic()<deadline,'previous-reload-still-running');time.sleep(.2)
    runtime=None
    if current_digest==digest:
        try:runtime=live(generation,targets)
        except (OSError,ValueError):pass
        if runtime is None and pending.get('generation')==generation:
            raise ValueError('prior-reload-outcome-still-unknown')
    if runtime is None:
        accepted=current
        prior_generation=prior.get('generation')
        previous_file=ROOT/('candidate-'+str(prior_generation)+'.cfg')
        if prior_generation and previous_file.exists():
            data=read(previous_file)
            require(hashlib.sha256(data).hexdigest()==prior.get('config_sha256'),'accepted-config-corrupt');accepted=data
        elif current_digest!=c['adoption_sha256']:
            previous=ROOT/('before-'+str(pending.get('generation'))+'.cfg')
            if previous.exists() and hashlib.sha256(read(previous)).hexdigest()==pending.get('previous_sha256'):accepted=read(previous)
        atomic(intent,json.dumps({'generation':generation,'config_sha256':digest,'previous_sha256':hashlib.sha256(accepted).hexdigest()}).encode())
        backup=ROOT/('before-'+generation+'.cfg')
        if not backup.exists():atomic(backup,accepted)
        atomic(CONFIG,content,0o644)
        try:command(['systemctl','reload','haproxy'])
        except ValueError as reload_error:
            # A nonzero reload is not enough by itself: first check whether it loaded.
            try:runtime=live(generation,targets)
            except (OSError,ValueError):
                job=command(['systemctl','show','haproxy.service','-p','Job','--value']).strip()
                if prior_generation and job in ('','0'):
                    try:live(prior_generation,config_targets(accepted))
                    except (OSError,ValueError):pass
                    else:
                        atomic(CONFIG,accepted,0o644);intent.unlink()
                raise reload_error
        if runtime is None:runtime=wait_live(generation,targets)
    require(command(['systemctl','is-active','haproxy']).strip()=='active','haproxy-not-active')
    result={'ok':True,'edge_id':c.get('edge_id'),'generation':generation,'config_sha256':digest,'servers_state':runtime}
    atomic(receipt,json.dumps(result).encode())
    if intent.exists():intent.unlink()
    prune_files(generation)
    return result


def apply_desired(c, known):
    desired=c['targets'];content=candidate_content(c);keep=[ip for ip in desired if ip in known]
    if set(known)-set(desired) and set(desired)-set(known):
        # Withdrawal must complete even when a retained or new backend cannot answer.
        generation=c['generation']+'-withdraw'
        install_generation(subset_config(content,generation,keep),generation,keep,c)
    added=[ip for ip in desired if ip not in keep]
    require(not c.get('withdraw_only') or not added,'withdrawal-awaits-new-admission')
    require(all(probe(ip) for ip in added),'new-backend-not-healthy-from-edge')
    return install_generation(content,c['generation'],desired,c)


def main():
    os.umask(0o077);require(os.geteuid()==0,'root-required')
    raw=sys.stdin.buffer.read(1024*1024+1);require(len(raw)<=1024*1024,'oversized-input');c=json.loads(raw)
    identity=json.loads(read(Path(c['identity_file'])))
    require(identity.get('project')==c['project_id'] and identity.get('label')==c['identity_label'] and identity.get('permanent') is True,'edge-identity-mismatch')
    require(isinstance(c['targets'],list) and len(c['targets'])<=8 and len(set(c['targets']))==len(c['targets']),'target-bound')
    for ip in c['targets']:require(ipaddress.IPv4Address(ip).is_private,'nonprivate-target')
    if c.get('probe_only'):
        print(json.dumps({'ok':True,'edge_id':c['edge_id'],'reachable':all(probe(ip) for ip in c['targets'])}));return
    require(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9-]{0,53}',c['generation']),'invalid-generation')
    require(str(ROOT.resolve())==str(ROOT),'symlink-state-directory');ROOT.mkdir(mode=0o700,parents=True,exist_ok=True)
    info=ROOT.lstat();require(stat.S_ISDIR(info.st_mode) and info.st_uid==0 and not info.st_mode&0o077,'unsafe-state-directory')
    lock=os.open(ROOT/'lock',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600);fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    owner=ROOT/'owner.json';expected={k:c[k] for k in ['manager_id','scope_hash','project_id','edge_id']}
    current=read(CONFIG)
    if owner.exists():require(json.loads(read(owner))==expected,'foreign-managed-edge')
    else:
        require(hashlib.sha256(current).hexdigest()==c['adoption_sha256'],'uncommissioned-edge-config');atomic(owner,json.dumps(expected).encode())
    receipt=ROOT/'receipt.json'
    if receipt.exists():
        prior=json.loads(read(receipt));previous=ROOT/('candidate-'+prior['generation']+'.cfg')
        require(previous.exists() and hashlib.sha256(read(previous)).hexdigest()==prior['config_sha256'],'accepted-config-unavailable')
        known=config_targets(read(previous))
    else:
        require(hashlib.sha256(current).hexdigest()==c['adoption_sha256'],'unconfirmed-initial-config');known=config_targets(current)
    print(json.dumps(apply_desired(c,known)))


if __name__=='__main__':
    try:main()
    except Exception as e:
        print(json.dumps({'ok':False,'error':str(e) if isinstance(e,ValueError) else type(e).__name__}));raise SystemExit(1)
