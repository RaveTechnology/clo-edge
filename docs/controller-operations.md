# Controller operations

Experimental v0.1.0-rc.1, Linux amd64. One fixed independent management VM, one
process, one private local state directory. It must not run on an edge or in the
served Kubernetes cluster. Do not clone the installation, use a shared filesystem
or launch a second instance with a different state directory. Controller-host HA
is not provided. An unavailable controller leaves the current data path working.

## Prepare

Create two dedicated HAProxy VMs and an independent controller VM in one project.
Do not put workloads on these edge VMs: failover powers off the entire old VM.
Reserve one public IPv4; configure exactly its ID/address/current bandwidth.
Keep management connectivity independent of this movable address. This program
does not create, delete or start cloud resources.

Configure both private ingress instances as in the
[data-plane report](data-plane-2026-11-17.md): data8000/8443, health9000 `/ping`,
PROXYv2 trusted only from the two private edge /32s; strip forwarding/header aliases,
firewall ingress to those edges. TLS certificate must match `public_host`. The
public `health_path` must return200 through ingress; use a non-mutating dedicated
route. HAProxy health8405 checks both backend pools and returns its edge UUID.
Firewall8405 to the controller /32; it contains no API credentials. Never add
accept-proxy to the public HAProxy frontends.

Edit `deploy/config.example.json`; its addresses and UUIDs are placeholders.
Optional `ca_file` names a PEM CA bundle for private TLS; default uses public trust.
Set `automatic` to its intended commissioned value **before init**. `true` still
starts disarmed; offline `arm` is also required. Changing normalized configuration
after init closes the write gate. A changed file is not automatically adopted.

Render and validate separate HAProxy configs with `clo-edge render-haproxy
--config /etc/clo-edge/config.json --edge-id <configured-uuid>` and `haproxy -c`.
Install them on the respective edges; preserve independent SSH/firewall access.

## Fixed-host systemd installation

Install the binary to `/usr/local/bin/clo-edge`, create a system account/group
`clo-edge` and state directory `/var/lib/clo-edge` owned by that account mode0700.
Configuration is root-owned, readable by the service user; the API token lives
in a separate root-owned mode 0600 `/etc/clo-edge/api-token`.
Install `deploy/clo-edge.service` and `deploy/clo-edge-admin@.service`. Their
LoadCredential inputs are materialized as mode 0600 files in private volatile
RuntimeDirectory paths. This accommodates systemd credential file permissions
without weakening the program's private-file check. Secrets never enter the state
journal/directory. Do not put secrets in arguments, config, logs or images.

Commission with the daemon stopped: `systemctl daemon-reload`, then
`systemctl start clo-edge-admin@init.service`, and (if automatic=true)
`systemctl start clo-edge-admin@arm.service`. Each command completes before the
next. Then enable/start `clo-edge.service`. Both administration commands require
CLO reads; arm additionally requires public and both private health checks.
Check `systemctl is-active clo-edge.service` and that inspected
`last_observation` advances; an armed journal alone does not prove a running daemon.

Default polling5s/probes2s/suspicion30s. A move requires continuous public AND
active-private failure, healthy standby and validated CLO state. A public-only
failure is an alert, not an automatic move in this conservative version. Shared
ingress failure does not cause fencing. Detection plus shutdown plus native IP
convergence defines actual RTO; the earlier manual measurements are not its SLA.

`clo-edge inspect --state-dir /var/lib/clo-edge` reads the atomic journal while the
daemon runs. `/metrics` and `/readyz` bind only to127.0.0.1:9128 by default. Use
your existing monitoring to alert on `RecoveryRequired`, armed=0, missing process,
cloud-state-unavailable or failed public health; this release sends no messages.

Successful failover leaves old VM stopped and disarms the controller. Existing
TCP/WS sessions must reconnect. Repair/start the old edge explicitly in CLO,
verify both edge health paths, wait the10minute cooldown, stop daemon, run `arm`,
restart daemon. Re-arming does not move the IP or automatically fail back.

## RecoveryRequired

Do not delete/replace state to make the warning disappear. Do not repeat POSTs
from a shell. Save journal and current provider state; ensure no older controller
process can resume; settle all pending native jobs with CLO. A matching GET after
a lost POST response is not evidence that no delayed mutation can still occur.
Accepted commands can be polled after a process restart; dispatched commands
without durable acceptance never replay or issue the next mutation.

