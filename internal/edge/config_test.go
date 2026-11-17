package edge

import (
	"encoding/json"
	"testing"
)

func testConfig() Config {
	return Config{ProjectID: "11111111-1111-4111-8111-111111111111", AddressID: "22222222-2222-4222-8222-222222222222", ControllerServerID: "33333333-3333-4333-8333-333333333333", PublicIP: "192.0.2.10", PublicHost: "example.test", HealthPath: "/healthz", BandwidthMbps: 100,
		Edges: [2]Edge{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PrivateIP: "10.0.0.10"}, {ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", PrivateIP: "10.0.0.11"}}, IngressIPs: []string{"10.0.0.20", "10.0.0.21"}}
}

func TestConfigRejectsUnsafeTopology(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"duplicate edge", func(c *Config) { c.Edges[1] = c.Edges[0] }},
		{"controller is edge", func(c *Config) { c.ControllerServerID = c.Edges[0].ID }},
		{"nonprivate edge", func(c *Config) { c.Edges[0].PrivateIP = "192.0.2.5" }},
		{"bad bandwidth", func(c *Config) { c.BandwidthMbps = 500 }},
		{"path injection", func(c *Config) { c.HealthPath = "/ok\r\nInjected: yes" }},
		{"host injection", func(c *Config) { c.PublicHost = "example.test/other" }},
		{"too short detection", func(c *Config) { c.SuspectSeconds = 1 }},
		{"duplicate ingress", func(c *Config) { c.IngressIPs[1] = c.IngressIPs[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			tc.change(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	c := testConfig()
	raw, _ := json.Marshal(c)
	if _, err := DecodeConfig(append(raw[:len(raw)-1], []byte(",\"unexpected\":true}")...)); err == nil {
		t.Fatal("unknown field accepted")
	}
	got, err := DecodeConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Automatic || got.PollSeconds != 5 || got.SuspectSeconds != 30 || got.ProbeSeconds != 2 {
		t.Fatalf("unsafe defaults: %+v", got)
	}
}
