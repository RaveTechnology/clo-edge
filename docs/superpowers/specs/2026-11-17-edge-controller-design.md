# CLO edge controller v0.1 design

Date: 17 November 2026. The owner delegated placement and implementation choices
and explicitly requested implementation without intermediate approvals.

## Intent and boundaries

Automate the already tested two-HAProxy / one movable IPv4 topology. Preserve
source IP with PROXYv2; TLS and application routing stay in private Kubernetes.
SIP/RTP, storage, production deployment, VM provisioning and a general CCM are
outside this change. No zero-downtime or measured automatic RTO is claimed yet.

## Approaches and chosen scope

The owner delegated this choice to the implementer. Choose one private management
VM, outside both edge VMs and the served Kubernetes cluster. It runs one Go
process under systemd, with durable state on a local disk and a lifetime flock.
An alternative management-Kubernetes deployment avoids an extra VM but requires
safe issuer handover; a Lease alone does not fence cloud requests. Two competing
edge-side controllers likewise require a separate coordination authority. Neither
complexity is justified for v0.1. Controller-host HA is deliberately deferred:
traffic continues if this host fails, but failover waits for host recovery.

This is one installation on one fixed host. Do not clone its state or run it on
another host. Host replacement requires operator termination/fencing of the old
issuer and resolution of its pending native API jobs; there is no timed takeover.

## Configuration and ownership

Strict JSON configuration binds installation to a project, two distinct dedicated
edge server IDs/private IPv4s, an independent controller server ID, one external
IPv4 ID/literal value, its bandwidth (100 or 1024 Mbps), two ingress private IPs,
public HTTPS hostname/health path, optional CA, and timers. No credentials in
configuration/state. Public examples use documentation addresses and placeholders.

Defaults: observe only; polls every 5 s, probes at most 2 s, continuous suspect
window 30 s, cloud convergence deadline 180 s, post-failover cooldown 600 s.
Automatic mutation needs explicit configuration and offline `arm`. A completed
failover disarms the installation. Re-arming is explicit after repairing the
stopped old edge. There is no automatic failback, start, reboot, VM/IP creation,
deletion, cloud firewall editing or management-IP mutation.

Before writes, verify complete project server/address membership, controller is
not an edge, exact IP/ID/type/entity/bandwidth, known owner, source/target identities
and VM state. Foreign attachment, changed config, deleted configured resources,
incomplete inventory or ambiguous state close the write gate. Only dedicated
edge VMs may be power-fenced; they must not host Kubernetes/application workloads.

## Health and decision

HAProxy provides a private-only `/healthz` endpoint reporting its configured edge
identity and success only while both HTTP and TLS backend pools have an available
target. It is firewalled to the controller by the operator. Public frontend never
accepts PROXY metadata; backends send PROXYv2. Health port is separate from data
ports and is not publicly exposed by the generated config.

The independent controller probes the active and standby private health endpoints
and the public HTTPS path. Public requests dial the configured VIP directly while
checking the configured hostname and CA; do not trust DNS changes, redirects,
environment proxies or ignored certificate errors. Require exact private identity
body. Health requests use new connections and bounded body reads.

Trigger only while armed, outside cooldown, public AND active-private checks fail
continuously for at least 30 s AND standby-private succeeds throughout that window.
Healthy public path, healthy active-private path, shared ingress outage, unhealthy
standby, missing/stale observations or an API outage must not trigger a move.
This conservative rule deliberately does not remediate public-only path failures.
There is no majority quorum or claim to diagnose every network partition.

## Durable workflow and uncertain outcomes

One serialized loop, one pending command, one mutation per iteration. State is
strictly versioned and bound to the complete validated config plus local machine
ID. Atomic temp-write/fsync/rename/directory-fsync; any storage failure stops the
process before further commands. `init` is explicit and refuses existing state;
`run` never silently initializes. A second process on this host cannot acquire
the same state lock. A paused old process retains its lock indefinitely.

State progression: Watching → Stopping → Detaching → Attaching → Verifying →
Completed (disarmed). Before each POST, save a dispatched pending intent. Send
exactly once. Save acceptance only after native 202. Confirm terminal conditions
with subsequent reads before advancing. Before detaching or attaching, source
must still be **STOPPED AND OFF**; OFF during STOPPING is insufficient.

For lost response, timeout, malformed success, rejection, or crash between dispatch
and durable acceptance: RecoveryRequired, no blind retry and no next mutation,
even if a later GET appears to match the intended result. A restart may continue
an accepted operation by reading its terminal postcondition; it never resends it.
Accepted operations that exceed the deadline also require manual recovery.
Native API has no confirmed idempotency key or authoritative pending-job receipt.
No force-unlock or generic reset command is supplied. Operator repair must settle
native jobs and reconcile ownership before commissioning a fresh installation.

For source already STOPPED/OFF, record that observation without sending stop.
Detach only when source owns the configured address; attach only when its native
type is FREE, status is DOWN, with no attachment. Attach keeps exact bandwidth and entity=server.
After attach confirm ACTIVE/exact target; verify public TLS/HTTP AND target-private
health, then mark completed. Verification failure pauses, without moving back.
No other actor may manage this IP while automatic operation is armed.

## Program and packaging

Go 1.27, Linux amd64. Reuse the official CLO SDK v3.2.0 with automatic SDK retry
disabled and logging discarded; the existing public cloud adapter drops address
entity/bandwidth and has no attach/detach methods, so keep this edge-specific
adapter here rather than release an unrelated shared-library change. Explicitly
check native response codes/envelopes, bounded GET retry/pacing, no POST retries,
no redirects and no provider body/token in diagnostics.

Commands: `init`, `arm`, `run`, `inspect`, `render-haproxy`, `version`.
State lock protects all modifying commands. `inspect` reads atomic state without
acquiring the exclusive daemon lock. systemd uses a dedicated non-root account,
StateDirectory, read-only configuration, LoadCredential and restricted filesystem.
Container packaging documents single fixed host/local state/machine-id mounts;
no replicated Deployment is supplied. Prometheus text/status are loopback-only.

## Acceptance

Test real local files, kernel locking and HTTP/TLS servers. Cover unchanged
config/host bindings, unknown fields, corrupt/missing state, competing process,
failed journal write, untrusted/redirected TLS, missing inventory pages, foreign
owners/entity, source STOPPING/OFF, delayed 202, ambiguous POST/response loss,
restart at every durable boundary, no blind repeat, shared ingress failure,
standby failure, false suspicion, cooldown, manual re-arm and no failback.

CLI integration runs through an isolated fake CLO HTTP server and real probes;
verify exactly scoped native calls and crash recovery. Build binary/container,
validate generated HAProxy config and run independent final branch review.
A disposable native CLO failover test, if run, gets a new ownership journal and
explicit cleanup proof; never replay the completed 17 November experiment scripts.
Production deployment remains a separate operation.

## Evidence and sources

- [Completed data-plane experiment](../../data-plane-2026-11-17.md).
- [CLO network model](https://clo.ru/help/network/local): L3, VRRP unavailable.
- [CLO OpenAPI](https://api.clo.ru/openapi.json), read 17 November 2026,
  101 paths, SHA256 `8bd421d34daf95df60e7a3efc32a9ded77a558b36660fec542995c325c088c32`.
- [Kubernetes leader-election implementation](https://github.com/kubernetes/client-go/blob/master/tools/leaderelection/leaderelection.go)
  explicitly does not guarantee fencing.
