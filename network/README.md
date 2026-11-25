# Public interface companion

This companion configures a hot-plugged public interface after the VIP controller
has fenced the old VM and transferred the address. The cloud API is read-only.
It does not stop VMs, move addresses, clear recovery journals, or arm failover.

Install `controller.py` on the fixed controller host and `guest_helper.py` plus
`guest_network.py` under `/usr/local/lib/clo-edge-network/` on both dedicated edge
VMs. Python 3, iproute2 and OpenSSH are required. Keep the private API token on the
controller only. The controller needs root to inspect the private journal and use
its dedicated SSH identity; the core failover service remains its separate user.

Create controller-only `/etc/clo-edge-network/controller.json` (0600):

```json
{
  "controller_machine_id": "PINNED_CONTROLLER_MACHINE_ID",
  "core_config_hash": "NORMALIZED_CORE_CONFIG_HASH_FROM_INSPECT",
  "core_config_path": "/etc/clo-edge/config.json",
  "core_config_file_sha256": "SHA256_OF_EXACT_CONFIG_FILE",
  "core_state_dir": "/var/lib/clo-edge",
  "token_file": "/etc/clo-edge/api-token",
  "project_id": "PROJECT_UUID",
  "address_id": "ADDRESS_UUID",
  "public_ip": "192.0.2.20",
  "prefix": 24,
  "gateway": "192.0.2.1",
  "ssh_key_file": "/etc/clo-edge-network/id_ed25519",
  "known_hosts_file": "/etc/clo-edge-network/known_hosts",
  "edges": [
    {"server_id": "EDGE_A_UUID", "machine_id": "PINNED_A_MACHINE_ID", "private_ip": "10.0.0.10"},
    {"server_id": "EDGE_B_UUID", "machine_id": "PINNED_B_MACHINE_ID", "private_ip": "10.0.0.11"}
  ]
}
```

Each `/etc/clo-edge-network/guest.json` (root-owned 0600) contains that guest's
`server_id`, `machine_id`, `project_id`, `private_ip`, `public_ip`, `prefix`,
`gateway`. Generate a new SSH identity on the controller. Pin independently
verified edge host keys; never disable host verification. Add its public key to
each edge's root `authorized_keys` without replacing existing keys:

```text
restrict,from="CONTROLLER_PRIVATE_IP",command="/usr/bin/python3 /usr/local/lib/clo-edge-network/guest_helper.py" ssh-ed25519 PUBLIC_KEY
```

The forced helper accepts bounded expiring JSON only. MAC, host, project, address,
private network, route and rule conflicts are checked before applying. Only the
matching public NIC, table 200, source rule priority 32000 and that NIC's rp_filter
may change. Private interface configuration, routes and unrelated rules must
remain identical. Conflicts fail closed; inspect them instead of deleting routes.

Create `/var/lib/clo-edge-network` root-only, install the supplied units, then run
`systemctl daemon-reload` and `systemctl enable --now clo-edge-network.timer`.
The timer makes no cloud reads during stopping/detaching. A stable healthy owner
has a bounded 60-second observation cache; a transfer is always freshly inspected.
Recovery mode does not authorize an unknown attach or an unfenced peer.

Observe `journalctl -u clo-edge-network.service` and its private `state.json`.
A `ready` receipt proves guest configuration for that observation, not HTTP HA.
Commission with a synthetic public observer running before a bounded edge fault;
accept only a terminal native fence, exact new owner, public HTTPS and the core's
normal Completed state. A failed drill remains a failed drill even if an offline
operator subsequently restores service. Existing TCP/WebSocket sessions reconnect.

The controller is a single fixed host. This helper does not make that host HA.
