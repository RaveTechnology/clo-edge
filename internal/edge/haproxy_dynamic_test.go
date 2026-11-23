package edge

import (
	"strings"
	"testing"
)

func TestDynamicBackendsSupportZeroOneAndReplacementWithoutConfigMutation(t *testing.T) {
	c := testConfig()
	before := c.Hash()
	for _, targets := range [][]string{nil, {"10.0.0.30"}, {"10.0.0.30", "10.0.0.31", "10.0.0.32"}} {
		out, err := RenderHAProxyBackends(c, c.Edges[0].ID, targets, "generation-123")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(out, " send-proxy-v2") != 2*len(targets) || !strings.Contains(out, "clo-ingress-generation-123.sock") || strings.Contains(out, c.IngressIPs[0]+":") {
			t.Fatal(out)
		}
	}
	if before != c.Hash() {
		t.Fatal("VIP configuration/hash mutated")
	}
	for _, targets := range [][]string{{"127.0.0.1"}, {"8.8.8.8"}, {c.Edges[0].PrivateIP}, {"10.0.0.30", "10.0.0.30"}} {
		if _, err := RenderHAProxyBackends(c, c.Edges[0].ID, targets, "generation-123"); err == nil {
			t.Fatal("unsafe dynamic targets accepted")
		}
	}
}
