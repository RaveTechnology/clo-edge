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
