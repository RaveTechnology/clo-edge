import copy
import hashlib
import unittest
from unittest.mock import patch

import apply_edge
import prepare_node


class HostContracts(unittest.TestCase):
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
