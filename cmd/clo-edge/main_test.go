package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

func TestCLICompleteAcceptedAttachDryRunAndDurableReceipt(t *testing.T) {
	args, f := cliFixture(t)
	factory := func(string) (edge.Provider, error) { return f, nil }
	var out bytes.Buffer
	if err := execute(context.Background(), append([]string{"init"}, args...), &out, factory, cliProbe{}); err != nil {
		t.Fatal(err)
	}
	machine, _ := os.ReadFile("/etc/machine-id")
	store, err := edge.OpenStore(args[3], strings.TrimSpace(string(machine)))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	now := time.Now()
	s.Phase = edge.RecoveryRequired
	s.SourceID = s.ActiveID
	for id := range f.v.Servers {
		if id != s.SourceID && id != f.v.Address.ID && id != "33333333-3333-4333-8333-333333333333" {
			s.TargetID = id
		}
	}
	s.Pending = &edge.Pending{Kind: "attach", Accepted: true, DispatchedAt: now.Add(-time.Minute)}
	s.PhaseSince = now.Add(-time.Minute)
	if err = store.Save(s); err != nil {
		t.Fatal(err)
	}
	store.Close()
	v := f.v.Servers[s.SourceID]
	v.Status = "STOPPED"
	v.SwitchStatus = "OFF"
	f.v.Servers[s.SourceID] = v
	f.v.Address.Owner = s.TargetID
	journal := filepath.Join(args[3], "state.json")
	before, _ := os.ReadFile(journal)
	out.Reset()
	if err = execute(context.Background(), append(append([]string{"complete-accepted-attach"}, args...), "--dry-run"), &out, factory, cliRecoveryProbe{source: s.SourceID}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(journal)
	if !bytes.Equal(before, after) {
		t.Fatal("dry run changed journal bytes")
	}
	var receipt struct {
		Before edge.State `json:"before"`
		After  edge.State `json:"after"`
		DryRun bool       `json:"dry_run"`
	}
	if json.Unmarshal(out.Bytes(), &receipt) != nil || !receipt.DryRun || receipt.Before.Pending == nil || receipt.After.Phase != edge.Completed {
		t.Fatal("dry run receipt is incomplete")
	}
	out.Reset()
	if err = execute(context.Background(), append([]string{"complete-accepted-attach"}, args...), &out, factory, cliRecoveryProbe{source: s.SourceID}); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt.DryRun || receipt.After.Armed || receipt.After.ActiveID != s.TargetID {
		t.Fatal("completion receipt is incomplete")
	}
	got, err := edge.Inspect(args[3])
	if err != nil || got.Phase != edge.Completed || got.Pending != nil || f.writes != 0 {
		t.Fatal("CLI did not persist a read-only-cloud completion")
	}
}
func TestCLIDryRunFlagDoesNotSilentlyArm(t *testing.T) {
	args, f := cliFixture(t)
	if execute(context.Background(), append(append([]string{"arm"}, args...), "--dry-run"), &bytes.Buffer{}, func(string) (edge.Provider, error) { return f, nil }, cliProbe{}) == nil {
		t.Fatal("unsupported dry-run flag accepted")
	}
	if f.writes != 0 {
		t.Fatal("invalid invocation wrote cloud")
	}
}

type cliRecoveryProbe struct{ source string }

func (p cliRecoveryProbe) Observe(ctx context.Context, c edge.Config) edge.Observation {
	o := (cliProbe{}).Observe(ctx, c)
	o.Private[p.source] = false
	o.PrivateUnreachable = map[string]bool{p.source: true}
	return o
}

func TestCLILegacyVerificationJournalNeverAuthorizesRecovery(t *testing.T) {
	args, f := cliFixture(t)
	factory := func(string) (edge.Provider, error) { return f, nil }
	var out bytes.Buffer
	if err := execute(context.Background(), append([]string{"init"}, args...), &out, factory, cliProbe{}); err != nil {
		t.Fatal(err)
	}
	machine, _ := os.ReadFile("/etc/machine-id")
	store, err := edge.OpenStore(args[3], strings.TrimSpace(string(machine)))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	s.Phase = edge.RecoveryRequired
	s.SourceID = s.ActiveID
	s.TargetID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	s.PhaseSince = time.Now().Add(-5 * time.Minute)
	s.Reason = "native convergence deadline exceeded; settle pending job manually"
	if err = store.Save(s); err != nil {
		t.Fatal(err)
	}
	store.Close()
	v := f.v.Servers[s.SourceID]
	v.Status = "STOPPED"
	v.SwitchStatus = "OFF"
	f.v.Servers[s.SourceID] = v
	f.v.Address.Owner = s.TargetID
	var data []byte
	for _, p := range []edge.Phase{edge.Verifying, edge.RecoveryRequired} {
		at := s.PhaseSince.Add(time.Millisecond)
		if p == edge.RecoveryRequired {
			at = at.Add(4 * time.Minute)
		}
		message, _ := json.Marshal(map[string]any{"phase": p, "reason": func() string {
			if p == edge.RecoveryRequired {
				return s.Reason
			}
			return ""
		}()})
		row, _ := json.Marshal(map[string]string{"_MACHINE_ID": s.MachineID, "_SYSTEMD_UNIT": "clo-edge.service", "_SYSTEMD_INVOCATION_ID": "unit", "__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10), "MESSAGE": string(message)})
		data = append(data, row...)
		data = append(data, '\n')
	}
	proof := filepath.Join(filepath.Dir(args[3]), "verification.jsonl")
	if err = os.WriteFile(proof, data, 0644); err != nil {
		t.Fatal(err)
	}
	call := append(append([]string{"complete-verification-timeout"}, args...), "--verification-journal", proof, "--dry-run")
	if execute(context.Background(), call, &out, factory, cliRecoveryProbe{source: s.SourceID}) == nil {
		t.Fatal("public-readable proof accepted")
	}
	os.Chmod(proof, 0600)
	before, _ := os.ReadFile(filepath.Join(args[3], "state.json"))
	out.Reset()
	if err = execute(context.Background(), call, &out, factory, cliRecoveryProbe{source: s.SourceID}); err == nil {
		t.Fatal("legacy unbound journal authorized recovery")
	}
	after, _ := os.ReadFile(filepath.Join(args[3], "state.json"))
	if !bytes.Equal(before, after) || f.writes != 0 {
		t.Fatal("dry verification recovery mutated state/cloud")
	}
}

func TestCLIResumeAcceptedDetachDryRunAndDurableReceipt(t *testing.T) {
	args, f := cliFixture(t)
	factory := func(string) (edge.Provider, error) { return f, nil }
	var out bytes.Buffer
	if err := execute(context.Background(), append([]string{"init"}, args...), &out, factory, cliProbe{}); err != nil {
		t.Fatal(err)
	}
	machine, _ := os.ReadFile("/etc/machine-id")
	store, err := edge.OpenStore(args[3], strings.TrimSpace(string(machine)))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	now := time.Now()
	s.Phase = edge.RecoveryRequired
	s.SourceID = s.ActiveID
	for id := range f.v.Servers {
		if id != s.SourceID && id != f.v.Address.ID && id != "33333333-3333-4333-8333-333333333333" {
			s.TargetID = id
		}
	}
	s.Pending = &edge.Pending{Kind: "detach", Accepted: true, DispatchedAt: now.Add(-time.Minute)}
	s.PhaseSince = now.Add(-time.Minute)
	if err = store.Save(s); err != nil {
		t.Fatal(err)
	}
	store.Close()
	v := f.v.Servers[s.SourceID]
	v.Status = "STOPPED"
	v.SwitchStatus = "OFF"
	f.v.Servers[s.SourceID] = v
	f.v.Address.Owner = ""
	f.v.Address.Entity = ""
	f.v.Address.Type = "FREE"
	f.v.Address.Status = "DOWN"
	journal := filepath.Join(args[3], "state.json")
	before, _ := os.ReadFile(journal)
	out.Reset()
	if err = execute(context.Background(), append(append([]string{"resume-accepted-detach"}, args...), "--dry-run"), &out, factory, cliRecoveryProbe{source: s.SourceID}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(journal)
	if !bytes.Equal(before, after) {
		t.Fatal("dry run changed journal bytes")
	}
	var receipt struct {
		Before edge.State `json:"before"`
		After  edge.State `json:"after"`
		DryRun bool       `json:"dry_run"`
	}
	if json.Unmarshal(out.Bytes(), &receipt) != nil || !receipt.DryRun || receipt.Before.Pending == nil || receipt.After.Phase != edge.Attaching {
		t.Fatal("dry run receipt is incomplete")
	}
	out.Reset()
	if err = execute(context.Background(), append([]string{"resume-accepted-detach"}, args...), &out, factory, cliRecoveryProbe{source: s.SourceID}); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt.DryRun || !receipt.After.Armed || receipt.After.ActiveID != s.SourceID {
		t.Fatal("completion receipt is incomplete")
	}
	got, err := edge.Inspect(args[3])
	if err != nil || got.Phase != edge.Attaching || got.Pending != nil || f.writes != 0 {
		t.Fatal("CLI did not persist a read-only-cloud completion")
	}
}
