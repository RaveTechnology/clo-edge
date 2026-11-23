package main

import (
	"bytes"
	"encoding/json"
	"github.com/RaveTechnology/clo-edge/internal/edge"
	"github.com/RaveTechnology/clo-edge/internal/ingress"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionAndInvalidInvocationNeverNeedCloudCredentials(t *testing.T) {
	var out, err bytes.Buffer
	if code := run([]string{"version"}, &out, &err); code != 0 || !strings.HasPrefix(out.String(), "clo-ingress ") {
		t.Fatalf("version: %d %s %s", code, &out, &err)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"run"}, {"check", "--config", "/missing/private"}} {
		out.Reset()
		err.Reset()
		if code := run(args, &out, &err); code == 0 {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
}
func TestCheckUsesPrivateFilesAndDisabledDefaultWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	raw, e := os.ReadFile("../../deploy/config.example.json")
	if e != nil {
		t.Fatal(e)
	}
	ec, e := edge.DecodeConfig(raw)
	if e != nil {
		t.Fatal(e)
	}
	machine, e := os.ReadFile("/etc/machine-id")
	if e != nil {
		t.Fatal(e)
	}
	c := ingress.Config{Edge: ec, ManagementServerID: "44444444-4444-4444-8444-444444444444", ProvisioningClusterUID: "55555555-5555-4555-8555-555555555555", DownstreamSystemUID: "66666666-6666-4666-8666-666666666666", TraefikUID: "77777777-7777-4777-8777-777777777777", ClusterName: "rave-prod", Namespace: "fleet-default", PrivateCIDR: "10.0.0.0/24", ObserverIP: "10.0.0.12", StateDir: filepath.Join(dir, "state"), ExpectedMachineID: strings.TrimSpace(string(machine)), EdgeIdentityLabels: map[string]string{ec.Edges[0].ID: "edge-a", ec.Edges[1].ID: "edge-b"}, AdoptionSHA256: map[string]string{ec.Edges[0].ID: strings.Repeat("a", 64), ec.Edges[1].ID: strings.Repeat("b", 64)}}
	for name, destination := range map[string]*string{"management": &c.ManagementKubeconfig, "downstream": &c.DownstreamKubeconfig, "token": &c.TokenFile, "ssh": &c.EdgeSSHKey, "hosts": &c.KnownHostsFile} {
		path := filepath.Join(dir, name)
		if e = os.WriteFile(path, []byte("fixture-private-data"), 0600); e != nil {
			t.Fatal(e)
		}
		*destination = path
	}
	config := filepath.Join(dir, "config.json")
	body, _ := json.Marshal(c)
	os.WriteFile(config, body, 0600)
	var out, errors bytes.Buffer
	if code := run([]string{"check", "--config", config}, &out, &errors); code != 0 {
		t.Fatalf("check made a network dependency or rejected safe config: %s %s", &out, &errors)
	}
	if !strings.Contains(out.String(), `"automatic":false`) || strings.Contains(out.String(), "fixture-private-data") {
		t.Fatal("unsafe default or credential output")
	}
	os.Chmod(config, 0644)
	out.Reset()
	errors.Reset()
	if code := run([]string{"check", "--config", config}, &out, &errors); code == 0 {
		t.Fatal("publicly readable runtime config accepted")
	}
}
