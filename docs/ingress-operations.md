# Automatic private ingress workers

`clo-ingress` is the native companion for a Rancher-managed RKE2 cluster and the
two existing HAProxy edges. It runs on the fixed Rancher management host, outside
the served worker pools. It discovers eligible replacement workers and prepares
their host firewall before labeling them for Traefik. Once the owned Traefik Pod
is Ready, the real edge-to-worker /ping admission check gates HAProxy publication.

The manager makes **no CLO writes**: it cannot provision, start, stop, delete or
move an address. An operator still controls worker pool quantities. If every
eligible worker is deleted, service resumes after replacements actually become
Ready; there is no promise of serving traffic with zero ingress hosts. Select
`workload=general` in the intended private worker pools. Public/VoIP, tainted and
control-plane Nodes are excluded. Healthy accepted Node UIDs are retained; the
default maximum is two, with a configurable bound of one through eight.

`max_backends` limits the active HAProxy selection. Previously prepared workers
retain their firewall and readiness label when this limit is lowered; their
Traefik Pods may remain as prepared standby capacity. The manager does not remove
a persistent safety guard to reduce a backend count.

## Authority and installation

Run one fixed systemd installation; do not clone its machine-id/state or run a
second manager. This is a trusted administrative host with root access to its
selected workers and edges. Management and downstream Kubernetes credentials,
the CLO token and SSH keys stay on this host. Supplied Kubernetes credentials
must be maintained through normal certificate/token rotation.

The management Kubernetes identity needs get of the pinned provisioning Cluster,
get/list of its CAPI Machines, and get of current Machine-name `-machine-state`
Secrets in the configured namespace. Kubernetes RBAC cannot restrict dynamic
Secret names by prefix, so restrict this identity to the intended management
namespace. The downstream identity needs get of kube-system, get/list/patch of
Nodes, and get of the pinned Traefik DaemonSet/list of its Pods. Do not give these
credentials to Node agents. The program uses only inventory GET operations with
the CLO SDK; the token's actual provider-side permissions remain your responsibility.

Install both executables from the binary archive, then copy
`deploy/ingress/config.example.json` to `/etc/clo-ingress/config.json`, mode0600.
Replace every synthetic identity/address/hash. Pin the provisioning Cluster UID,
downstream kube-system UID, Traefik DaemonSet UID, manager VM ID and current
`/etc/machine-id`. Copy private management/downstream kubeconfigs to the declared
paths. The service's ProtectHome hides `/root`, so use `/etc/clo-ingress/` copies;
all credentials must be owned by the service UID, private, regular and single-linked.

Install the API token and existing edge SSH key as `/etc/clo-ingress/clo-token`
and `/etc/clo-ingress/edge-key`, mode0600. The supplied systemd unit exposes them
through LoadCredential at the example runtime paths. The CLO and Kubernetes
identities are pinned before any host preparation.

Seed `/var/lib/clo-ingress/known_hosts`, mode0600, with verified Ed25519 keys for
both edge VM UUID host aliases and the fixed controller VM UUID. These fixed hosts
use StrictHostKeyChecking=yes. New workers use their distinct native VM UUID as
host alias, record the first host key on the trusted project network, and refuse
subsequent host-key changes. Initial worker host keys are not independently
attested by the CLO API. A guest identity check verifies the driver-written
project/provider-name/attempt marker before any worker write.

Record the exact current HAProxy config SHA256 for each edge in adoption_sha256.
An existing unmanaged config is adopted only on that exact hash. Existing prepared
workers may be explicitly listed in adopt_node_uids; their current guard must
match the intended policy. Fresh replacement VM images need no adoption override.

Keep automatic=false and run `clo-ingress check --config <private-config>` using
credential paths available to that shell, then `once` for read-only live discovery.
`check` validates local files/host binding; `once` validates real API identities.
The systemd credential paths exist only while the unit runs. Check the private
journal's observed_candidates and verify the intended Nodes before enabling.
Install the unit, set automatic=true and start it. The companion binds metrics and
/readyz only on127.0.0.1:9130. The state directory is0700, its files0600.

