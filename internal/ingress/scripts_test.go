package ingress

import (
	"os/exec"
	"testing"
)

func TestEmbeddedHostScriptContracts(t *testing.T) {
	command := exec.Command("python3", "-m", "unittest", "discover", "-s", "scripts", "-p", "test_scripts.py", "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("host preparation/runtime contract: %s %v", output, err)
	}
}
