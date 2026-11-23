package ingress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"
)

func makeArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func archiveFixture(t *testing.T) []byte {
	return makeArchive(t, map[string]string{"machines/worker-a/config.json": `{"Driver":{"MachineName":"worker-a","IPAddress":"10.0.0.20","ApiToken":"fixture-cloud-secret","Ownership":{"ProjectID":"11111111-1111-4111-8111-111111111111","ProviderName":"clo-worker-a-0123456789abcdef","Attempt":"0123456789abcdef","ServerID":"99999999-9999-4999-8999-999999999999","BootVolumeIDs":["dddddddd-dddd-4ddd-8ddd-dddddddddddd"],"Pending":false}}}`, "machines/worker-a/id_rsa": "fixture-private-ssh-material"})
}
func TestArchiveReadsOnlyCurrentMachineAndNeverSerializesCredentials(t *testing.T) {
	state, err := ParseArchive(archiveFixture(t), "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if state.MachineName != "worker-a" || state.IPAddress != "10.0.0.20" || string(state.PrivateKey) != "fixture-private-ssh-material" {
		t.Fatal("wrong archive identity")
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), "fixture-cloud-secret") || strings.Contains(string(raw), "fixture-private-ssh-material") {
		t.Fatal("archive credential leaked into public state")
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		machine string
	}{{"foreign", archiveFixture(t), "worker-b"}, {"traversal", makeArchive(t, map[string]string{"../config.json": "{}"}), "worker-a"}, {"oversize", makeArchive(t, map[string]string{"huge": strings.Repeat("z", 17<<20)}), "worker-a"}, {"invalid", []byte("not-an-archive"), "worker-a"}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseArchive(tc.data, tc.machine); err == nil {
				t.Fatal("unbounded or foreign archive accepted")
			}
		})
	}
}
func TestRealRancherNameOnlyNodeRefStillPinsObservedNodeUID(t *testing.T) {
	cfg, s := fixture(t)
	s.Candidates[0].NodeRefName = "worker-a"
	s.Candidates[0].NodeRefUID = ""
	out, err := Verify(cfg, s)
	if err != nil || len(out) != 1 || out[0].NodeUID != "77777777-7777-4777-8777-777777777777" {
		t.Fatalf("real name-only Rancher link rejected: %v %v", out, err)
	}
	s.Candidates[0].NodeRefName = "worker-b"
	out, err = Verify(cfg, s)
	if err == nil && len(out) > 0 {
		t.Fatal("foreign NodeRef name accepted")
	}
}