## What replacement does

1. Join the current Node UID/name to its active Machine UID/NodeRef name and current
   machine-state archive. Use NodeRef UID too when Rancher supplies it. Reject an
   unverified project, attempt, boot attachment or private address.
2. Authenticate to that worker, validate its driver identity and install only the
   ingress nft table/unit/RKE2 dependency drop-in. Allow loopback/self and the two
   edge /32s on8000/8443/9000, plus the health observer /32 on9000. Reject other
   ingress-port traffic. Existing SSH/API/SIP/RTP and unrelated tables are preserved.
   Validate syntax before applying; never restart RKE2 for preparation.
3. Add ingress-ready with Node UID/resourceVersion preconditions, then wait for the
   pinned DaemonSet's Ready Pod on that exact Node. The label is deliberately absent
   until preparation succeeds; merely putting it on every new pool is insufficient.
4. Validate a complete HAProxy candidate, verify /ping from each reachable edge,
   atomically replace its managed config and gracefully reload it. A unique durable
   generation has a root-only Unix runtime socket; reading its running server state
   proves the new targets loaded. A lost SSH response is reconciled using host
   intents/runtime generation. Replaying an unknown cloud operation is never needed.

An authoritative removed/replaced Node withdraws its old backend before preparing
a new one, even when a private address is reused. API/discovery errors introduce no
new targets or speculative removals. They raise reconciliation errors and leave
the previously accepted data path running. A stopped/unreachable fixed edge is not
started automatically; its managed config is reconciled when it can be reached.
Keep the service available during Rancher worker scaling.

HAProxy config is now owned by this manager. Do not overwrite it with the legacy
static render command; unexpected unmanaged file changes are refused. The VIP
controller's original config, journal, pending operation and state machine are not
modified. Its runtime probes the fixed edge8405 endpoints, which already reflect
the current backend pool. Existing RecoveryRequired requires its own recovery.

## Dynamic external health

Every successful reconciliation publishes non-secret discovery.json on the fixed
controller under `/var/lib/clo-ingress/`. Its project, cluster, manager, scope hash,
generation, timestamp and at-most-eight private Node/VM identities are validated by
`deploy/ingress/ingress_discovery.py`. Integrate this helper into the existing forced
read-only SSH health exporter and its offhost observer: probe the manifest's targets,
validate the same expected identities on both ends, and preserve legacy static mode
until dynamic mode is explicitly configured. Corrupt dynamic data must not silently
fall back to historical addresses.

Export `rave_clo_ingress_discovery_enabled`, `..._valid`,
`..._last_success_timestamp_seconds` and `..._targets` with the existing cluster label.
`deploy/ingress/alerts.yml` and its rule tests cover stale/invalid discovery. Existing
public HTTPS and edge export health alerts remain necessary. Discovery contains no
API token, Kubernetes credential or private SSH key.

Back up the service config, credentials, exact binary/version and its private state
through your existing protected administrative backup system. Worker SSH keys can
also be recovered from current Rancher archives. This does not add controller-host
HA or automated takeover. Changes to pinned host/cluster/DaemonSet identities need
an explicit scope migration rather than deleting a journal to suppress an error.
CSI enrollment, its boot-serial registry, volume fencing and telephony remain separate.

## Failure reconciliation

Each reachable edge is reconciled independently. Obsolete addresses are withdrawn
before health admission of newly added addresses; a failed remaining `/ping`
cannot preserve a removed endpoint. A lagging edge can first receive a safe subset
and retain pending admission for the complete desired generation.

Every cycle proves the loaded generation. An unchanged target set reuses the same
generation, including when one edge is stopped. Closed candidate/backup files are
bounded to eight generations plus the current generation. A proven rejected
reload restores the last accepted boot configuration; an uncertain result retains
its intent and is not blindly replayed.

A missing archive preserves only a previously accepted Node/Machine/native binding
whose complete CLO ownership still matches. It permits no new adoption. A confirmed
Node loss or IP reassignment still withdraws the obsolete target. Healthy reserves
are considered after failed preparation or admission, with a bounded shared budget.
