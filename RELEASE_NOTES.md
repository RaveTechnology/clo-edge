# v0.2.0-rc.1 — automatic ingress worker reconciliation

Adds the native clo-ingress companion for Rancher RKE2 private workers: identity-bound firewall preparation, guarded ingress-ready labels, live HAProxy generation verification and current backend discovery for external health checks. Runs on an existing fixed management host; no permanent VM or cloud-write capability is added. Default mode is observe-only.

The binary archive now includes both clo-edge and clo-ingress. The existing scratch image still runs the VIP controller. Existing VIP journals and recovery gates are unchanged. This release does not claim CSI recovery, controller-host HA or uninterrupted service after deleting every eligible ingress worker.

Previous accepted features and limitations remain below.

# Experimental CLO edge controller

One fixed management host, two dedicated HAProxy VMs, one configured external IPv4.
Source STOPPED/OFF fencing, write-ahead durable journal, no native command replay,
explicit arming, private identity probes and pinned public TLS checks.

Observe-only by default. No automatic failback, controller-host HA, infrastructure
provisioning or SIP/RTP support. Existing TCP/WebSocket sessions must reconnect.
Native network data-plane results do not qualify automatic failover RTO or SLA.
Review the acceptance report and operations guide before using this prerelease.
No production deployment is performed by publishing this release.

The native controller lab qualified automatic STOPPED/OFF fencing, IP transfer,
external HTTPS before/after, unchanged reserve boot/service identity and restart
without failback. First observed response completed 129.472 s after fault injection;
one sample, not an SLA. See docs/controller-native-2026-11-17.md for method/limits.

The image is also included as a public Docker image archive. Verify SHA256SUMS,
then use `docker load -i clo-edge-linux-amd64-image.tar.gz`; registry login is not
required to download or load that Release asset.
