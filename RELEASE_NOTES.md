# Experimental CLO edge controller

One fixed management host, two dedicated HAProxy VMs, one configured external IPv4.
Source STOPPED/OFF fencing, write-ahead durable journal, no native command replay,
explicit arming, private identity probes and pinned public TLS checks.

Observe-only by default. No automatic failback, controller-host HA, infrastructure
provisioning or SIP/RTP support. Existing TCP/WebSocket sessions must reconnect.
Native network data-plane results do not qualify automatic failover RTO or SLA.
Review the acceptance report and operations guide before using this prerelease.
No production deployment is performed by publishing this release.
