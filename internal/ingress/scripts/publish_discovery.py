"""Publish only non-secret committed discovery on the fixed observer/controller host."""
import hashlib
import json
import os
from pathlib import Path
import stat
import sys


def main():
    os.umask(0o077)
    assert os.geteuid() == 0
    raw = sys.stdin.buffer.read(1024 * 1024 + 1)
    assert len(raw) <= 1024 * 1024
    c = json.loads(raw)
    identity = json.loads(Path(c['identity_file']).read_text())
    assert identity.get('project') == c['project_id'] and identity.get('label') == 'controller' and identity.get('permanent') is True
    directory = Path('/var/lib/clo-ingress')
    assert str(directory.resolve()) == str(directory)
    directory.mkdir(mode=0o700, exist_ok=True)
    info = directory.lstat()
    assert stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o077
    value = c['manifest']
    assert value['project_id'] == c['project_id'] and value['manager_id'] == c['manager_id'] and len(value['targets']) <= 8
    destination = directory / 'discovery.json'
    if destination.exists():
        assert not destination.is_symlink()
        old = json.loads(destination.read_text())
        assert old['project_id'] == value['project_id'] and old['cluster_uid'] == value['cluster_uid'] and old['manager_id'] == value['manager_id']
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
