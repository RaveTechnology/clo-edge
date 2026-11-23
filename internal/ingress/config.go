package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"

	"github.com/RaveTechnology/clo-edge/internal/edge"
)

type Config struct {
	Edge                   edge.Config       `json:"edge"`
	ManagementServerID     string            `json:"management_server_id"`
	ProvisioningClusterUID string            `json:"provisioning_cluster_uid"`
	DownstreamSystemUID    string            `json:"downstream_system_uid"`
	ClusterName            string            `json:"cluster_name"`
	Namespace              string            `json:"namespace"`
	PrivateCIDR            string            `json:"private_cidr"`
	ObserverIP             string            `json:"observer_ip"`
	MaxBackends            int               `json:"max_backends"`
	PollSeconds            int               `json:"poll_seconds"`
	Automatic              bool              `json:"automatic"`
	ManagementKubeconfig   string            `json:"management_kubeconfig,omitempty"`
	DownstreamKubeconfig   string            `json:"downstream_kubeconfig,omitempty"`
	TokenFile              string            `json:"token_file,omitempty"`
	EdgeSSHKey             string            `json:"edge_ssh_key,omitempty"`
	KnownHostsFile         string            `json:"known_hosts_file,omitempty"`
	StateDir               string            `json:"state_dir,omitempty"`
	Kubectl                string            `json:"kubectl,omitempty"`
	SSH                    string            `json:"ssh,omitempty"`
	AdoptionSHA256         map[string]string `json:"adoption_sha256,omitempty"`
	TraefikUID             string            `json:"traefik_uid,omitempty"`
	TraefikNamespace       string            `json:"traefik_namespace,omitempty"`
	ExpectedMachineID      string            `json:"expected_machine_id,omitempty"`
	AdoptNodeUIDs          []string          `json:"adopt_node_uids,omitempty"`
	EdgeIdentityLabels     map[string]string `json:"edge_identity_labels,omitempty"`
	PlatformIdentityFile   string            `json:"platform_identity_file,omitempty"`
}

var uuidRE = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func (c *Config) Validate() error {
	if err := c.Edge.Validate(); err != nil {
		return err
	}
	for _, id := range []string{c.ManagementServerID, c.ProvisioningClusterUID, c.DownstreamSystemUID} {
		if !uuidRE.MatchString(id) {
			return errors.New("manager and Kubernetes UUID identities required")
		}
	}
	if c.ManagementServerID == c.Edge.ControllerServerID || c.ManagementServerID == c.Edge.Edges[0].ID || c.ManagementServerID == c.Edge.Edges[1].ID {
		return errors.New("manager must be independent of edges and VIP controller")
	}
	if !nameRE.MatchString(c.ClusterName) || !nameRE.MatchString(c.Namespace) {
		return errors.New("invalid Kubernetes scope names")
	}
	prefix, err := netip.ParsePrefix(c.PrivateCIDR)
	ip, e := netip.ParseAddr(c.ObserverIP)
	if err != nil || e != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || !ip.Is4() || !prefix.Contains(ip) {
		return errors.New("private project CIDR and health observer required")
	}
	for _, edge := range c.Edge.Edges {
		ip, err := netip.ParseAddr(edge.PrivateIP)
		if err != nil || !prefix.Contains(ip) {
			return errors.New("edge outside private network")
		}
	}
	if c.MaxBackends == 0 {
		c.MaxBackends = 2
	}
	if c.PollSeconds == 0 {
		c.PollSeconds = 30
	}
	if c.MaxBackends < 1 || c.MaxBackends > 8 || c.PollSeconds < 10 || c.PollSeconds > 300 {
		return errors.New("unsafe backend bound or polling interval")
	}
	return nil
}
func (c Config) ScopeHash() string {
	raw, _ := json.Marshal([]string{c.Edge.Hash(), c.ManagementServerID, c.ProvisioningClusterUID, c.DownstreamSystemUID, c.ClusterName, c.Namespace, c.PrivateCIDR, c.ObserverIP, c.TraefikUID, c.TraefikNamespace, c.ExpectedMachineID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
