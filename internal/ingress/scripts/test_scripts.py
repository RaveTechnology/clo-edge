import copy
import contextlib
import hashlib
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import apply_edge
import prepare_node


class HostContracts(unittest.TestCase):
    def test_new_worker_preparation_resumes_after_guest_side_effect_without_adoption(self):
        c = {'node': {'project_id': 'project', 'provider_name': 'worker', 'attempt': 'attempt',
                      'node_uid': 'node', 'server_id': 'server', 'private_ip': '10.0.0.20'},
             'manager_id': 'manager', 'scope_hash': 'scope', 'edge_ips': ['10.0.0.10', '10.0.0.11'],
             'observer_ip': '10.0.0.12', 'adopt_existing': False}
        actual = {'schemaVersion': 1, 'projectID': 'project', 'providerName': 'worker', 'attempt': 'attempt'}
        fail_enable = [True]
        def run(args):
            if args[:2] == ['systemctl', 'enable'] and fail_enable[0]:
                fail_enable[0] = False
                raise OSError('lost session after firewall files and kernel were updated')
            if args[:2] == ['systemctl', 'show']: return '1234\n'
            if args[:3] == ['nft', '-s', '-j']: return '{"nftables":[]}'
            return 'active\n'
        def read(path):
            if str(path) == '/etc/clo/machine-identity.json': return json.dumps(actual).encode()
            return path.read_bytes()
        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as patches:
            root = Path(directory) / 'state'; root.mkdir()
            files = Path(directory) / 'guest'; files.mkdir()
            for name, value in {'ROOT': root, 'RULES': files/'guard.nft', 'UNIT': files/'guard.service', 'DROPIN': files/'agent.d'/'guard.conf'}.items():
                patches.enter_context(patch.object(prepare_node, name, value))
            patches.enter_context(patch.object(prepare_node, 'private_dir'))
            patches.enter_context(patch.object(prepare_node, 'read_regular', side_effect=read))
            patches.enter_context(patch.object(prepare_node, 'atomic', side_effect=lambda p, b, mode=0o600: p.write_bytes(b)))
            patches.enter_context(patch.object(prepare_node, 'run', side_effect=run))
            patches.enter_context(patch.object(prepare_node.os, 'geteuid', return_value=0))
            patches.enter_context(patch.object(prepare_node.os, 'open', return_value=42))
            patches.enter_context(patch.object(prepare_node.fcntl, 'flock'))
            patches.enter_context(patch.object(prepare_node.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout=b'{"nftables":[]}')))
            for attempt in range(2):
                with patch.object(prepare_node.sys, 'stdin', SimpleNamespace(buffer=io.BytesIO(json.dumps(c).encode()))), patch('builtins.print'):
                    if attempt == 0:
                        with self.assertRaises(OSError): prepare_node.main()
                    else:
                        prepare_node.main()
            self.assertTrue(json.loads((root/'receipt.json').read_text())['persistent_guard'])

    def test_reload_waits_for_the_new_runtime_socket_without_reloading_again(self):
        with patch.object(apply_edge, 'live', side_effect=[FileNotFoundError(), ConnectionRefusedError(), 'verified-state']) as live, patch.object(apply_edge.time, 'sleep'):
            self.assertEqual(apply_edge.wait_live('generation', ['10.0.0.20']), 'verified-state')
            self.assertEqual(live.call_count, 3)
        with patch.object(apply_edge, 'live', side_effect=FileNotFoundError()), patch.object(apply_edge.time, 'monotonic', side_effect=[0, 11]):
            with self.assertRaises(ValueError):
                apply_edge.wait_live('generation', [])

    def test_edge_candidate_validates_utf8_content_and_ownership_prefix(self):
        generation = '11111111-1111-4111-8111-111111111111'
        content = '# Managed by clo-ingress; generation ' + generation + '\nglobal\n'
        value = {'generation': generation, 'config': content,
                 'config_sha256': hashlib.sha256(content.encode()).hexdigest()}
        self.assertEqual(apply_edge.candidate_content(value), content.encode())
        for field, bad in [('generation', 'foreign'), ('config_sha256', '0' * 64)]:
            changed = dict(value, **{field: bad})
            with self.assertRaises(ValueError):
                apply_edge.candidate_content(changed)

    def test_equivalent_edge_order_preserves_existing_guard_adoption(self):
        forward = prepare_node.guard_rules('10.0.0.20', ['10.0.0.10', '10.0.0.11'], '10.0.0.12')
        reverse = prepare_node.guard_rules('10.0.0.20', ['10.0.0.11', '10.0.0.10'], '10.0.0.12')
        self.assertEqual(forward, reverse)

    def test_guard_rejects_aliasing_and_nonprivate_health_topology(self):
        value = prepare_node.guard_rules('10.0.0.20', ['10.0.0.10', '10.0.0.11'], '10.0.0.12')
        self.assertIn('tcp dport 9000 ip saddr 10.0.0.12/32 accept', value)
        self.assertNotIn('tcp dport { 8000, 8443, 9000 } ip saddr 10.0.0.12', value)
        self.assertIn('counter reject with tcp reset', value)
        for ip, observer in [('8.8.8.8', '10.0.0.12'), ('10.0.0.20', '10.0.0.10')]:
            with self.assertRaises(ValueError):
                prepare_node.guard_rules(ip, ['10.0.0.10', '10.0.0.11'], observer)

    def test_changed_attempt_cannot_prepare_another_guest(self):
        c = {'node': {'project_id': 'project', 'provider_name': 'owned-worker', 'attempt': 'current'}}
        good = {'schemaVersion': 1, 'projectID': 'project', 'providerName': 'owned-worker', 'attempt': 'current'}
        prepare_node.check_identity(c, good)
        bad = copy.deepcopy(good)
        bad['attempt'] = 'previous'
        with self.assertRaises(ValueError):
            prepare_node.check_identity(c, bad)

    def test_kernel_counter_changes_do_not_cause_policy_reinstallation(self):
        before = {'nftables': [{'rule': {'handle': 3, 'expr': [{'counter': {'packets': 1, 'bytes': 20}}, {'reject': {'type': 'tcp reset'}}]}}]}
        counters = copy.deepcopy(before)
        counters['nftables'][0]['rule']['handle'] = 500
        counters['nftables'][0]['rule']['expr'][0]['counter']['packets'] = 900
        self.assertEqual(prepare_node.kernel_fingerprint(before), prepare_node.kernel_fingerprint(counters))
        weakened = copy.deepcopy(before)
        weakened['nftables'][0]['rule']['expr'][1] = {'accept': None}
        self.assertNotEqual(prepare_node.kernel_fingerprint(before), prepare_node.kernel_fingerprint(weakened))

    def test_runtime_requires_both_exact_backend_pools(self):
        state = '1\n# be_id be_name srv_id srv_name srv_addr srv_op_state\n3 http-ingresses 1 ingress-0 10.0.0.20 2\n4 tls-ingresses 1 ingress-0 10.0.0.20 2\n'
        apply_edge.verify_runtime_state(state, ['10.0.0.20'])
        for bad in [state.replace('10.0.0.20', '10.0.0.99'), state.split('4 tls')[0], state + state.splitlines()[-1] + '\n']:
            with self.assertRaises(ValueError):
                apply_edge.verify_runtime_state(bad, ['10.0.0.20'])


if __name__ == '__main__':
    unittest.main()
