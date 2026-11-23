# Native ingress reconciliation acceptance — 2026-11-23

The native manager runs on the existing management VM. Its default selection
contains two private general workers. Worker pool quantities remain under the
operator's control. Prepared standby workers keep their guard and Traefik label.

Observe-only discovery first validated three eligible workers without changing
edge configuration. Automatic mode then commissioned the workers and published a
unique, live-verified HAProxy generation on both edges.

An owned temporary Rancher pool proved two worker incarnations. The first worker
was prepared, labeled and admitted automatically. After its removal, the obsolete
backend was withdrawn before its released private address was assigned to an owned
helper VM. Recreating the pool produced a different VM ID, Node UID and address;
the replacement was prepared and admitted without manual label or backend edits.
Both incarnations passed allowed edge connections, rejected management connections
and persistent guard checks. They carried only system Pods and no PVCs.

Cleanup removed the temporary pool/configuration, both test VMs and their boot
volumes/private addresses, and the address-holding helper. The original nine Node
UIDs and exact 13-server/13-volume/19-address inventory were preserved. The two
original worker agent PIDs were unchanged. The manager returned to its default
two-backend selection; both running edge generations and dynamic monitoring were
verified.

An independent frequent HTTPS probe observed no failure in its recorded window.
The workstation experienced connection timeouts, including an SSH/TLS access-path
problem. Those results are retained in the JSON record; this acceptance does not
claim uninterrupted access from every client or identify the transport fault's
cause. Final public TLS checks use the independent network.

The existing VIP controller's configuration and journal were unchanged. Its prior
RecoveryRequired incident remains unresolved. CSI recommissioning and customer
data/telephony migration are separate outstanding work. This result accepts the
ingress replacement automation. Full application platform acceptance remains open.

Machine-specific evidence, credentials and addresses remain private. Reproducible
commands and authority requirements are in [ingress operations](ingress-operations.md).
The [JSON record](ingress-native-2026-11-23.json) contains bounded, non-secret evidence.
