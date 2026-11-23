package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/RaveTechnology/clo-edge/internal/edge"
	"github.com/RaveTechnology/clo-edge/internal/provider"
)

type InventoryReader interface {
	Inventory(context.Context, string) (provider.ProjectInventory, error)
}
type LiveRuntime struct {
	Config      Config
	Kube        Kubernetes
	Cloud       InventoryReader
	Runner      Runner
	inventory   Inventory
	candidates  map[string]Candidate
	edgeRunning map[string]bool
}

func NewRuntime(cfg Config, cloud InventoryReader, runner Runner) (*LiveRuntime, error) {
	if err := cfg.ValidateRuntime(); err != nil {
		return nil, err
	}
	if cloud == nil || runner == nil {
		return nil, errors.New("explicit runtime adapters required")
	}
	return &LiveRuntime{Config: cfg, Kube: Kubernetes{Config: cfg, Runner: runner}, Cloud: cloud, Runner: runner}, nil
}
func normalizeInventory(source provider.ProjectInventory) Inventory {
	out := Inventory{Servers: map[string]Server{}, Volumes: map[string]Volume{}, Addresses: map[string]Address{}}
	for _, v := range source.Servers {
		s := Server{ID: v.Id, ProjectID: v.Project, Name: v.Name, Status: v.Status, SwitchStatus: v.SwitchStatus}
		if v.Addresses != nil {
			s.AddressIDs = *v.Addresses
		}
		if v.DiskData != nil {
			for _, disk := range *v.DiskData {
				s.DiskIDs = append(s.DiskIDs, disk.Id)
			}
		}
		out.Servers[s.ID] = s
	}
	for _, v := range source.Volumes {
		volume := Volume{ID: v.Id, Bootable: v.Bootable}
		if v.AttachedToServer != nil {
			volume.ServerID = v.AttachedToServer.Id
		}
		out.Volumes[v.Id] = volume
	}
	for _, v := range source.Addresses {
		a := Address{ID: v.Id, Status: v.Status, External: v.External}
		if v.Address != nil {
			a.IP = *v.Address
		}
		if v.Version != nil {
			a.Version = *v.Version
		}
		if v.AttachedTo != nil {
			a.ServerID = v.AttachedTo.Id
			a.Entity = v.AttachedTo.Entity
		}
		out.Addresses[v.Id] = a
	}
	return out
}
func (r *LiveRuntime) validateHosts(source provider.ProjectInventory) error {
	own, ok := r.inventory.Servers[r.Config.ManagementServerID]
	if !ok || own.ProjectID != r.Config.Edge.ProjectID || own.Status != "ACTIVE" || own.SwitchStatus != "ON" {
		return errors.New("fixed manager VM is not active in configured project")
	}
	hosts := append([]edge.Edge{}, r.Config.Edge.Edges[:]...)
	hosts = append(hosts, edge.Edge{ID: r.Config.Edge.ControllerServerID, PrivateIP: r.Config.ObserverIP})
	r.edgeRunning = map[string]bool{}
	privateIPs := map[string]string{}
	for _, a := range r.inventory.Addresses {
		if !a.External && a.Entity == "server" {
			if id, exists := privateIPs[a.IP]; exists && id != a.ServerID {
				return errors.New("ambiguous private address ownership")
			}
			privateIPs[a.IP] = a.ServerID
		}
	}
	for _, host := range hosts {
		server, exists := r.inventory.Servers[host.ID]
		if !exists || server.ProjectID != r.Config.Edge.ProjectID || privateIPs[host.PrivateIP] != host.ID {
			return errors.New("fixed host binding differs from configured project/private IP")
		}
		running := server.Status == "ACTIVE" && server.SwitchStatus == "ON"
		stopped := server.Status == "STOPPED" && server.SwitchStatus == "OFF"
		if !running && !stopped {
			return errors.New("fixed host has a pending native transition")
		}
		r.edgeRunning[host.ID] = running
	}
	found := false
	for _, a := range source.Addresses {
		if a.Id != r.Config.Edge.AddressID {
			continue
		}
		found = true
		if a.Address == nil || *a.Address != r.Config.Edge.PublicIP || !a.External || a.Version == nil || *a.Version != 4 || a.BandwidthMaxMbps == nil || *a.BandwidthMaxMbps != r.Config.Edge.BandwidthMbps {
			return errors.New("configured public IPv4 identity changed")
		}
		if a.Type == "FREE" && a.Status == "DOWN" && a.AttachedTo == nil {
			continue
		}
		if a.Type != "FIXED" || a.AttachedTo == nil || a.AttachedTo.Entity != "server" || (a.AttachedTo.Id != r.Config.Edge.Edges[0].ID && a.AttachedTo.Id != r.Config.Edge.Edges[1].ID) {
			return errors.New("configured public IPv4 has unknown ownership")
		}
	}
	if !found {
		return errors.New("configured public IPv4 absent from complete inventory")
	}
	return nil
}
func (r *LiveRuntime) Discover(ctx context.Context) (Snapshot, error) {
	snapshot, err := r.Kube.Discover(ctx)
	if err != nil {
		return snapshot, err
	}
	cloud, err := r.Cloud.Inventory(ctx, r.Config.Edge.ProjectID)
	if err != nil {
		return snapshot, err
	}
	r.inventory = normalizeInventory(cloud)
	snapshot.Inventory = r.inventory
	if err = r.validateHosts(cloud); err != nil {
		return snapshot, err
	}
	verified, err := Verify(r.Config, snapshot)
	if err != nil {
		return snapshot, err
	}
	r.candidates = map[string]Candidate{}
	for _, c := range verified {
		r.candidates[c.NodeUID] = c
	}
	return snapshot, nil
}
func (r *LiveRuntime) Prepare(ctx context.Context, c Candidate) error {
	trusted, ok := r.candidates[c.NodeUID]
	if !ok || !trusted.Identity.Equal(c.Identity) {
		return errors.New("candidate no longer matches discovered identity")
	}
	if _, err := r.Kube.node(ctx, c); err != nil {
		return err
	}
	directory := filepath.Join(r.Config.StateDir, "keys")
	if err := privateDirectory(directory); err != nil {
		return err
	}
	key := filepath.Join(directory, c.ServerID+".key")
	if err := writePrivate(key, c.PrivateKey); err != nil {
		return err
	}
	request := map[string]any{"node": c.Identity, "manager_id": r.Config.ManagementServerID, "scope_hash": r.Config.ScopeHash(), "edge_ips": []string{r.Config.Edge.Edges[0].PrivateIP, r.Config.Edge.Edges[1].PrivateIP}, "observer_ip": r.Config.ObserverIP, "adopt_existing": slices.Contains(r.Config.AdoptNodeUIDs, c.NodeUID)}
	var result struct {
		OK         bool   `json:"ok"`
		NodeUID    string `json:"node_uid"`
		ServerID   string `json:"server_id"`
		Persistent bool   `json:"persistent_guard"`
	}
	if err := r.remote(ctx, c.ServerID, c.PrivateIP, key, true, prepareNodeScript, request, &result); err != nil {
		return fmt.Errorf("preparing verified ingress worker failed: %w", err)
	}
	if !result.OK || result.NodeUID != c.NodeUID || result.ServerID != c.ServerID || !result.Persistent {
		return errors.New("guest did not confirm identity-bound persistent guard")
	}
	return nil
}
func (r *LiveRuntime) SetReady(ctx context.Context, c Candidate) error {
	return r.Kube.SetReady(ctx, c)
}
func (r *LiveRuntime) BackendReady(ctx context.Context, c Candidate) (bool, error) {
	return r.Kube.BackendReady(ctx, c)
}
func (r *LiveRuntime) Publish(ctx context.Context, p Publication) (PublishResult, error) {
	result := PublishResult{}
	ips := []string{}
	for _, target := range p.Targets {
		candidate, ok := r.candidates[target.NodeUID]
		if !ok || !candidate.Identity.Equal(target) {
			return result, errors.New("publication identity absent from current verified candidates")
		}
		ips = append(ips, target.PrivateIP)
	}
	for _, host := range r.Config.Edge.Edges {
		if !r.edgeRunning[host.ID] {
			result.PendingEdges = append(result.PendingEdges, host.ID)
			continue
		}
		config, err := edge.RenderHAProxyBackends(r.Config.Edge, host.ID, ips, p.Generation)
		if err != nil {
			return result, err
		}
		sum := sha256.Sum256([]byte(config))
		request := map[string]any{"project_id": r.Config.Edge.ProjectID, "manager_id": r.Config.ManagementServerID, "scope_hash": r.Config.ScopeHash(), "edge_id": host.ID, "identity_file": r.Config.PlatformIdentityFile, "identity_label": r.Config.EdgeIdentityLabels[host.ID], "generation": p.Generation, "targets": ips, "config": config, "config_sha256": hex.EncodeToString(sum[:]), "adoption_sha256": r.Config.AdoptionSHA256[host.ID]}
		var response struct {
			OK           bool   `json:"ok"`
			EdgeID       string `json:"edge_id"`
			Generation   string `json:"generation"`
			ConfigSHA256 string `json:"config_sha256"`
			ServersState string `json:"servers_state"`
		}
		if err = r.remote(ctx, host.ID, host.PrivateIP, r.Config.EdgeSSHKey, false, applyEdgeScript, request, &response); err != nil {
			return result, fmt.Errorf("publishing ingress generation failed: %w", err)
		}
		if !response.OK || response.EdgeID != host.ID || response.Generation != p.Generation || response.ConfigSHA256 != hex.EncodeToString(sum[:]) {
			return result, errors.New("edge generation acknowledgement mismatch")
		}
		if err = VerifyServersState(response.ServersState, ips); err != nil {
			return result, err
		}
		result.AppliedEdges = append(result.AppliedEdges, host.ID)
	}
	if len(result.AppliedEdges) == 0 {
		return result, errors.New("no configured running edge available")
	}
	return result, nil
}

