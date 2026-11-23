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


def main():
    os.umask(0o077)
    require(os.geteuid() == 0, 'root-required')
    raw = sys.stdin.buffer.read(1024 * 1024 + 1)
    require(len(raw) <= 1024 * 1024, 'oversized-input')
    c = json.loads(raw)
    identity = json.loads(read(Path(c['identity_file'])))
    require(identity.get('project') == c['project_id'] and identity.get('label') == c['identity_label']
            and identity.get('permanent') is True, 'edge-identity-mismatch')
    require(len(c['targets']) <= 8 and len(set(c['targets'])) == len(c['targets']), 'target-bound')
    for ip in c['targets']:
        require(ipaddress.IPv4Address(ip).is_private, 'nonprivate-target')
    require(str(ROOT.resolve()) == str(ROOT), 'symlink-state-directory')
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = ROOT.lstat()
    require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o077, 'unsafe-state-directory')
    lock = os.open(ROOT / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    owner = ROOT / 'owner.json'
    expected = {'manager_id': c['manager_id'], 'scope_hash': c['scope_hash'],
                'project_id': c['project_id'], 'edge_id': c['edge_id']}
    if owner.exists():
        require(json.loads(read(owner)) == expected, 'foreign-managed-edge')
    else:
        require(hashlib.sha256(read(CONFIG)).hexdigest() == c['adoption_sha256'], 'uncommissioned-edge-config')
        atomic(owner, json.dumps(expected).encode())
    generation = c['generation']
    require(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9-]{0,63}', generation), 'invalid-generation')
    content = candidate_content(c)
    digest = hashlib.sha256(content).hexdigest()
    candidate = ROOT / ('candidate-' + generation + '.cfg')
    if candidate.exists():
        require(read(candidate) == content, 'generation-reused-with-different-config')
    else:
        atomic(candidate, content)
    command(['haproxy', '-c', '-f', str(candidate)])
    # Admission is checked from the actual edge path, not just kubelet readiness.
    require(all(probe(ip) for ip in c['targets']), 'backend-not-healthy-from-edge')
    current = read(CONFIG)
    receipt = ROOT / 'receipt.json'
    prior = json.loads(read(receipt)) if receipt.exists() else {}
    intent = ROOT / 'intent.json'
    pending = json.loads(read(intent)) if intent.exists() else {}
    allowed = {c['adoption_sha256'], prior.get('config_sha256'), pending.get('previous_sha256'), pending.get('config_sha256'), digest}
    require(hashlib.sha256(current).hexdigest() in allowed, 'edge-config-changed-outside-manager')
    # Never overlap an accepted/reloading systemd job from a lost SSH reply.
    deadline = time.monotonic() + 15
    while True:
        job = command(['systemctl', 'show', 'haproxy.service', '-p', 'Job', '--value']).strip()
        if job in ('', '0'):
            break
        require(time.monotonic() < deadline, 'previous-reload-still-running')
        time.sleep(0.2)
    runtime = None
    if hashlib.sha256(current).hexdigest() == digest:
        try:
            runtime = live(generation, c['targets'])
        except (OSError, ValueError):
            pass
    if runtime is None:
        atomic(intent, json.dumps({'generation': generation, 'config_sha256': digest,
                                  'previous_sha256': hashlib.sha256(current).hexdigest()}).encode())
        backup = ROOT / ('before-' + generation + '.cfg')
        if not backup.exists():
            atomic(backup, current)
        atomic(CONFIG, content, 0o644)
        command(['systemctl', 'reload', 'haproxy'])
        runtime = wait_live(generation, c['targets'])
    require(command(['systemctl', 'is-active', 'haproxy']).strip() == 'active', 'haproxy-not-active')
    result = {'ok': True, 'edge_id': c['edge_id'], 'generation': generation,
              'config_sha256': digest, 'servers_state': runtime}
    atomic(receipt, json.dumps(result).encode())
    if intent.exists():
        intent.unlink()
    print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(json.dumps({'ok': False, 'error': str(e) if isinstance(e, ValueError) else type(e).__name__}))
        raise SystemExit(1)
