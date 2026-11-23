"""Publish only non-secret committed discovery on the fixed observer/controller host."""
import hashlib
import json
import os
from pathlib import Path
import stat
import sys


def require(ok, message):
    if not ok:
        raise ValueError(message)


def read_regular(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1
                and not info.st_mode & 0o022 and info.st_size <= 1024 * 1024, 'unsafe-discovery-source')
        return source.read(1024 * 1024 + 1)


def main():
    os.umask(0o077)
    require(os.geteuid() == 0, 'root-required')
    raw = sys.stdin.buffer.read(1024 * 1024 + 1)
    require(len(raw) <= 1024 * 1024, 'oversized-input')
    c = json.loads(raw)
    identity = json.loads(read_regular(Path(c['identity_file'])))
    require(identity.get('project') == c['project_id'] and identity.get('label') == 'controller' and identity.get('permanent') is True, 'foreign-discovery-host')
    directory = Path('/var/lib/clo-ingress')
    require(str(directory.resolve()) == str(directory), 'symlink-directory')
    directory.mkdir(mode=0o700, exist_ok=True)
    info = directory.lstat()
    require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o077, 'unsafe-discovery-directory')
    value = c['manifest']
    require(value['project_id'] == c['project_id'] and value['manager_id'] == c['manager_id'] and isinstance(value['targets'], list) and len(value['targets']) <= 8, 'foreign-discovery-input')
    destination = directory / 'discovery.json'
    if destination.exists():
        require(not destination.is_symlink(), 'symlink-manifest')
        old = json.loads(read_regular(destination))
        require(all(old.get(k) == value.get(k) for k in ['project_id', 'cluster_uid', 'manager_id', 'scope_hash']), 'foreign-prior-manifest')
    content = json.dumps(value, sort_keys=True).encode()
    temporary = directory / ('.discovery-' + str(os.getpid()))
    with temporary.open('xb') as output:
        output.write(content)
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary, destination)
    fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    print(json.dumps({'ok': True, 'sha256': hashlib.sha256(content).hexdigest()}))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('{"ok":false,"error":"discovery-publication-rejected"}')
        raise SystemExit(1)
