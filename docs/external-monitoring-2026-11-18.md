# External monitoring commissioning, 18 November 2026

A permanent verified HTTPS monitor for the Rancher management endpoint is running
on an existing Ubuntu management host outside CLO. No VM was provisioned.
The existing K3s process and host boot identity were unchanged.
[Sanitized proof](external-monitoring-2026-11-18.json);
[installation and operations](external-monitoring.md).

Three native systemd services use Prometheus 3.16.0, blackbox 0.29.1 and
Alertmanager 0.35.0 from the locked official image binaries. All API listeners are
loopback-only, Alertmanager clustering is disabled, and a dedicated unprivileged
account runs the services with CPU, memory and storage limits. Sources for the
Telegram bot/chat credentials are root-owned 0600 files passed with LoadCredential.
Effective access was verified: the designated service user can read the runtime
credentials and an unrelated system user cannot. A private off-host configuration
backup was saved. No CLO API credential is needed.

Public probes run every 15s, require valid TLS/hostname, status 200 and the expected
pong body, and follow no redirects. Rules hold HTTPS failures for 60s, warn on
certificate expiry below 14 days, become critical below 3 days, and detect absent or
unreachable probes. Seven semantic rule scenarios passed. Empty-rule and broken
exporter-detection negative controls failed as expected. A route negative control
proved the permanent acceptance-label discard guard prevents synthetic alerts
from reaching the human receiver.

A TLS-verified loopback fixture was made unavailable and restored. Real
Prometheus→Alertmanager firing/resolved notifications arrived only at an owned
loopback webhook; the actual Rancher target stayed healthy. The fixture target,
module and temporary service were removed and original configs restored. The
permanent discard guard remains.

The owner explicitly selected an existing Telegram group and authorized one test.
The bot verified its identity with getChat. One alert was submitted to the
separate test route; exactly one completed Telegram attempt and zero additional
failures were observed. Test resolved notifications were disabled and the event
had a two-minute expiry. No manual resubmission or human read receipt is claimed.
Normal operational alerts include resolved messages.

All three processes were restarted. They remained enabled on boot and produced a
fresh successful TLS/body probe afterward. The host itself was not rebooted. Raw
targets, recipient metadata, credentials and operational logs remain private.
The reusable templates and semantic checks are public; no existing release tag or
asset was replaced.

This currently monitors one Rancher /ping endpoint. It does not prove full cluster
or application health, continuous uptime, or production migration readiness.
Permanent production edge targets and private controller phase/armed scraping will
be added when those resources and their private transport are commissioned.
Observer-host HA and an independent deadman are not implemented. The separate CSI
native fresh-volume expansion ERROR remains a production blocker.
