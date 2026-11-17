# Native controller acceptance, 17 November 2026

The experimental controller completed a real CLO failover, including automatic
source-VM shutdown and a movable IPv4 transfer. This is one qualified lab sample,
not production acceptance or an SLA. [Sanitized evidence](controller-native-2026-11-17.json).

## Scope

Four disposable Ubuntu 24.04 VMs, each 1 shared vCPU / 2 GB RAM / 10 GB boot: two HAProxy
edges, a private independent controller and an auxiliary fixture VM. Two synthetic
HTTP/TLS ingress fixtures ran on the controller/fixture VMs for this test only.
This lab did not run Kubernetes; the earlier
[K3s/Traefik data-plane experiment](data-plane-2026-11-17.md) remains separate.
The existing project NAT and two reserved public IPv4 resources were reused.

The actual packaged systemd service ran as `clo-edge`. Both offline admin
templates (`init` and `arm`) executed successfully. The token was a mode 0600
regular file inside a private volatile RuntimeDirectory; it was not put in the
state journal. A nonzero live PID and advancing observation were required before
acceptance, rather than trusting an armed journal alone.

## Results

| Scenario | Observed result |
|---|---|
| Independent public HTTPS before the fault | GitHub-hosted runner: 5/5, pinned IPv4, verified hostname/private lab CA |
| Both synthetic ingress fixtures stopped | No edge shutdown or IP move during 45 seconds of checks |
| Active HAProxy service stopped | Controller detected persistent public/private failure and fenced the dedicated source VM |
| Terminal fence | STOPPED and OFF confirmed before detach/attach; 7 independent observed checks |
| First successful workstation request start | 128.467 s after the fault command began |
| First successful workstation response complete | 129.472 s after the fault command began |
| Controller recorded completion | 136.248 s after the fault command began |
| Reserve VM | Boot ID and HAProxy service start/restart counters unchanged |
| Independent public HTTPS after transfer | GitHub-hosted runner: 5/5 |
| Controller process restarted | Retained completed/disarmed state; source stayed stopped, no failback |
| Cleanup | All 4 VMs and 4 boot volumes404; exact original inventory restored |

Independent public probes:
[before](https://github.com/RaveTechnology/clo-edge/actions/runs/44161529169),
[after](https://github.com/RaveTechnology/clo-edge/actions/runs/44162394545).
Only public IP/hostname/CA information was supplied as ephemeral probe config;
no CLO API credential went to those jobs. That configuration was removed.

## Method and limits

Controller defaults were used: polls 5 s, probes 2 s, persistent suspicion 30 s,
per-phase convergence 180 s and re-arm cooldown 600 s. Fault injection stopped the
active HAProxy service, not its VM. The controller itself sent the stop command,
waited for native STOPPED/OFF, detached the known IPv4, attached it to the healthy
reserve, verified TLS/HTTP and disarmed. There was no operator-driven IP transfer
in this accepted fault sequence. The native address kind is FIXED when attached
to a server; detached terminal state is type=FREE/status=DOWN without attachment.

The workstation observer used fresh connections, verified TLS and standard HTTP
framing instead of waiting for TCP EOF. Its 14 fault-period samples included 12
failures and 2 successes, with no retry hiding an outage. Five-second I/O budgets,
sampling, SSH injection latency and cloud convergence affect the observations.
Controller completion uses its wall clock; comparisons include cross-host clock
and observation uncertainty. Existing TCP/WebSocket sessions are not migrated.

Three earlier attempts are explicitly excluded from automatic acceptance. Two
stopped during workstation HTTPS preparation; the same generated path passed
locally and later used framed HTTP/independent GitHub probes. Another exposed
systemd credential permissions: its service never ran, despite offline arming.
That deployment was corrected with private runtime materialization and verified
in the final run. Every prior VM/boot was deleted before the next attempt;
at most four lab VMs existed at once.

Native cloud response-loss injection, throughput, prolonged outages, RKE2
production packaging, controller-host HA, SIP/RTP and production-data migration
were not qualified. Lost native responses remain covered by local real-HTTP SDK
tests and require operator recovery. Continuous public monitoring still needs an
external blackbox probe. The separate CSI native-volume expansion NO-GO remains.

Cleanup was independently read back after the worker finished: no servers,
volumes, load balancers or keypairs; the original NAT and three external address
resources remained, including the same two reserved IPv4s free. No raw addresses,
project/server/volume IDs, credentials, keys or operational logs are published.
