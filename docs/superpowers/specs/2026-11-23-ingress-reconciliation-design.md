# Automatic Rancher ingress node replacement

An operator must be able to delete and recreate private general workers through
Rancher without manually copying firewall rules, labeling Nodes, or editing
HAProxy addresses. Losing every worker necessarily interrupts ingress until a
replacement becomes Ready. The manager does not create workers or override a
pool's desired quantity.

The owner requested the previously described firewall/readiness/backend/label
automation and previously delegated implementation choices without intermediate
approvals. The choices below are implementation decisions, not additional user
requirements. Existing public Apache-2.0 distribution and privacy constraints
continue to apply.

## Approaches

1. **Pool labels and cloud-init only:** prepares new hosts, but cannot safely
   handle changing backend addresses or withdraw reused addresses. Editing the
   current machine configuration can also replace working Nodes.
2. **An in-cluster controller/privileged preparation agent:** can discover Nodes,
   but disappears when all general workers disappear and introduces host-level
   agents on every candidate.
3. **A separate manager on the existing Rancher host, selected here:** uses the
   already available management/downstream kubectl and Rancher machine archives,
   validates CLO ownership, prepares workers over SSH, and publishes dynamic
   HAProxy configurations. It adds no permanent VM. The host remains a trusted
   administrative host and is a single manager, not a new HA control plane.

The manager is a separate `clo-ingress` native Go executable in this repository.
It does not call the VIP controller's Stop/Detach/Attach methods or change its
configuration or journal. The existing VIP controller probes fixed edge health
endpoints; those reflect the dynamically configured backend health already.

## Scope and invariants

- Pin the CLO project, management VM/machine-id, provisioning Cluster UID and
  downstream kube-system UID. One fixed-host journal, lifetime flock, atomic
  fsync/rename writes, bounded inputs, no symlink/shared-state takeover.
- Candidate selector is `workload=general`, Ready, schedulable Linux/amd64 private
  workers; dedicated control-plane and VoIP nodes are excluded. At most two are
  selected by default; a bounded configurable maximum supports maintenance tests.
  Retain healthy selected Node UIDs to avoid churn; otherwise use stable order.
- A Node must join one active CAPI Machine/NodeRef and a completed current
  machine-state archive. Pin Node UID, Machine UID, CLO VM ID, provider name and
  attempt. Verify complete CLO server/address/boot inventories, project ownership,
  private IP, boot attachment, no public address, and guest identity before writes.
  Pending archives are ordinary convergence, not permission to use unverified IPs.
- API/discovery failure permits no new adoption or speculative withdrawal.
  An authoritative removed/replaced/unready identity withdraws its backend.
  Empty authoritative selection publishes empty backends; stale IPs never remain
  merely because a new Node has not appeared. Neither the manager nor tests delete
  or recreate user Nodes.
- Keep credentials on the management host. Workers receive neither CLO API token
  nor central Kubernetes credentials. SSH key material from Rancher is bounded,
  private, never logged or included in public state/metrics. New worker SSH uses
  identity-bound host aliases and records host keys; known host changes are refused.
- Guest writes are limited to the existing ingress nft table/unit and an RKE2
  dependency drop-in. Allow loopback/self, both edge /32s on 8000/8443/9000 and
  the independent health observer /32 on 9000; reject other ingress-port traffic.
  Validate nft before applying; preserve SSH, API, SIP/RTP and other tables. Do not
  restart RKE2. Reboots require the persistent guard before RKE2.
- Only after verified guest preparation add the UID-preconditioned
  `clo.rave-technology.io/ingress-ready=true` label. Wait for this Node's owned
  Traefik Pod and private /ping before publishing it. This ordering breaks the
  label/DaemonSet readiness cycle without exposing an unprepared host.
- Validate complete HAProxy candidates, then atomically install and gracefully
  reload each reachable owned edge. Reconcile uncertain SSH results by reading
  live state; do not infer loaded configuration solely from a successful write.
  A unique durable publication generation has its own root-only Unix stats socket,
  so live `show servers state` proves which configuration generation is running.
  Retain previous config for rollback of a rejected candidate. Never restart,
  power off, move an IP, or alter PROXY/header/TLS policy to update backends.
- An unavailable stopped edge retains a pending application until it returns;
  the reachable active edge can continue serving. The manager does not start it.
- Export the committed discovery manifest to the existing fixed controller's
  private filesystem. Its forced read-only health export and the offhost observer
  validate project/cluster/manager identity, bounded target count and private IPs,
  and probe current targets instead of the historical hardcoded list. Legacy
  static exports remain compatible unless dynamic mode is explicitly enabled.
  Corrupt dynamic exports fail validation; no silent fallback masks the error.

## Interfaces and files

- `internal/ingress`: scope/config, candidate verification/selection, journal,
  reconciliation engine and mutation ordering, runtime adapters and embedded guest
  scripts. Pure core interfaces permit failure tests without real cloud changes.
- `internal/provider/inventory.go`: complete read-only SDK inventory, retaining
  existing pagination/redirect/response bounds.
- `internal/edge/haproxy.go`: additive bounded dynamic-target renderer. Legacy
  renderer and all VIP state-machine behavior remain unchanged.
- `cmd/clo-ingress`: `version`, `check`, `once`, `run`; observe-only by default,
  explicit enabled installation; signal cancellation, private files and metrics.
- `deploy/ingress`: example configuration, systemd service, monitoring/discovery
  helper and portable tests. Public examples use synthetic identities only.
- Packaging builds and ships the new native executable. The scratch edge image
  remains the VIP controller image; the SSH/kubectl-based companion runs natively.

The runtime uses explicit management/downstream kubeconfig paths. The production
host's root privileges already administer Rancher; document the minimum Kubernetes
permissions (read Machines and their machine-state Secrets, read Nodes/Pods and
patch Node labels). CLO credentials are read-only in this program's interface.

## Acceptance

Unit/failure tests cover wrong project/IP/attempt/boot/UID, incomplete inventory,
reused IP with new VM/Node identity, archive limits, failed firewall, startup races,
one/zero/replaced candidates, unknown SSH outcome, partial edge application,
restart/idempotence, malformed live HAProxy state and manifest identity. Test real
filesystem atomicity and actual HAProxy parsing/runtime reload where available.

Install observe-only first on the existing Rancher host, adopt the two already
prepared workers without changing their RKE2 PIDs, then enable reconciliation.
Public HTTPS and both private edge/backend paths remain monitored.

Live replacement acceptance uses one **owned temporary Rancher worker**, with the
maximum temporarily raised to three so the two user workers remain selected.
Verify automatic firewall/label/Traefik/backend publication, then remove only the
owned test worker via its pool quantity. Temporarily occupy its released private
address with one owned tiny helper, recreate the test worker and verify a new
UID/address is discovered without hand edits. Continuously sample public HTTPS;
preserve both user worker UIDs. Remove only the test pool/MachineConfig/helper and
their exact owned native resources; restore the default maximum two. Stop on any
unknown creation outcome and reconcile instead of replaying requests.

The existing unrelated CSI recommissioning and unresolved VIP RecoveryRequired
incident remain explicitly outstanding. Do not clear either by changing labels or
journals outside this manager's scope. Update operational discovery/protections,
backups and metrics for the new component without claiming full CSI/HA acceptance.

References: [Kubernetes Nodes](https://kubernetes.io/docs/concepts/architecture/nodes/),
[HAProxy 2.8 runtime protocol](https://docs.haproxy.org/2.8/management.html).
