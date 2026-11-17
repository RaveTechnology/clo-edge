# CLO Edge

**Design and lab evidence; failover controller is not implemented yet.**

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

Next: design and implement the controller with independent failure detection,
single-writer decisions and ambiguous API result reconciliation. Total RTO
includes detection, chosen fencing and IP transfer. Wait for terminal
STOPPED/OFF when a shutdown is required: OFF can appear during STOPPING.
No controller image is published before that code exists.

[Cleaned measurements](docs/ip-transfer-2026-11-17.json). Apache-2.0.
