package edge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsAndBindsHost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := OpenStore(dir, "host-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(); !os.IsNotExist(err) {
		t.Fatalf("missing state silently adopted: %v", err)
	}
	st := State{Version: 1, MachineID: "host-one", ConfigHash: "config", Phase: Watching, ActiveID: "a"}
	if err = s.Save(st); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir, "host-one")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got.ActiveID != "a" {
		t.Fatalf("persistence: %+v %v", got, err)
	}
	s.Close()
	s, err = OpenStore(dir, "host-two")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Load(); err == nil {
		t.Fatal("journal cloned onto another host accepted")
	}
}

func TestStoreRejectsImpossibleSuspectState(t *testing.T) {
	s, e := OpenStore(filepath.Join(t.TempDir(), "state"), "host")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st := State{Version: 1, MachineID: "host", ConfigHash: "config", Phase: Watching, Armed: true, ActiveID: "a", SuspectSince: time.Unix(1000, 0)}
	if s.Save(st) == nil {
		t.Fatal("saved suspect window without last observation")
	}
}
func TestStoreDetectsSyntacticallyValidBitrot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, e := OpenStore(dir, "host")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st := State{Version: 1, MachineID: "host", ConfigHash: "config", Phase: Watching, ActiveID: "a"}
	if e = s.Save(st); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "state.json")
	raw, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	changed := strings.Replace(string(raw), `"armed": false`, `"armed": true`, 1)
	if changed == string(raw) {
		t.Fatal("fixture failed to corrupt a valid JSON value")
	}
	os.WriteFile(p, []byte(changed), 0600)
	if _, e = s.Load(); e == nil {
		t.Fatal("valid JSON bitrot silently armed controller")
	}
}

func TestStoreLockCrossProcess(t *testing.T) {
	if d := os.Getenv("CLO_EDGE_LOCK_TEST_DIR"); d != "" {
		s, e := OpenStore(d, "host")
		if e == nil {
			s.Close()
			os.Exit(20)
		}
		os.Exit(0)
	}
	dir := filepath.Join(t.TempDir(), "state")
	s, e := OpenStore(dir, "host")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreLockCrossProcess$")
	cmd.Env = append(os.Environ(), "CLO_EDGE_LOCK_TEST_DIR="+dir)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("second process acquired lock: %s %v", out, e)
	}
}

func TestStoreRejectsCorruptionAndSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, e := OpenStore(dir, "host")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, raw := range []string{`{`, `{"version":99}`, `{"version":1,"machine_id":"host","phase":"Watching","config_hash":"x","active_id":"a","extra":true}`} {
		if e = os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Load(); e == nil {
			t.Fatal("corrupt state accepted")
		}
	}
	if e = os.Remove(filepath.Join(dir, "state.json")); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "state.json"))
	if _, e = s.Load(); e == nil {
		t.Fatal("followed symlink")
	}
}
