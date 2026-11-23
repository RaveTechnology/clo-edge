package ingress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreIsExclusivePrivateAndHostBound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "manager")
	s, err := OpenStore(dir, "host-a", "scope")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenStore(dir, "host-a", "scope"); err == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
	st := State{Version: 1, MachineID: "host-a", ScopeHash: "scope", Phase: "Ready"}
	if err = s.Save(st); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir, "host-b", "scope")
	if err == nil {
		_, err = s.Load()
		s.Close()
	}
	if err == nil {
		t.Fatal("cloned host accepted state")
	}
	s, err = OpenStore(dir, "host-a", "scope")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := filepath.Join(dir, "state.json")
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state is not private")
	}
	if err = os.WriteFile(p, []byte(`{"state":{},"sha256":"tampered"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}
