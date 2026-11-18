# External public monitoring

The examples in `deploy/monitoring` run an independent public HTTPS observer on
an Ubuntu 24.04/systemd host outside CLO. They do not change the failover controller
or require a CLO API key. Keep this host outside the workload cluster as well.
One observer host is still a single point of failure; independent deadman/observer
HA is not included.

The initial target is Rancher `/ping`. Add the production edge `/healthz` with
module `edge_health` only when that endpoint actually exists. Controller phase,
armed state and private metrics need a separately commissioned private transport;
these public probes do not scrape them or prove the controller is armed.

## Files and versions

`images.lock.json` records official immutable image sources and SHA-256 of the
native linux/amd64 binaries extracted from them: Prometheus 3.16.0, Alertmanager
0.35.0 and blackbox exporter 0.29.1. Extract `/bin/prometheus` and `/bin/promtool`
from the Prometheus image, `/bin/alertmanager` and `/bin/amtool` from Alertmanager,
and `/bin/blackbox_exporter` from blackbox. Use a uniquely named temporary
`docker create`, `docker cp`, and remove only that stopped container afterward.
Verify each binary against the lock file before transfer to the observer.
Docker is not required on the permanent observer.

Install the five binaries, owned by root, under `/opt/rave-clo-monitor/bin`.
The parent directory must be traversable by the `rave-clo-monitor` service group;
use root:group mode 0750 and binaries mode 0755. Create a dedicated system account
with no home and a nologin shell. Do not reuse an existing unrelated account,
configuration directory or units with these names.

Create `/etc/rave-clo-monitor` as root:`rave-clo-monitor`, mode 0750. Install:

- `blackbox.yml`, `alerts.yml` and `telegram.tmpl` with their existing names;
- `prometheus.example.yml` as `prometheus.yml`, changing file discovery to
  `/etc/rave-clo-monitor/targets.yml`;
- `targets.example.yml` as `targets.yml`, replacing the `.invalid` target;
- `alertmanager.example.yml` as `alertmanager.yml`.

Configuration is root:service-group, mode 0640. Store the bot token in
`telegram.bot` and the numeric chat ID in `telegram.chat`, both root-owned 0600
regular files in that directory. Do not put their values in Git, unit arguments,
environment variables or logs. Alertmanager receives private systemd credentials
through `LoadCredential`; it reads `bot_token_file` and `chat_id_file` from the
service credential directory. Verify the selected chat using Telegram `getChat`
before sending anything.

Install the three `.service` files under `/etc/systemd/system`. All APIs bind
only to loopback: blackbox 19115, Prometheus 19090, Alertmanager 19093. The
Alertmanager cluster listener is disabled. Use SSH forwarding for administration;
do not expose these unauthenticated APIs publicly. No existing Rancher, firewall
or workload unit needs a change or restart.

Resource limits are 64 MiB/20% CPU for blackbox, 384 MiB/30% for Prometheus and
128 MiB/20% for Alertmanager. Prometheus keeps seven days, capped at 512 MB of
retained blocks; WAL/head overhead is additional. State directories are private
and survive service restarts. Check available memory and disk before installation.

## Verify and start

`make monitoring-check` validates examples and semantic alert tests with the exact
locked official images and no container network access. On the observer also run
`promtool check config`, `amtool check-config`, blackbox `--config.check`, and
`systemd-analyze verify` against the installed paths before enabling units.

Start `rave-clo-blackbox.service` first. Its `/probe` API must return
`probe_success 1` for the configured HTTPS target, with certificate and hostname
verification enabled and the expected body (`pong` or `ok`, optional newline).
Only then enable/start `rave-clo-alertmanager.service` and
`rave-clo-prometheus.service`. Enable all three for boot. No host reboot is needed
for commissioning; verify enablement and restart only these units for persistence.

Inspect `probe_success`, `probe_http_ssl`, `probe_ssl_earliest_cert_expiry`, targets,
rules and Alertmanager readiness. Check effective process user, credential-file
permissions, resource limits and loopback listeners. Confirm existing management
services retained their boot/PID identity and remain healthy.

Alerts cover:

- HTTPS failure lasting one minute, including wrong body, status or TLS failure;
- certificate expiry below fourteen days (warning after ten minutes) or three
  days (critical after one minute);
- missing/unreachable public prober targets for one minute.

Brief failures remain visible in metrics but do not immediately notify. The
observer uses IPv4, verifies TLS 1.2 or newer, and follows no redirects. A healthy
`/ping` is endpoint availability, not full Rancher, Kubernetes or application health.

## Delivery and acceptance

Only alerts carrying `environment=clo` and `monitor=rave-clo` reach the normal
Telegram receiver; unmatched alerts are discarded. Normal notifications include
resolved events. The message shows severity and status for each event, so a
resolved warning and a firing critical alert remain distinguishable in one group. Alerts carrying the reserved `acceptance_run` label are discarded
by the permanent configuration. A temporary loopback-only receiver may override
that route during controlled fixture acceptance; restore the discard guard before
removing the fixture or leaving commissioning.

Semantic tests check the failure delay, recovery, brief-failure suppression,
certificate thresholds and missing-target behavior. A live synthetic fixture can
prove firing/resolved through the observer without stopping a real public target.
Keep those notifications local and clean up its temporary target/module/service.

Obtain explicit authorization for the human recipient and test notification.
The separate `CLOMonitoringDeliveryTest` route has zero group wait, a one-hour
repeat interval and `send_resolved: false`. Submit one event with a fresh recorded
nonce and a two-minute expiry, without `acceptance_run`. Preserve intent before
submission and do not manually resubmit an ambiguous send. Require a successful
Telegram notification counter and zero new failed deliveries; record the recipient
identity separately. Alertmanager delivery is not an exactly-once protocol.

Keep runtime targets, chat metadata, credentials and raw operational proofs
private. Publish only sanitized commissioning results. If notifications fail,
inspect Alertmanager logs and failure metrics locally; its own outage cannot be
reported through the same failed sender.

## Operations

For target changes, atomically replace `targets.yml`; file discovery refreshes
within thirty seconds. Validate rule/config changes first, then send SIGHUP only
to the relevant monitor unit. `systemctl restart` affects only the named monitor.
Journal logs and private Prometheus/Alertmanager state are the diagnostic sources.

For removal, stop/disable only these three units, archive any desired history,
then remove their exact owned config/binary/state directories and dedicated user.
Revoke the bot token only if this deployment is its sole user. A shared existing
alert bot must remain usable by its other deployments. No production workload or
Rancher restart is part of installation or removal.

References: [Alertmanager configuration](https://prometheus.io/docs/alerting/latest/configuration/),
[blackbox configuration](https://github.com/prometheus/blackbox_exporter/blob/master/CONFIGURATION.md).