type DiscoveryManifest struct {
	SchemaVersion int        `json:"schema_version"`
	ProjectID     string     `json:"project_id"`
	ClusterUID    string     `json:"cluster_uid"`
	ManagerID     string     `json:"manager_id"`
	ScopeHash     string     `json:"scope_hash"`
	Generation    string     `json:"generation"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Targets       []Identity `json:"targets"`
}

func (r *LiveRuntime) Export(ctx context.Context, s State) error {
	if !r.edgeRunning[r.Config.Edge.ControllerServerID] {
		return errors.New("fixed discovery sink unavailable; serving ingress remains configured")
	}
	manifest := DiscoveryManifest{SchemaVersion: 1, ProjectID: r.Config.Edge.ProjectID, ClusterUID: r.Config.DownstreamSystemUID, ManagerID: r.Config.ManagementServerID, ScopeHash: r.Config.ScopeHash(), Generation: s.Generation, UpdatedAt: s.LastAttempt, Targets: append([]Identity{}, s.Targets...)}
	request := map[string]any{"identity_file": r.Config.PlatformIdentityFile, "project_id": r.Config.Edge.ProjectID, "manager_id": r.Config.ManagementServerID, "manifest": manifest}
	var response struct {
		OK     bool   `json:"ok"`
		SHA256 string `json:"sha256"`
	}
	if err := r.remote(ctx, r.Config.Edge.ControllerServerID, r.Config.ObserverIP, r.Config.EdgeSSHKey, false, publishDiscoveryScript, request, &response); err != nil {
		return err
	}
	if !response.OK || !regexpHex.MatchString(response.SHA256) {
		return errors.New("discovery sink rejected committed manifest")
	}
	if err := AtomicJSON(filepath.Join(r.Config.StateDir, "discovery.json"), manifest, 0600); err != nil {
		return err
	}
	return nil
}
