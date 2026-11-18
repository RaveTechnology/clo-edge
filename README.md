# CLO Edge

**Experimental single-host failover controller; production acceptance pending.**

The Go controller watches two dedicated HAProxy VMs, confirms terminal
STOPPED/OFF on the source, and moves one explicitly configured IPv4 through CLO
API. It uses a durable local command journal and a lifetime kernel lock. Lost
native responses require operator recovery; commands are never blindly replayed.
Observe-only is the default. Explicit arming is required; a completed move
disarms the controller. No automatic failback or controller-host HA is provided.

[Installation, health policy and recovery](docs/controller-operations.md).
Build: `make check build`; `make haproxy-check` validates the generated config.
Binary commands: init, arm, run, inspect, render-haproxy, version.

[Native automatic failover acceptance](docs/controller-native-2026-11-17.md):
source STOPPED/OFF, reserve retained boot/service identity, independent public
HTTPS 5/5 before and after. First observed response completed 129.472 s after fault
injection; controller recorded completion 136.248 s. One sample, not an SLA.

[Combined Rancher/RKE2/edge lab](docs/rke2-edge-2026-11-18.md): three private
RKE2 nodes provisioned through Rancher, no false edge fence on node/shared-ingress
loss, and automatic move to an initially private-only standby. First observed
HTTPS response completed 126.939 s after the edge fault.
The node observer had an interruption and request failures; production acceptance
and permanent external alerting remain pending.

[External public monitoring](docs/external-monitoring.md) includes pinned native
Prometheus/blackbox/Alertmanager examples, scoped Telegram delivery, private
credentials and semantic alert tests. Private controller metrics require a separate
reachable transport; a public probe does not prove controller readiness.

[Commissioning sample, 18 November](docs/external-monitoring-2026-11-18.md):
permanent Rancher public probe, controlled local firing/resolution, one authorized
Telegram test and real service-restart acceptance. Production edge/controller
metrics and observer-host HA remain separate work.

Proposed HTTP/HTTPS/WSS entry: two standalone HAProxy VMs in the CLO project,
one movable public IPv4, PROXYv2 to two private Kubernetes ingress instances.
Certificates and HTTP routing remain in the ingress. SIP/RTP stays separate.

A controlled two-VM test17November2026 transferred the same public IPv4 via
CLO API without reboot:42.014/45.554seconds to first HTTP on the new VM; observed
success gaps40.844/40.988seconds. With the old VM explicitly OFF, transfer
took50.618seconds (55.895seconds from the power-off command). This is one run,
not an automatic HA/SLA guarantee. Receiver/source boot IDs and service start
times were unchanged during hot moves; guest network reconfiguration was automatic.

CLO standard L3 does not support VRRP; assignment must go through the API.
[Provider network documentation](https://clo.ru/help/network/local).

The separate [four-VM data-plane experiment](docs/data-plane-2026-11-17.md)
qualified HTTP/TLS, source-address trust on both private ingress instances,
header/PROXY spoof handling, active WS and idle WSS, ingress health exclusion and
an explicit powered-off-edge IP transfer. 57 connections were correlated with
actual edge TCP peers. The measured API move took 37.828 seconds to first HTTPS;
power-off command to first HTTPS was 76.764 seconds. These are samples, not an SLA.
[Sanitized measurements](docs/data-plane-2026-11-17.json).

The controller implementation follows these observations. Total RTO includes
detection, fencing and IP transfer. OFF during STOPPING is not terminal.
The earlier experiment did not test automatic failover; local controller
acceptance and any subsequent native acceptance must be reported separately.

[Cleaned measurements](docs/ip-transfer-2026-11-17.json). Apache-2.0.
