package edge

import "testing"

func TestHAProxyRenderRejectsUnconfiguredEdge(t *testing.T) {
	c := testConfig()
	if _, e := RenderHAProxy(c, "unowned"); e == nil {
		t.Fatal("rendered unconfigured edge")
	}
	c.Edges[0].PrivateIP = "10.0.0.10\n  bind :8405"
	if _, e := RenderHAProxy(c, c.Edges[0].ID); e == nil {
		t.Fatal("config injection allowed")
	}
}
