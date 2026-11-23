package ingress

import (
	"errors"
	"net/netip"
	"reflect"
	"sort"
	"strings"
)

type Identity struct {
	Name         string   `json:"name"`
	NodeUID      string   `json:"node_uid"`
	MachineUID   string   `json:"machine_uid"`
	ServerID     string   `json:"server_id"`
	PrivateIP    string   `json:"private_ip"`
	ProjectID    string   `json:"project_id"`
	ProviderName string   `json:"provider_name"`
	Attempt      string   `json:"attempt"`
	BootIDs      []string `json:"boot_ids"`
}

func (a Identity) Equal(b Identity) bool { return reflect.DeepEqual(a, b) }

type Candidate struct {
	Identity
	NodeRefUID   string `json:"node_ref_uid"`
	Ready        bool   `json:"ready"`
	Schedulable  bool   `json:"schedulable"`
	ControlPlane bool   `json:"control_plane"`
	Workload     string `json:"workload"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Pending      bool   `json:"pending"`
	PrivateKey   []byte `json:"-"`
}
type Server struct {
	ID, ProjectID, Name, Status, SwitchStatus string
	AddressIDs, DiskIDs                       []string
}
type Volume struct {
	ID, ServerID string
	Bootable     bool
}
type Address struct {
	ID, IP, ServerID, Entity, Status string
	External                         bool
	Version                          int
}
type Inventory struct {
	Servers   map[string]Server
	Volumes   map[string]Volume
	Addresses map[string]Address
}
type Snapshot struct {
	ProvisioningClusterUID, DownstreamSystemUID string
	Candidates                                  []Candidate
	Inventory                                   Inventory
}

func Verify(cfg Config, s Snapshot) ([]Candidate, error) {
	if s.ProvisioningClusterUID != cfg.ProvisioningClusterUID || s.DownstreamSystemUID != cfg.DownstreamSystemUID {
		return nil, errors.New("Kubernetes cluster identity changed")
	}
	if len(s.Candidates) > 1024 {
		return nil, errors.New("candidate bound exceeded")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	out := []Candidate{}
	for _, c := range s.Candidates {
		if seen[c.NodeUID] || names[c.Name] {
			return nil, errors.New("duplicate candidate identity")
		}
		seen[c.NodeUID] = true
		names[c.Name] = true
		if !c.Ready || !c.Schedulable || c.Pending || c.ControlPlane || c.Workload != "general" || c.OS != "linux" || c.Arch != "amd64" {
			continue
		}
		if verifyCandidate(cfg, c, s.Inventory) == nil {
			out = append(out, c)
		}
	}
	return out, nil
}
func verifyCandidate(cfg Config, c Candidate, inv Inventory) error {
	reject := errors.New("candidate native binding is not verified")
	for _, id := range []string{c.NodeUID, c.MachineUID, c.ServerID} {
		if !uuidRE.MatchString(id) {
			return reject
		}
	}
	if !nameRE.MatchString(c.Name) || c.NodeUID != c.NodeRefUID || c.ProjectID != cfg.Edge.ProjectID || c.Attempt == "" || strings.ContainsAny(c.Attempt+c.ProviderName, "\r\n\x00") {
		return reject
	}
	prefix, err := netip.ParsePrefix(cfg.PrivateCIDR)
	ip, e := netip.ParseAddr(c.PrivateIP)
	if err != nil || e != nil || !ip.Is4() || !ip.IsPrivate() || !prefix.Contains(ip) {
		return reject
	}
	for _, id := range []string{cfg.ManagementServerID, cfg.Edge.ControllerServerID, cfg.Edge.Edges[0].ID, cfg.Edge.Edges[1].ID} {
		if c.ServerID == id {
			return reject
		}
	}
	server, ok := inv.Servers[c.ServerID]
	if !ok || server.ID != c.ServerID || server.ProjectID != c.ProjectID || server.Name != c.ProviderName || server.Status != "ACTIVE" || server.SwitchStatus != "ON" {
		return reject
	}
	if len(server.AddressIDs) != 1 {
		return reject
	}
	address, ok := inv.Addresses[server.AddressIDs[0]]
	if !ok || address.ID != server.AddressIDs[0] || address.External || address.Entity != "server" || address.ServerID != c.ServerID || address.Status != "ACTIVE" || address.Version != 4 || address.IP != c.PrivateIP {
		return reject
	}
	for _, a := range inv.Addresses {
		if a.Entity == "server" && a.ServerID == c.ServerID && a.ID != address.ID {
			return reject
		}
	}
	if len(c.BootIDs) != 1 || !uuidRE.MatchString(c.BootIDs[0]) {
		return reject
	}
	boot, ok := inv.Volumes[c.BootIDs[0]]
	if !ok || boot.ID != c.BootIDs[0] || !boot.Bootable || boot.ServerID != c.ServerID {
		return reject
	}
	found := false
	for _, id := range server.DiskIDs {
		v, ok := inv.Volumes[id]
		if !ok {
			return reject
		}
		if v.Bootable && id != boot.ID {
			return reject
		}
		if id == boot.ID {
			found = true
		}
	}
	if !found {
		return reject
	}
	return nil
}
func Survivors(previous []Identity, available []Candidate) []Identity {
	out := []Identity{}
	for _, old := range previous {
		for _, c := range available {
			if old.Equal(c.Identity) {
				out = append(out, old)
				break
			}
		}
	}
	return out
}
func Select(available []Candidate, previous []Identity, maximum int) []Candidate {
	out := []Candidate{}
	seen := map[string]bool{}
	add := func(c Candidate) {
		if len(out) < maximum && !seen[c.NodeUID] {
			out = append(out, c)
			seen[c.NodeUID] = true
		}
	}
	for _, old := range previous {
		for _, c := range available {
			if old.Equal(c.Identity) {
				add(c)
			}
		}
	}
	ordered := append([]Candidate{}, available...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, c := range ordered {
		add(c)
	}
	return out
}
