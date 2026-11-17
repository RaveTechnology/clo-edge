# CLO edge controller implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** A conservative, runnable single-host edge failover controller with durable
no-repeat recovery, explicit arming and deployable packages.

**Architecture:** One Go process outside served Kubernetes. A pure serialized
reconciler consumes scoped CLO state and bounded probes, persists intent before
each native command and fences the source before moving the configured IPv4.

**Tech Stack:** Go 1.27, official CLO SDK v3.2.0, Linux flock/fsync, systemd,
HAProxy 2.8, Docker, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-17-edge-controller-design.md`.

## Global constraints

- Linux amd64, Go 1.27, public branding rave-technology; no private source names,
  Git hosts, tokens or actual resource addresses/IDs in public files/history.
- One fixed independent host; no controller HA/automatic lease takeover.
- Source STOPPED AND OFF before IP mutation; one project/two dedicated edge IDs/one IPv4.
- Default observe-only; explicit offline arming; complete failover disarms; no failback.
- Poll5s/probe2s/suspect30s/convergence180s/cooldown600s defaults; no ignored TLS errors.
- Write-ahead durable intent, no POST replay; uncertainty blocks subsequent writes.
- Use GOWORK=off; never modify other product repositories or production.

## Review focus

- A queued native mutation may complete after its HTTP response is lost: never
  treat a matching later GET as permission to issue the next command.
- A frozen old process can resume after a timeout: kernel lifetime lock, not lease.
- A shared ingress outage must not power off a healthy edge.
- A cloned or corrupted journal must not be silently adopted.
- A controller restart during attach must not detach the newly serving edge.

### Task 1: configuration and durable state

**Files:** `go.mod`, `internal/edge/config{,_test}.go`,
`internal/edge/store{,_test}.go`, `.gitignore`.
**Interfaces:** Config validates strict JSON and hashes normalized configuration;
OpenStore(dir,machineID), Store.Load/Save/Close; State records phase, arming,
bindings, timestamps and optional Pending{kind,accepted,dispatchedAt}.

- [x] RED: invalid topology/unknown config, missing/corrupt/rebound journal,
  concurrent lock, atomic persistence and second-process lock tests.
- [x] Run `GOWORK=off go test ./internal/edge`, expect missing implementation FAIL.
- [x] Implement config validation, versioned state, lifetime flock, atomic fsync writes.
- [x] Run tests PASS, `git diff --check`; commit scoped files.

### Task 2: provider and health adapters

**Files:** `internal/provider/{client,client_test}.go`,
`internal/edge/{probe,probe_test}.go`.
**Interfaces:** Provider Snapshot(ctx,Config) returns exact project/VM/address
view; Stop/Detach/Attach return accepted-only-on-202. Probe returns one complete
timestamped Observation with public/active/standby readiness.

- [x] RED: complete inventory, alien entity/owner, bandwidth preservation,
  stop/detach/attach bodies, one POST on 500/connection loss, redaction/redirect;
  private identity, TLS hostname/CA, pinned VIP and no redirect tests.
- [x] Run scoped tests FAIL, implement adapters with SDK retry disabled.
- [x] Run full `GOWORK=off go test ./...` PASS and commit.

### Task 3: serialized failover reconciler

**Files:** `internal/edge/{reconcile,reconcile_test}.go`.
**Interfaces:** Engine.Tick(ctx,now,Observation) loads/saves through Store and
calls Provider, enforcing durable pending commands. Arm checks current ownership
and healthy topology. All operations happen in the same exclusive store session.

- [x] RED: healthy/single-path/shared/standby failures; consecutive30s gate;
  STOPPING/OFF cannot detach; stop→detach→attach→verify only exact IDs; completion
  disarms; crash before/after native acknowledgement; missing write barrier;
  uncertain completion never advances; deadline and restart preserve locks.
- [x] Run scoped tests FAIL; implement smallest state machine satisfying spec.
- [x] Run race/full suite PASS and commit.

### Task 4: runnable commands, HAProxy and deployment

**Files:** `cmd/clo-edge/{main,main_test}.go`, `internal/edge/haproxy{,_test}.go`,
`deploy/*`, `Dockerfile`, `Makefile`, `README.md`, `.github/workflows/build.yml`.
**Interfaces:** init/arm/run/inspect/render-haproxy/version CLI; file-only tokens,
signal cancellation, loopback status; HAProxy private health identity and pools.

- [x] RED: init refuses existing state, run refuses missing state, arming default
  observe-only, cancellation, rendered topology behavior and CLI integration.
- [x] Run tests FAIL, implement commands and render, service/container examples.
- [x] Build and run binary; validate HAProxy config in real parser/container;
  race/vet and image non-root smoke PASS; commit.

### Task 5: final acceptance and release preparation

**Files:** `docs/controller-acceptance-2026-11-17.md`, release/build workflows and
operator recovery documentation as needed; private knowledge handoff.

- [x] Exercise real CLI+HTTP fake-cloud/probes with exact native call ledger,
  including restart and ambiguous mutation; test production failure modes.
- [x] Run full suite, race/vet, build/container/parser/privacy checks.
- [x] Commit; request one fresh whole-branch review with spec/plan/ledger.
- [x] Resolve Important/Critical findings with RED→GREEN and full suite.
- [x] If native lab is practical, use new scoped ownership journal and verify
  cleanup independently. Report live acceptance separately from local acceptance.
- [ ] Integrate/publish within existing authorization only after all gates pass;
  read back exact remote SHA/CI/artifacts and record production limitations.
