"""Validated non-secret discovery for restricted SSH health exporters/observers.

Legacy exporters keep using their static targets until explicitly configured
with an expected project/cluster/manager/scope and this discovery protocol.
"""
import datetime
import ipaddress
import json
import os
import stat
import time
import uuid


def require(ok, message):
    if not ok:
        raise ValueError(message)


def read_private_json(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, 'rb') as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and info.st_uid == os.getuid()
                and not info.st_mode & 0o077 and info.st_size <= 1024 * 1024, 'untrusted discovery file')
        return json.load(source)


def validate(value, expected, now=None):
    require(isinstance(value, dict) and type(value.get('schema_version')) is int and value['schema_version'] == 1,
            'unsupported ingress discovery schema')
    for name in ['project_id', 'cluster_uid', 'manager_id', 'scope_hash']:
        require(value.get(name) == expected[name], 'foreign ingress discovery identity')
    try:
        uuid.UUID(value['generation'])
        network = ipaddress.IPv4Network(expected['private_cidr'], strict=True)
        stamp = datetime.datetime.fromisoformat(value['updated_at'].replace('Z', '+00:00'))
        require(stamp.tzinfo is not None, 'missing discovery timezone')
        timestamp = stamp.timestamp()
    except (ValueError, KeyError, TypeError, AttributeError) as error:
        raise ValueError('invalid ingress discovery metadata') from error
    require(timestamp > 0 and timestamp <= (time.time() if now is None else now) + 90, 'future discovery timestamp')
    targets = value.get('targets')
    require(isinstance(targets, list) and len(targets) <= 8, 'invalid ingress target bound')
    ips, nodes, servers = [], set(), set()
    for target in targets:
        require(isinstance(target, dict) and target.get('project_id') == expected['project_id'], 'foreign ingress target')
        try:
            node = str(uuid.UUID(target['node_uid']))
            server = str(uuid.UUID(target['server_id']))
            address = ipaddress.IPv4Address(target['private_ip'])
        except (ValueError, KeyError, TypeError) as error:
            raise ValueError('invalid target identity/address') from error
        require(address.is_private and address in network and str(address) not in expected.get('forbidden_ips', []),
                'target outside permitted private worker network')
        require(node not in nodes and server not in servers and str(address) not in ips, 'ambiguous target incarnation')
        nodes.add(node)
        servers.add(server)
        ips.append(str(address))
    return ips, timestamp


def load(path, expected):
    value = read_private_json(path)
    validate(value, expected)
    return value
