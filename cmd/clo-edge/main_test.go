package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RaveTechnology/clo-edge/internal/edge"
)

type cliCloud struct {
	v      edge.View
	writes int
}

func TestPrivateRuntimeCredentialCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "systemd-credential")
	dest := filepath.Join(dir, "runtime-token")
	if e := os.WriteFile(src, []byte("unit-secret"), 0444); e != nil {
		t.Fatal(e)
	}
	if _, e := tokenFile(src); e == nil {
		t.Fatal("accepted world-readable direct token")
	}
	if out, e := exec.Command("install", "-m", "0600", src, dest).CombinedOutput(); e != nil {
		t.Fatalf("materialize credential: %s %v", out, e)
	}
	got, e := tokenFile(dest)
	if e != nil || got != "unit-secret" {
		t.Fatal("private runtime copy rejected")
	}
}

func (f *cliCloud) Snapshot(context.Context, edge.Config) (edge.View, error) { return f.v, nil }
func (f *cliCloud) Stop(context.Context, string) error                       { f.writes++; return nil }
func (f *cliCloud) Detach(context.Context, string) error                     { f.writes++; return nil }
func (f *cliCloud) Attach(context.Context, string, string, int) error        { f.writes++; return nil }

type cliProbe struct{}

func (cliProbe) Observe(_ context.Context, c edge.Config) edge.Observation {
	return edge.Observation{At: time.Now(), Public: true, Private: map[string]bool{c.Edges[0].ID: true, c.Edges[1].ID: true}}
}
func cliFixture(t *testing.T) ([]string, *cliCloud) {
	t.Helper()
	dir := t.TempDir()
	c := edge.Config{ProjectID: "11111111-1111-4111-8111-111111111111", AddressID: "22222222-2222-4222-8222-222222222222", ControllerServerID: "33333333-3333-4333-8333-333333333333", PublicIP: "192.0.2.10", PublicHost: "example.test", HealthPath: "/healthz", BandwidthMbps: 100, Edges: [2]edge.Edge{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PrivateIP: "10.0.0.10"}, {ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", PrivateIP: "10.0.0.11"}}, IngressIPs: []string{"10.0.0.20", "10.0.0.21"}}
	c.Validate()
	raw, _ := json.Marshal(c)
	cp := filepath.Join(dir, "config.json")
	tp := filepath.Join(dir, "token")
	os.WriteFile(cp, raw, 0600)
	os.WriteFile(tp, []byte("unit-secret"), 0600)
	f := &cliCloud{v: edge.View{Servers: map[string]edge.Server{}, Address: edge.Address{ID: c.AddressID, IP: c.PublicIP, Status: "ACTIVE", Type: "FIXED", Entity: "server", Owner: c.Edges[0].ID, External: true, Version: 4, Bandwidth: 100}}}
	for _, id := range []string{c.ControllerServerID, c.Edges[0].ID, c.Edges[1].ID} {
		f.v.Servers[id] = edge.Server{ID: id, ProjectID: c.ProjectID, Status: "ACTIVE", SwitchStatus: "ON"}
	}
	return []string{"--config", cp, "--state-dir", filepath.Join(dir, "state"), "--token-file", tp, "--listen", "127.0.0.1:0"}, f
}
func TestCLICommissionObserveAndCancel(t *testing.T) {
	args, f := cliFixture(t)
	factory := func(string) (edge.Provider, error) { return f, nil }
	var out bytes.Buffer
	call := func(cmd string) error {
		return execute(context.Background(), append([]string{cmd}, args...), &out, factory, cliProbe{})
	}
	if call("run") == nil {
		t.Fatal("run silently initialized")
	}
	if e := call("init"); e != nil {
		t.Fatal(e)
	}
	if call("init") == nil {
		t.Fatal("init replaced existing state")
	}
	if call("arm") == nil {
		t.Fatal("armed automatic=false")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := execute(ctx, append([]string{"run"}, args...), &out, factory, cliProbe{}); e != nil {
		t.Fatal(e)
	}
	if f.writes != 0 {
		t.Fatal("observe-only CLI mutated cloud")
	}
	out.Reset()
	if e := call("inspect"); e != nil {
		t.Fatal(e)
	}
	var state edge.State
	if json.Unmarshal(out.Bytes(), &state) != nil || state.Armed {
		t.Fatal("inspect did not return disarmed journal")
	}
}
func TestCLIRefusesNonLoopbackMetrics(t *testing.T) {
	args, f := cliFixture(t)
	args = append(args, "--listen", "0.0.0.0:9128")
	if execute(context.Background(), append([]string{"run"}, args...), &bytes.Buffer{}, func(string) (edge.Provider, error) { return f, nil }, cliProbe{}) == nil {
		t.Fatal("metrics exposed publicly")
	}
}
