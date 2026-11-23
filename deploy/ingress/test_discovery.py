import copy
import datetime
import unittest

import ingress_discovery


class DiscoveryContracts(unittest.TestCase):
    def fixture(self):
        expected = {'project_id': '11111111-1111-4111-8111-111111111111',
                    'cluster_uid': '22222222-2222-4222-8222-222222222222',
                    'manager_id': '33333333-3333-4333-8333-333333333333',
                    'scope_hash': 'a' * 64, 'private_cidr': '10.0.0.0/24'}
        manifest = {'schema_version': 1, **{k: expected[k] for k in ['project_id', 'cluster_uid', 'manager_id', 'scope_hash']},
                    'generation': '44444444-4444-4444-8444-444444444444',
                    'updated_at': '2026-11-23T10:00:00Z',
                    'targets': [{'node_uid': '55555555-5555-4555-8555-555555555555',
                                 'server_id': '66666666-6666-4666-8666-666666666666',
                                 'private_ip': '10.0.0.20', 'project_id': expected['project_id']}]}
        return expected, manifest

    def test_current_target_addresses_replace_historical_probe_addresses(self):
        expected, manifest = self.fixture()
        ips, timestamp = ingress_discovery.validate(manifest, expected, now=1795428000)
        self.assertEqual(ips, ['10.0.0.20'])
        self.assertEqual(timestamp, 1795428000)
        manifest['targets'][0]['private_ip'] = '10.0.0.21'
        self.assertEqual(ingress_discovery.validate(manifest, expected, now=1795428000)[0], ['10.0.0.21'])
        manifest['targets'] = []
        self.assertEqual(ingress_discovery.validate(manifest, expected, now=1795428000)[0], [])

    def test_foreign_and_ambiguous_manifests_cannot_change_probe_targets(self):
        expected, original = self.fixture()
        for field, value in [('project_id', 'foreign'), ('cluster_uid', 'foreign'), ('manager_id', 'foreign'),
                             ('scope_hash', 'b' * 64), ('schema_version', True), ('generation', '../../file')]:
            with self.subTest(field=field):
                manifest = copy.deepcopy(original)
                manifest[field] = value
                with self.assertRaises(ValueError):
                    ingress_discovery.validate(manifest, expected, now=1795428000)
        for ip in ['8.8.8.8', '10.1.0.20', '127.0.0.1']:
            manifest = copy.deepcopy(original)
            manifest['targets'][0]['private_ip'] = ip
            with self.assertRaises(ValueError):
                ingress_discovery.validate(manifest, expected, now=1795428000)
        manifest = copy.deepcopy(original)
        manifest['targets'] *= 2
        with self.assertRaises(ValueError):
            ingress_discovery.validate(manifest, expected, now=1795428000)


if __name__ == '__main__':
    unittest.main()
