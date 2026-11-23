"""Fixed-path, JSON-stdin ingress guard preparation. Run over authenticated root SSH."""
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

ROOT = Path('/var/lib/clo-ingress/node')
RULES = Path('/etc/rave-prod-ingress.nft')
UNIT = Path('/etc/systemd/system/rave-prod-ingress-firewall.service')
DROPIN = Path('/etc/systemd/system/rke2-agent.service.d/20-ingress-guard.conf')


def require(ok, code):
    if not ok:
        raise ValueError(code)


def run(args):
    p = subprocess.run(args, capture_output=True, timeout=20)
    require(p.returncode == 0 and len(p.stdout) <= 1024 * 1024, 'host-command-failed')
    return p.stdout.decode()


def private_dir(path):
    require(str(path.resolve()) == str(path), 'symlink-directory')
    path.mkdir(parents=True, mode=0o700, exist_ok=True)
    info = path.lstat()
    require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o077, 'unsafe-state-directory')


def read_regular(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as f:
        info = os.fstat(f.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1
                and not info.st_mode & 0o022 and info.st_size <= 1024 * 1024, 'unsafe-host-file')
        return f.read(1024 * 1024 + 1)


def atomic(path, value, mode=0o600):
    require(not path.is_symlink() and str(path.parent.resolve()) == str(path.parent), 'symlink-destination')
    tmp = path.with_name('.' + path.name + '.clo-ingress-' + str(os.getpid()))
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(value)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if tmp.exists():
            tmp.unlink()


def guard_rules(node_ip, edges, observer):
    edges = sorted(edges, key=ipaddress.IPv4Address)
    ips = [ipaddress.IPv4Address(ip) for ip in [node_ip, *edges, observer]]
    require(all(ip.is_private for ip in ips) and len(edges) == 2
            and len(set(ips)) == 4, 'invalid-private-guard-topology')
    return ('add table inet rave_prod_ingress\nflush table inet rave_prod_ingress\n'
            'table inet rave_prod_ingress { chain ingress { type filter hook input priority -10; policy accept;\n'
            ' tcp dport { 8000, 8443, 9000 } iifname "lo" accept\n'
            ' tcp dport { 8000, 8443, 9000 } ip saddr ' + node_ip + '/32 accept\n'
            + ''.join(' tcp dport { 8000, 8443, 9000 } ip saddr ' + ip + '/32 accept\n' for ip in edges)
            + ' tcp dport 9000 ip saddr ' + observer + '/32 accept\n'
            ' tcp dport { 8000, 8443, 9000 } counter reject with tcp reset\n }; }\n')


def check_identity(c, actual):
    n = c['node']
    require(actual == {'schemaVersion': 1, 'projectID': n['project_id'],
                       'providerName': n['provider_name'], 'attempt': n['attempt']}, 'guest-identity-mismatch')


def kernel_fingerprint(value):
    def stable(v):
        if isinstance(v, list):
            return [stable(item) for item in v]
        if isinstance(v, dict):
            return {key: ({} if key == 'counter' else stable(item)) for key, item in v.items()
                    if key not in ('handle', 'metainfo')}
        return v
    return hashlib.sha256(json.dumps(stable(value), sort_keys=True).encode()).hexdigest()


def main():
    os.umask(0o077)
    require(os.geteuid() == 0, 'root-required')
    raw = sys.stdin.buffer.read(1024 * 1024 + 1)
    require(len(raw) <= 1024 * 1024, 'oversized-input')
    c = json.loads(raw)
    check_identity(c, json.loads(read_regular(Path('/etc/clo/machine-identity.json'))))
    private_dir(ROOT.parent)
    private_dir(ROOT)
    lock = os.open(ROOT / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    marker = ROOT / 'owner.json'
    expected = {'manager_id': c['manager_id'], 'scope_hash': c['scope_hash'],
                'project_id': c['node']['project_id'], 'server_id': c['node']['server_id']}
    if marker.exists():
        require(json.loads(read_regular(marker)) == expected, 'foreign-host-marker')
    else:
        existing = any(p.exists() or p.is_symlink() for p in [RULES, UNIT, DROPIN])
        require(not existing or c.get('adopt_existing') is True, 'unmanaged-existing-guard')
    content = guard_rules(c['node']['private_ip'], c['edge_ips'], c['observer_ip']).encode()
    if not marker.exists() and RULES.exists():
        require(read_regular(RULES) == content, 'existing-policy-differs')
    unit = ('[Unit]\nAfter=network-online.target\nBefore=rke2-agent.service\n'
            '[Service]\nType=oneshot\nExecStart=/usr/sbin/nft -f /etc/rave-prod-ingress.nft\n'
            'RemainAfterExit=yes\n[Install]\nWantedBy=multi-user.target\n').encode()
    dropin = ('[Unit]\nRequires=rave-prod-ingress-firewall.service\nAfter=rave-prod-ingress-firewall.service\n'
              '[Service]\nExecStartPre=/usr/sbin/nft list table inet rave_prod_ingress\n').encode()
    pid = run(['systemctl', 'show', 'rke2-agent', '-p', 'MainPID', '--value']).strip()
    require(pid.isdigit() and pid != '0', 'worker-agent-not-running')
    previous = ROOT / 'receipt.json'
    old = json.loads(read_regular(previous)) if previous.exists() else {}
    kernel = subprocess.run(['nft', '-s', '-j', 'list', 'table', 'inet', 'rave_prod_ingress'], capture_output=True, timeout=10)
    files_match = all(p.exists() and read_regular(p) == value for p, value in [(RULES, content), (UNIT, unit), (DROPIN, dropin)])
    kernel_matches = kernel.returncode == 0 and kernel_fingerprint(json.loads(kernel.stdout)) == old.get('kernel_sha256')
    service = subprocess.run(['systemctl', 'is-active', '--quiet', 'rave-prod-ingress-firewall.service'], timeout=10).returncode == 0
    enabled = subprocess.run(['systemctl', 'is-enabled', '--quiet', 'rave-prod-ingress-firewall.service'], capture_output=True, timeout=10).returncode == 0
    if not (marker.exists() and files_match and kernel_matches and service and enabled):
        candidate = ROOT / 'candidate.nft'
        atomic(candidate, content)
        run(['nft', '-c', '-f', str(candidate)])
        atomic(ROOT / 'intent.json', json.dumps({'node': c['node'], 'rules_sha256': hashlib.sha256(content).hexdigest()}).encode())
        DROPIN.parent.mkdir(parents=True, exist_ok=True)
        for path, value in [(RULES, content), (UNIT, unit), (DROPIN, dropin)]:
            atomic(path, value, 0o644)
        run(['nft', '-f', str(RULES)])
        run(['systemctl', 'daemon-reload'])
        run(['systemctl', 'enable', '--now', 'rave-prod-ingress-firewall.service'])
        run(['systemctl', 'is-active', 'rave-prod-ingress-firewall.service'])
        run(['systemctl', 'is-enabled', 'rave-prod-ingress-firewall.service'])
        atomic(marker, json.dumps(expected).encode())
    require(run(['systemctl', 'show', 'rke2-agent', '-p', 'MainPID', '--value']).strip() == pid, 'worker-agent-changed')
    kernel = json.loads(run(['nft', '-s', '-j', 'list', 'table', 'inet', 'rave_prod_ingress']))
    receipt = {'ok': True, 'node_uid': c['node']['node_uid'], 'server_id': c['node']['server_id'],
               'rules_sha256': hashlib.sha256(content).hexdigest(), 'kernel_sha256': kernel_fingerprint(kernel),
               'rke2_pid': pid, 'persistent_guard': True}
    atomic(previous, json.dumps(receipt).encode())
    print(json.dumps(receipt))


if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print(json.dumps({'ok': False, 'error': str(e) if isinstance(e, ValueError) else type(e).__name__}))
        raise SystemExit(1)
