package ingress

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/RaveTechnology/clo-edge/internal/edge"
)

func fixture(t *testing.T) (Config, Snapshot) {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	ec, err := edge.DecodeConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Edge: ec, ManagementServerID: "44444444-4444-4444-8444-444444444444", ProvisioningClusterUID: "55555555-5555-4555-8555-555555555555", DownstreamSystemUID: "66666666-6666-4666-8666-666666666666", ClusterName: "rave-prod", Namespace: "fleet-default", PrivateCIDR: "10.0.0.0/24", ObserverIP: "10.0.0.12", MaxBackends: 2, PollSeconds: 30, Automatic: true}
	a := Candidate{Identity: Identity{Name: "worker-a", NodeUID: "77777777-7777-4777-8777-777777777777", MachineUID: "88888888-8888-4888-8888-888888888888", ServerID: "99999999-9999-4999-8999-999999999999", PrivateIP: "10.0.0.20", ProjectID: ec.ProjectID, ProviderName: "clo-worker-a-0123456789abcdef", Attempt: "0123456789abcdef", BootIDs: []string{"dddddddd-dddd-4ddd-8ddd-dddddddddddd"}}, NodeRefUID: "77777777-7777-4777-8777-777777777777", Ready: true, Schedulable: true, Workload: "general", OS: "linux", Arch: "amd64"}
	inv := Inventory{Servers: map[string]Server{a.ServerID: {ID: a.ServerID, ProjectID: ec.ProjectID, Name: a.ProviderName, Status: "ACTIVE", SwitchStatus: "ON", AddressIDs: []string{"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}, DiskIDs: a.BootIDs}}, Volumes: map[string]Volume{a.BootIDs[0]: {ID: a.BootIDs[0], ServerID: a.ServerID, Bootable: true}}, Addresses: map[string]Address{"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee": {ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", IP: a.PrivateIP, ServerID: a.ServerID, Entity: "server", Status: "ACTIVE", Version: 4}}}
	return cfg, Snapshot{ProvisioningClusterUID: cfg.ProvisioningClusterUID, DownstreamSystemUID: cfg.DownstreamSystemUID, Candidates: []Candidate{a}, Inventory: inv}
}
func cloneSnapshot(s Snapshot) Snapshot {
	b, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(b, &out)
	return out
}

func TestVerifyRejectsWrongIncarnationAndNativeBindings(t *testing.T) {
	cfg, original := fixture(t)
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"cluster", func(s *Snapshot) { s.DownstreamSystemUID = "foreign" }},
		{"node-ref", func(s *Snapshot) { s.Candidates[0].NodeRefUID = "foreign" }},
		{"project", func(s *Snapshot) { s.Candidates[0].ProjectID = "aaaaaaaa-1111-4111-8111-111111111111" }},
		{"server-name", func(s *Snapshot) {
			v := s.Inventory.Servers[s.Candidates[0].ServerID]
			v.Name = "another-machine"
			s.Inventory.Servers[v.ID] = v
		}},
		{"private-ip", func(s *Snapshot) { s.Candidates[0].PrivateIP = "10.0.0.21" }},
		{"foreign-address", func(s *Snapshot) {
			v := s.Inventory.Addresses["eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"]
			v.ServerID = "foreign"
			s.Inventory.Addresses[v.ID] = v
		}},
		{"public-address", func(s *Snapshot) {
			v := s.Inventory.Addresses["eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"]
			v.External = true
			s.Inventory.Addresses[v.ID] = v
		}},
		{"boot-owner", func(s *Snapshot) {
			v := s.Inventory.Volumes[s.Candidates[0].BootIDs[0]]
			v.ServerID = "foreign"
			s.Inventory.Volumes[v.ID] = v
		}},
		{"missing-boot", func(s *Snapshot) { delete(s.Inventory.Volumes, s.Candidates[0].BootIDs[0]) }},
		{"duplicate-node", func(s *Snapshot) { s.Candidates = append(s.Candidates, s.Candidates[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cloneSnapshot(original)
			tc.mutate(&s)
			if got, err := Verify(cfg, s); err == nil && len(got) > 0 {
				t.Fatal("unsafe identity accepted")
			}
		})
	}
	got, err := Verify(cfg, original)
	if err != nil || len(got) != 1 || got[0].NodeUID != "77777777-7777-4777-8777-777777777777" {
		t.Fatalf("valid candidate: %v %v", got, err)
	}
}
func TestPendingAndDedicatedNodesDoNotBecomeIngress(t *testing.T) {
	cfg, s := fixture(t)
	for _, change := range []func(*Candidate){func(c *Candidate) { c.Pending = true }, func(c *Candidate) { c.Ready = false }, func(c *Candidate) { c.Schedulable = false }, func(c *Candidate) { c.Workload = "voip" }, func(c *Candidate) { c.ControlPlane = true }} {
		x := cloneSnapshot(s)
		change(&x.Candidates[0])
		out, err := Verify(cfg, x)
		if err != nil || len(out) != 0 {
			t.Fatalf("ineligible node: %v %v", out, err)
		}
	}
}
func TestSelectionRetainsAcceptedUIDAndRejectsIPAlias(t *testing.T) {
	_, s := fixture(t)
	a := s.Candidates[0]
	b := a
	b.Name = "worker-b"
	b.NodeUID = "bbbbbbbb-1111-4111-8111-111111111111"
	b.PrivateIP = "10.0.0.21"
	c := b
	c.Name = "worker-c"
	c.NodeUID = "cccccccc-1111-4111-8111-111111111111"
	c.PrivateIP = "10.0.0.22"
	selected := Select([]Candidate{a, b, c}, []Identity{b.Identity, c.Identity}, 2)
	if len(selected) != 2 || selected[0].NodeUID != b.NodeUID || selected[1].NodeUID != c.NodeUID {
		t.Fatalf("accepted cohort churned: %v", selected)
	}
	replacement := a
	replacement.NodeUID = "ffffffff-1111-4111-8111-111111111111"
	replacement.ServerID = "ffffffff-2222-4222-8222-222222222222"
	if len(Survivors([]Identity{a.Identity}, []Candidate{replacement})) != 0 {
		t.Fatal("reused IP retained old identity")
	}
	if len(Select(nil, []Identity{a.Identity}, 2)) != 0 {
		t.Fatal("empty authoritative inventory retained a target")
	}
}
