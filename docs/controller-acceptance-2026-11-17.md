# Controller acceptance, 17 November 2026

The controller is an experimental single-host implementation. These results
qualify local software boundaries; production acceptance remains open.

## Local checks

The current suite passes 64 test cases including subcases, with race detection
and vet. One independent whole-branch review found three Important issues. Each
was reproduced and fixed: malformed/truncated202 acknowledgement, confirmed
native ownership violations mistaken for transport errors, and impossible suspect
window fields. The journal also uses a SHA256 envelope to detect accidental
valid-JSON bitrot. No distributed fencing or protection against a hostile host
administrator is implied.

- Real Linux kernel lock excludes a competing process. A new host identity,
  corrupted state, unknown fields and a changed configuration are rejected.
- Real atomic/fsync journal survives close/reopen. A failed durable write prevents
  the native command. No silent run-time initialization or generic force-reset.
- Private health checks require the exact edge identity. Public HTTPS dials the
  configured address and validates the hostname/CA. Redirects are refused.
- The production SDK adapter checks complete project inventories, server identity,
  attachment entity, address/bandwidth and native response codes. No POST retries.
- A composed reconciler + SDK + real local HTTP fake CLO + real journal follows
  asynchronous STOPPING/OFF → STOPPED/OFF, detach and attach. It restarts its engine
  between ticks. Only the configured stop/detach/attach sequence is sent once.
- Dropped HTTP responses after each of those three native effects leave
  RecoveryRequired. Later matching reads do not cause replay or follow-up writes.
- Healthy/public-only/private-only/shared/standby failures and stale observations
  do not trigger fencing. Continuous failure, source already off, cancellation
  before stop, source restart before attach and accepted-attach recovery are covered.
- CLI init/run/arm/inspect, observe-only, cancellation and loopback listener checks
  run without real cloud credentials. Success disarms; cooldown/re-arm are explicit.
- `make check haproxy-check package`: race tests, vet, JSON checks, Linux amd64
  binary/archive and real HAProxy2.8.31 config parser.
- Container build uses a digest-pinned Go1.27.1 builder and scratch runtime,
  UID/GID65532. Version smoke test runs without network. systemd unit is checked
  with ExecStart redirected only to the checked local binary (no host installation).

The fake CLO tests verify software safety under controlled effects; they cannot
prove provider consistency guarantees, VM fencing physics, network partitions,
cloud-job timing, automatic RTO or prolonged operation. The separate
[data-plane experiment](data-plane-2026-11-17.md) remains the native network proof.
No new native controller acceptance is claimed by these local checks.

The subsequent [native controller lab](controller-native-2026-11-17.md) passed
automatic fencing/move/verification and cleanup. It also corrected the real
FIXED address-kind contract and systemd credential materialization. Those
native results are qualified separately from the local HTTP boundary tests.

An initial native preparation attempt did not reach controller commissioning:
the workstation HTTPS precondition failed. Its four VMs and boot volumes were
deleted and baseline independently verified. The exact same probe and generated
HAProxy configuration subsequently passed a local two-backend PROXYv2/TLS test.
This failed preparation is not counted as a successful native controller test.

## Remaining deployment boundaries

One operator-owned installation on one fixed host. Do not clone state or run
another controller against the same IP; no provider-side fencing token exists.
Dedicated edge VMs and firewalling are deployment prerequisites. A failed
controller host stops automation; there is no automatic issuer takeover. A lost
native response may require provider/operator intervention. False or ambiguous
health observations deliberately prefer stopping automation over a speculative
move. Existing connections reconnect after a real failover.

SIP/RTP, production data, RKE2 production integration and the separate CSI native
resize limitation are outside this acceptance. Publishing an image is not
permission to deploy it to production.
