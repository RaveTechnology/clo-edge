package ingress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerPassesArgumentsWithoutShellExpansionAndBoundsErrors(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "probe")
	payload := "literal; $(touch " + filepath.Join(dir, "PWNED") + ")"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := (ExecRunner{}).Run(context.Background(), script, []string{payload}, nil)
	if err != nil || string(out) != payload {
		t.Fatalf("argument boundary lost: %s %v", out, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "PWNED")); !os.IsNotExist(err) {
		t.Fatal("shell expansion executed")
	}
	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte("#!/bin/sh\necho fixture-secret >&2\nexit 1\n"), 0700)
	_, err = (ExecRunner{}).Run(context.Background(), bad, nil, nil)
	if err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("child error exposed secret stderr")
	}
}
func TestRuntimeStateProofRejectsWrongOrMissingTargets(t *testing.T) {
	output := "1\n# be_id be_name srv_id srv_name srv_addr srv_op_state\n3 http-ingresses 1 ingress-0 10.0.0.20 2\n4 tls-ingresses 1 ingress-0 10.0.0.20 2\n"
	if err := VerifyServersState(output, []string{"10.0.0.20"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.ReplaceAll(output, "10.0.0.20", "10.0.0.99"), strings.Replace(output, "4 tls-ingresses 1 ingress-0 10.0.0.20 2\n", "", 1), "bad protocol"} {
		if err := VerifyServersState(bad, []string{"10.0.0.20"}); err == nil {
			t.Fatal("unproven runtime accepted")
		}
	}
}

func TestRealChildStdoutCannotBypassLimitThroughReadFrom(t *testing.T) {
	out, err := (ExecRunner{}).Run(context.Background(), "/usr/bin/python3", []string{"-c", "import sys;sys.stdout.write('x'*(17*1024*1024))"}, nil)
	if err == nil || len(out) > 16<<20 {
		t.Fatalf("real child exceeded output bound: bytes=%d error=%v", len(out), err)
	}
}
