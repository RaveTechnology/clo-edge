# Controlled native transfers, 24–25 November 2026

The active dedicated HAProxy service was stopped; the operator did not stop the
VM or move the address for the fault. The controller fenced the source, detached
and attached the configured IPv4 and reached normal Completed/disarmed. Independent
synthetic HTTPS observers ran before each fault and continued after recovery.

| Direction | Fault to stable HTTPS | Observed gap between successes |
|---|---:|---:|
| A → B | 123.245 s | 123.592 s |
| B → A | 120.921 s | 121.633 s |

These are two samples, not an RTO guarantee or SLA. The first successful sample
preceded the final review fixes; the reverse sample exercised the corrected
network helper. Two earlier drills failed and required offline operator recovery;
they are excluded from the successful measurements. No application traffic was
routed through this commissioning test.

The public NIC companion was qualified separately in an isolated Linux network
namespace: adding the address with noprefixroute left the entire main routing
table and private default route unchanged. Network apply remains limited to a
fenced transfer. When the peer is running it observes and refuses network changes;
a bounded cache prevents that refusal from creating an API polling storm.

The current binary exposes accepted-attach completion and accepted-detach resume.
Both require fresh terminal scope and fencing checks. A responding source is not
an unreachable source. Phase-only journal exports and unknown acknowledgements
cannot authorize recovery. No controller-host takeover, automatic failback or
automatic re-arming is provided. Repair the standby and explicitly arm after the
cooldown. Existing TCP/WebSocket sessions reconnect after a transfer.

[Sanitized measurements](controller-native-2026-11-25.json).
