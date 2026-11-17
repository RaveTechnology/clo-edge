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
in a separate root-owned mode0600 `/etc/clo-edge/api-token`.
Install `deploy/clo-edge.service`. Its LoadCredential copies the token privately
for the service. Do not put secrets in command arguments, config, logs or images.

Commission with the daemon stopped. Run `init`, then (if automatic=true) `arm`
as the service account with the same configuration/state and a private readable
token file. `systemd-run --wait --collect --pipe --uid=clo-edge
--property=LoadCredential=api-token:/etc/clo-edge/api-token
/usr/local/bin/clo-edge init --token-file %d/api-token` is suitable on a systemd
version supporting credential specifiers; verify credential-path expansion for
your installed systemd. Use the same command with `arm` after initialization.
Then enable/start `clo-edge.service`. Both init and arm require CLO reads; arm
additionally requires public and both private health checks to succeed.

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
