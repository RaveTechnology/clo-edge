package ingress

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileRefusesSharedPermissionsAndSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("fixture-private"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadPrivateFile(path, 1024); err != nil || string(data) != "fixture-private" {
		t.Fatal("private credential rejected")
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(path, 1024); err == nil {
		t.Fatal("group-readable credential accepted")
	}
	os.Chmod(path, 0600)
	alias := path + "-alias"
	os.Symlink(path, alias)
	if _, err := ReadPrivateFile(alias, 1024); err == nil {
		t.Fatal("symlink credential accepted")
	}
	if _, err := ReadPrivateFile(path, 4); err == nil {
		t.Fatal("oversized credential accepted")
	}
}