### Finish a durably accepted attach

`complete-accepted-attach` is an offline, read-only-cloud recovery command for one
narrow case: the journal still contains an acknowledged attach, the exact target
now owns the ACTIVE/FIXED address, the original source is terminal STOPPED/OFF,
and the target/controller are running. Fresh public TLS and target health must
pass; the old source health endpoint must be unreachable as well. Host/config/workflow bindings and the exclusive local state lock still
apply. It refuses missing acceptance, stop/detach recovery, wrong ownership,
stale probes and an unfenced source. A matching GET after a lost POST response
remains insufficient; this command cannot resolve an unacknowledged dispatch.

Preserve the original journal/config and settle operator interference first.
Stop `clo-edge.service`, run `clo-edge complete-accepted-attach --dry-run` with
the usual private config/token paths, and retain its before/after receipt. The
dry run makes no journal or native changes. Run the same command without
`--dry-run` to persist only Completed/disarmed; retain that receipt, then restart
the daemon. It never repeats an attach or issues a stop/detach. The source must
already have been fenced explicitly, and a later arm still requires both edges
healthy and the full cooldown. Do not initialize or edit the journal manually.

### Verification timeout without an accepted pending request

This recovery is intentionally unsupported. Old phase-only journal exports cannot
prove a specific workflow or its convergence deadline. A private JSON file is not
accepted as authorization. Preserve the state and native outcome; do not reset,
initialize or force-arm. A future operator requires trusted workflow-bound history.

If the fixed controller host is lost, fence it and its credentials before moving
state anywhere. Resolve pending provider jobs first. Host/config binding rejects
copied or rebound journals. No force-reset/lease-timeout takeover is provided.
Commissioning a replacement after operator repair is an explicit maintenance task.

## Container

The image runs as65532:65532. Mount a private local state directory owned by that
UID, read-only config and token files, the fixed host's `/etc/machine-id` read-only,
and any private CA. Use host networking for loopback metrics/private connectivity.
Use one fixed host/container and keep the same state mount for init/arm/run.
Do not deploy replicas, rolling updates, automatic relocation or ephemeral state.
Default image entrypoint is `/clo-edge`; pass the command and paths explicitly.
The scratch image contains no shell. The native systemd binary is the preferred
first deployment; verify resource scope in a disposable project before production.

Releases also include `clo-edge-linux-amd64-image.tar.gz` for registry-independent
installation. Download it and SHA256SUMS, verify the checksum, then `docker load -i`
the archive. Loading the archive does not commission or start a controller.

## Optional independent lab probe

`Manual public HTTPS probe` is an opt-in GitHub Actions workflow for the synthetic
lab fixture. Its ephemeral Actions secret `CLO_EDGE_PUBLIC_PROBE` is JSON with
`ip` (global IPv4), `hostname` and `ca_pem` (the public lab CA certificate). It must
never contain an API token or private key. The workflow pins the address, validates
TLS and samples `/healthz` five times, expecting the fixture body `ok` plus newline.
Its artifact contains only timestamps/results/error classes, without host/IP/CA.
Remove the temporary secret after the job. This manual preflight does not replace
continuous external blackbox monitoring of a deployed service.
# Terminal accepted detach recovery

`resume-accepted-detach --dry-run` evaluates a narrow offline transition from a
disarmed RecoveryRequired journal with a durably acknowledged pending detach.
It requires the unchanged host/config/workflow binding, terminal STOPPED/OFF
source, unreachable source health, healthy ACTIVE/ON target and controller, and
the exact configured FREE/DOWN unattached IPv4 with its configured bandwidth.
An unknown acknowledgement, other pending kind or unexpected owner is rejected.

After archiving the original journal and stopping the daemon, run the dry command
using the same private credential, service identity and writable state directory
as other offline commands below. Confirm byte-identical journal and the proposed
Attaching state, then run without `--dry-run`. The command submits no cloud writes;
it clears only the proven completed detach and resumes this specific transfer.
Restart the daemon: its normal durable dispatch owns the next attach intent. This
does not arm Watching, reset the journal or accept an earlier HA drill as passing.
Never run init or manually edit the state to resume recovery.
