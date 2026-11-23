# Automatic Ingress Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans inline. One independent final review, one Important fix pass.

**Goal:** Make eligible Rancher worker replacement automatically restore protected ingress and current HAProxy targets.
**Architecture:** Native Go manager on the existing management host, read-only CLO discovery, owned SSH preparation, UID-bound labels, verified graceful HAProxy publication and dynamic private monitoring.
**Tech Stack:** Go1.27, existing CLO SDK3.2, kubectl, SSH, Ubuntu24 nft/systemd, HAProxy2.8, Python3 host helpers.
**Spec:** ../specs/2026-11-23-ingress-reconciliation-design.md

## Global Constraints

No permanent VM creation, user Node deletion, cloud write in the manager, RKE2 restart, VIP/config/journal reset, PROXY/header weakening, CSI mutation, credential publication or unbounded retries. Preserve working user ingress while testing owned temporary replacement. Pin all resource and Kubernetes identities. Guard precedes label; healthy Traefik precedes backend publication.

## Review Focus

- IP reused by a different VM/Node: no stale backend adoption or wrong guest write.
- API/archive/SSH ambiguity and startup: preserve accepted service, no new unverified mutation or replay.
- Crash between configuration write/reload/journal save: prove running unique HAProxy generation and resume safely.
- Unready/zero workers and one unreachable edge: withdraw obsolete endpoints, restore when eligible Nodes return, do not start/fence VMs.
- Node replacement and monitoring: no manual label, backend IP or private-probe update; no credentials reach workers/public artifacts.

### Task 1: Core identities, selection, state and dynamic rendering

**Files:** internal/ingress/{config,model,store,reconcile}.go and matching tests; internal/edge/haproxy.go and tests.
**Consumes:** existing immutable VIP Config and published legacy renderer.
**Produces:** validated Candidate/Inventory/Target/State models, bounded stable selection, Runtime interface, Engine.Tick, atomic host-bound Store, RenderHAProxyBackends.

- [ ] RED: wrong project/UID/IP/boot/duplicate/pending identities; retained selection, removal, zero/one/many candidates.
- [ ] RED: prepare failure never labels/publishes; label success waits for ready Traefik; unknown/partial publication resumes from stored generation; discovery error leaves accepted targets intact.
- [ ] Implement minimal core and real-file journal/lock with before-side-effect intents.
- [ ] GREEN: go test -race ./...; legacy config hash/renderer/state-machine behavior preserved.
- [ ] Commit core with tests and ledger.

### Task 2: Real CLO, Kubernetes and SSH adapters

**Files:** internal/provider/inventory.go; internal/ingress/{runtime,kubernetes,ssh}.go; internal/ingress/scripts/*.py; adapter and script contract tests.
**Consumes:** Task1 runtime/model interfaces and target renderer.
**Produces:** complete SDK inventory; bounded archive discovery; identity-pinned guard/label/readiness; verified generation-based edge publication and discovery export.

- [ ] RED: partial/duplicate inventory, bounded archive parsing, foreign/changed NodeRef and guest/edge identity.
- [ ] RED: unsafe shell input, private key/log leakage, guard/reload failure, live-state generation mismatch.
- [ ] Implement adapters and embedded Python programs using JSON stdin and strict paths.
- [ ] GREEN: go test -race ./...; actual HAProxy parser/runtime on zero/one/many targets and graceful generation replacement; no cloud writes emitted.
- [ ] Commit adapters with tests and ledger.

### Task 3: Runnable service, monitoring, packaging and documentation

**Files:** cmd/clo-ingress/*; deploy/ingress/*; Makefile; README.md; RELEASE_NOTES.md; VERSION; docs/ingress-operations.md.
**Consumes:** Task2 runtime and committed discovery manifest.
**Produces:** configurable observe/once/run service, example config/RBAC guidance, dynamic observer helper, monitor tests, native packaged binary.

- [ ] RED: disabled/default mode has no mutations; invalid config/private files rejected; dynamic exports reject malformed/foreign targets and preserve legacy mode.
- [ ] Implement service/metrics/cancellation and observation compatibility; documentation must describe fixed-host authority and worker bootstrap sequence.
- [ ] GREEN: make check haproxy-check package; monitoring tests, CLI version/check/disabled smoke; public tracked-source privacy scan.
- [ ] Commit runnable component with tests and ledger.

### Task 4: Observe, deploy and prove real replacement

**Files:** private operator ROOT and sanitized docs/ingress-native-2026-11-23.{md,json}.
**Consumes:** Task3 exact built artifact; current two user worker/edge identities; pinned management and downstream credentials.
**Produces:** permanent service enabled on existing management VM, automatic owned worker replacement acceptance, all owned test resources cleaned, current operational handoff.

- [ ] Fresh private baseline/quotas/balance/source hashes and protected user IDs. Install observe-only, verify no changes, then explicitly enable existing two worker adoption.
- [ ] Verify public TLS/private guards/PROXY policy/RKE2 PID and VIP journal unchanged; dynamic monitoring reads actual targets.
- [ ] Temporarily max3; one owned test pool quantity1. Verify preparation/label/Pod/backend without operator intervention.
- [ ] Remove only test worker, occupy its released address with owned helper, recreate and prove new UID/IP publication and uninterrupted existing ingress. Bound waits; no replay of unknown create.
- [ ] Cleanup owned test resources, default max2, fresh external proofs/backup/protection and state; no user workload or CSI operations.
- [ ] Commit sanitized evidence; one independent final review; Important fixes RED→GREEN in one pass, full checks; no second review.
- [ ] Publish under existing public distribution authorization, install exact accepted artifact, update knowledge and final operational pointers; preserve private credentials/evidence.
