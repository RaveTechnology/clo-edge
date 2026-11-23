package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func addReserve(s *Snapshot) Candidate {
	c := s.Candidates[0]
	c.Name = "worker-b"
	c.NodeRefName = c.Name
	c.NodeUID = "12121212-1212-4212-8212-121212121212"
	c.NodeRefUID = c.NodeUID
	c.MachineUID = "13131313-1313-4313-8313-131313131313"
	c.ServerID = "14141414-1414-4414-8414-141414141414"
	c.PrivateIP = "10.0.0.21"
	c.ProviderName = "clo-worker-b-attempt"
	c.BootIDs = []string{"15151515-1515-4515-8515-151515151515"}
	a := "16161616-1616-4616-8616-161616161616"
	s.Inventory.Servers[c.ServerID] = Server{ID: c.ServerID, ProjectID: c.ProjectID, Name: c.ProviderName, Status: "ACTIVE", SwitchStatus: "ON", AddressIDs: []string{a}, DiskIDs: c.BootIDs}
	s.Inventory.Volumes[c.BootIDs[0]] = Volume{ID: c.BootIDs[0], ServerID: c.ServerID, Bootable: true}
	s.Inventory.Addresses[a] = Address{ID: a, IP: c.PrivateIP, ServerID: c.ServerID, Entity: "server", Status: "ACTIVE", Version: 4}
	s.Candidates = append(s.Candidates, c)
	return c
}
func makeUnknown(c *Candidate) {
	c.Pending = true
	c.ServerID = ""
	c.ProjectID = ""
	c.BootIDs = nil
	c.Attempt = ""
	c.ProviderName = ""
	c.PrivateKey = nil
}
func TestAcceptedIdentitySurvivesMissingArchiveButNotConfirmedIPReassignment(t *testing.T) {
	e, r, store := newEngine(t)
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	makeUnknown(&r.snapshot.Candidates[0])
	r.publications = nil
	_ = e.Tick(context.Background(), time.Now())
	st, _ := store.Load()
	if len(st.Targets) != 1 {
		t.Fatal("archive ambiguity withdrew working Node")
	}
	for key, a := range r.snapshot.Inventory.Addresses {
		a.ServerID = "17171717-1717-4717-8717-171717171717"
		r.snapshot.Inventory.Addresses[key] = a
	}
	_ = e.Tick(context.Background(), time.Now())
	st, _ = store.Load()
	if len(st.Targets) != 0 {
		t.Fatal("reassigned IP retained behind unknown archive")
	}
}
func TestStartupUnknownArchivesDoNotEmptyLegacyBackends(t *testing.T) {
	e, r, _ := newEngine(t)
	makeUnknown(&r.snapshot.Candidates[0])
	_ = e.Tick(context.Background(), time.Now())
	if len(r.publications) != 0 {
		t.Fatal("incomplete startup overwrote legacy serving config")
	}
}
func TestUnhealthyFirstCandidateDoesNotStarveHealthyReserve(t *testing.T) {
	for _, kind := range []string{"ssh", "pod"} {
		t.Run(kind, func(t *testing.T) {
			e, r, s := newEngine(t)
			e.Config.MaxBackends = 1
			addReserve(&r.snapshot)
			r.prepareFn = func(c Candidate) error {
				if kind == "ssh" && c.Name == "worker-a" {
					return errors.New("SSH unknown")
				}
				return nil
			}
			r.readyFn = func(c Candidate) (bool, error) { return kind != "pod" || c.Name != "worker-a", nil }
			if err := e.Tick(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			st, _ := s.Load()
			if len(st.Targets) != 1 || st.Targets[0].Name != "worker-b" {
				t.Fatal("healthy reserve never admitted")
			}
		})
	}
}
func TestCommittedWithdrawalExportedBeforeFailedPreparation(t *testing.T) {
	e, r, s := newEngine(t)
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	addReserve(&r.snapshot)
	r.snapshot.Candidates = r.snapshot.Candidates[1:]
	r.prepareError = errors.New("new guest unavailable")
	r.exports = nil
	_ = e.Tick(context.Background(), time.Now())
	st, _ := s.Load()
	if len(st.Targets) != 0 || len(r.exports) == 0 || len(r.exports[0].Targets) != 0 {
		t.Fatal("confirmed withdrawal stranded before discovery export")
	}
}
func TestEveryTickProvesSameGenerationAndStoppedEdgeDoesNotChurn(t *testing.T) {
	e, r, s := newEngine(t)
	r.publishFn = func(Publication) (PublishResult, error) {
		return PublishResult{AppliedEdges: []string{"a"}, PendingEdges: []string{"b"}}, nil
	}
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load()
	gen := st.Generation
	for i := 0; i < 3; i++ {
		if err := e.Tick(context.Background(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.publications) < 4 {
		t.Fatal("unchanged loaded generation never verified")
	}
	for _, p := range r.publications {
		if p.Generation != gen {
			t.Fatal("stopped edge caused new generation/reload churn")
		}
	}
}
func TestReachableSecondEdgeUpdatedWhenFirstSSHUnavailable(t *testing.T) {
	cfg, s := fixture(t)
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("fixture-key"), 0600)
	cfg.EdgeSSHKey = key
	cfg.SSH = "/usr/bin/ssh"
	calls := []string{}
	r := LiveRuntime{Config: cfg, candidates: map[string]Candidate{s.Candidates[0].NodeUID: s.Candidates[0]}, edgeRunning: map[string]bool{cfg.Edge.Edges[0].ID: true, cfg.Edge.Edges[1].ID: true}}
	r.Runner = runFunc(func(_ context.Context, _ string, _ []string, input []byte) ([]byte, error) {
		var c map[string]any
		json.Unmarshal(input, &c)
		id := c["edge_id"].(string)
		calls = append(calls, id)
		if id == cfg.Edge.Edges[0].ID {
			return nil, errors.New("SSH failed")
		}
		state := fmt.Sprintf("1\n# be_id be_name srv_id srv_name srv_addr srv_op_state\n3 http-ingresses 1 ingress-0 %s 2\n4 tls-ingresses 1 ingress-0 %s 2\n", s.Candidates[0].PrivateIP, s.Candidates[0].PrivateIP)
		return json.Marshal(map[string]any{"ok": true, "edge_id": id, "generation": c["generation"], "config_sha256": c["config_sha256"], "servers_state": state})
	})
	out, err := r.Publish(context.Background(), Publication{Generation: "19191919-1919-4919-8919-191919191919", Targets: []Identity{s.Candidates[0].Identity}})
	if err != nil || len(calls) != 2 || len(out.AppliedEdges) != 1 || len(out.PendingEdges) != 1 {
		t.Fatalf("reachable edge skipped after first SSH error: %+v %v calls%d", out, err, len(calls))
	}
}

func TestUnconfirmedArchiveKeyDoesNotPoisonCorrectedBinding(t *testing.T) {
	cfg, s := fixture(t)
	cfg.StateDir = t.TempDir()
	cfg.Kubectl = "kubectl"
	cfg.SSH = "ssh"
	c := s.Candidates[0]
	c.PrivateKey = []byte("bad-unconfirmed-key")
	r := LiveRuntime{Config: cfg, candidates: map[string]Candidate{c.NodeUID: c}}
	authOK := false
	r.Runner = runFunc(func(_ context.Context, program string, _ []string, _ []byte) ([]byte, error) {
		if program == "kubectl" {
			return []byte(nodeFixture), nil
		}
		if !authOK {
			return nil, errors.New("authentication rejected")
		}
		return json.Marshal(map[string]any{"ok": true, "node_uid": c.NodeUID, "server_id": c.ServerID, "persistent_guard": true})
	})
	r.Kube = Kubernetes{Config: cfg, Runner: r.Runner}
	if r.Prepare(context.Background(), c) == nil {
		t.Fatal("bad key accepted")
	}
	authOK = true
	c.PrivateKey = []byte("correct-current-key")
	r.candidates[c.NodeUID] = c
	if err := r.Prepare(context.Background(), c); err != nil {
		t.Fatalf("correct archive blocked by unauthenticated cache: %v", err)
	}
}

func TestPrivateAdmissionUsesReachableEdgesAndReportsFailedPing(t *testing.T) {
	cfg, s := fixture(t)
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("fixture-key"), 0600)
	cfg.EdgeSSHKey = key
	cfg.SSH = "ssh"
	r := LiveRuntime{Config: cfg, edgeRunning: map[string]bool{cfg.Edge.Edges[0].ID: true, cfg.Edge.Edges[1].ID: true}}
	good := true
	calls := 0
	r.Runner = runFunc(func(_ context.Context, _ string, _ []string, input []byte) ([]byte, error) {
		calls++
		var c map[string]any
		json.Unmarshal(input, &c)
		return json.Marshal(map[string]any{"ok": true, "edge_id": c["edge_id"], "reachable": good})
	})
	if ok, err := r.probeBackend(context.Background(), s.Candidates[0]); err != nil || !ok {
		t.Fatal("reachable private backend rejected")
	}
	good = false
	if ok, err := r.probeBackend(context.Background(), s.Candidates[0]); err != nil || ok {
		t.Fatal("failed edge ping admitted")
	}
	if calls < 3 {
		t.Fatal("private paths were not checked")
	}
}
