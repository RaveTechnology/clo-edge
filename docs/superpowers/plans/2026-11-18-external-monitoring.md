# External CLO monitoring implementation plan

Goal: continuous verified public HTTPS probes outside CLO, certificate/observer alerts and verified delivery to an owner-selected existing Telegram group. Existing control/data-plane code and immutable releases remain unchanged.
Architecture: dedicated native Prometheus3.16.0/blackbox0.29.1/Alertmanager0.35.0 services on an existing Ubuntu24.04 host outside CLO; all listeners loopback, dedicated unprivileged user, bounded memory/CPU/disk, systemd startup. Owner opted out of intermediate design approvals and chose the notification group and one test message.
Spec: this document and existing controller-operations.md; private operator binding stays outside Git.

## Constraints

No new VM, production-cluster rollout, firewall or Rancher change. No cloud keys in monitor. Telegram bot and chat credentials private0600 + LoadCredential; no recipient/token/actual target/nativeID in public files. One synthetic Telegram test message only, send_resolved=false for that test; actual operational alerts retain normal resolved notifications. Failures recorded, no blind replay of ambiguous send. Existing integration assets immutable.

### Task 1: reusable configuration and semantic verification

Files: deploy/monitoring/{blackbox.yml,prometheus.example.yml,targets.example.yml,alertmanager.example.yml,alerts.yml,alerts.test.yml,images.lock.json,rave-clo-*.service}; docs/external-monitoring.md; Makefile monitoring-check.
- [ ] Write actual PromQL tests for60s HTTPS outage threshold/recovery/no false alarm on brief failure,14day warning/3day critical certificate and missing exporter/target. Run against empty valid rules: assertions must fail; implement rules; pass exact promtool.
- [ ] Config: HTTPS+TLS verification/minTLS1.2, no redirect/protocolfallback, body-specific Rancher pong vs future edge ok modules. File targets operator-owned. Alerts scoped with environment=clo/monitor=rave-clo; default sink for other alerts. Dedicated one-shot test route cannot send resolved/repeat within its2minute lifetime.
- [ ] Units: native binaries, loopback19115/19090/19093, independent user; memory caps64/384/128MiB, CPU20/30/20percent, Prometheus7days/512MB retention; AMcluster disabled; credential files read via systemd. Validate official tools and systemd, document install/reload/remove/sourcehash/limits.

### Task 2: commission permanent host and prove delivery

Private binding only: existing management host outside CLO has~3.3GiB available/140GBdisk, portsfree, no prior own units/config.
- [ ] Stage digest-proven native binaries, exact configs/units and credential files overSSH; preflight rejects overwriting foreign files. Validate before enable; capture baseline Rancher/kernel boot/service PID to prove no restart.
- [ ] Start/enable only three own units; confirm active/current public probe_success1 +verifiedTLS/body +certificateexpiry; API ports localhost only; process user/credential permissions/resource caps. Use controlled LOCAL fixture for firing/resolved receiver test without touching target or sending Telegram.
- [ ] Configure owner-selected live receiver, verify Telegram getChat identity without send, inject one durable-intent test event with120sTTL to test-only route. Require notification success metric/request result and no failed deliveries; no second send if uncertain. Restart only own units to prove state/startup persistence without repeating Telegram test.
- [ ] Capture fresh permanent Rancher health and ownmonitor readiness; remove own temporary fixture/receiver, ensure nothing runs on workstation. Publish sanitized commissioning report +configs after one whole-branch review and green checks. Existing controller metrics cannot be scraped until a permanent controller/reachable private transport exists; do not claim they are deployed. Observer-host HA/deadman and productionedge target are future work.
