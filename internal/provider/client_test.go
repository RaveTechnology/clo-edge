package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RaveTechnology/clo-edge/internal/edge"
)

func config() edge.Config {
	c := edge.Config{ProjectID: "11111111-1111-4111-8111-111111111111", AddressID: "22222222-2222-4222-8222-222222222222", ControllerServerID: "33333333-3333-4333-8333-333333333333", PublicIP: "192.0.2.10", PublicHost: "example.test", HealthPath: "/healthz", BandwidthMbps: 1024, Edges: [2]edge.Edge{{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PrivateIP: "10.0.0.10"}, {ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", PrivateIP: "10.0.0.11"}}, IngressIPs: []string{"10.0.0.20", "10.0.0.21"}}
	c.Validate()
	return c
}

func TestMutationsAreScopedAndNeverRetried(t *testing.T) {
	for _, status := range []int{202, 200, 500, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var paths []string
			var payload map[string]any
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.Method+" "+r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer unit-secret" {
					t.Error("missing auth")
				}
				if strings.HasSuffix(r.URL.Path, "attach") {
					json.NewDecoder(r.Body).Decode(&payload)
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"unit-secret"}`)
			}))
			defer s.Close()
			c, e := newClient("unit-secret", s.URL, s.Client())
			if e != nil {
				t.Fatal(e)
			}
			cfg := config()
			for _, call := range []func() error{func() error { return c.Stop(context.Background(), cfg.Edges[0].ID) }, func() error { return c.Detach(context.Background(), cfg.AddressID) }, func() error { return c.Attach(context.Background(), cfg.AddressID, cfg.Edges[1].ID, 1024) }} {
				e = call()
				if (e == nil) != (status == 202) {
					t.Fatalf("status=%d accepted=%v", status, e == nil)
				}
				if e != nil && strings.Contains(e.Error(), "unit-secret") {
					t.Fatal("token leaked")
				}
			}
			if len(paths) != 3 || paths[0] != "POST /v2/servers/"+cfg.Edges[0].ID+"/stop" || paths[1] != "POST /v2/addresses/"+cfg.AddressID+"/detach" {
				t.Fatalf("wrong calls: %v", paths)
			}
			if payload["entity"] != "server" || payload["id"] != cfg.Edges[1].ID || payload["bandwidth_max_mbps"] != float64(1024) {
				t.Fatalf("wrong attach: %+v", payload)
			}
		})
	}
}

func TestSnapshotRejectsIncompleteOrForeignAddress(t *testing.T) {
	for _, tc := range []struct {
		name          string
		count         int
		entity, owner string
		wantErr       bool
	}{
		{"valid", 1, "server", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", false},
		{"incomplete", 2, "server", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true},
		{"foreign entity", 1, "loadbalancer", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true},
		{"foreign owner", 1, "server", "cccccccc-cccc-4ccc-8ccc-cccccccccccc", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			addr := map[string]any{"id": cfg.AddressID, "address": cfg.PublicIP, "status": "ACTIVE", "type": "SERVER", "external": true, "version": 4, "bandwidth_max_mbps": 1024, "attached_to": map[string]string{"entity": tc.entity, "id": tc.owner}}
			server := func(id string) map[string]any {
				return map[string]any{"id": id, "project": cfg.ProjectID, "status": "ACTIVE", "switch_status": "ON"}
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var v any
				switch {
				case strings.HasSuffix(r.URL.Path, "/addresses"):
					v = map[string]any{"count": tc.count, "result": []any{addr}}
				case strings.HasSuffix(r.URL.Path, "/servers"):
					v = map[string]any{"count": 3, "result": []any{server(cfg.ControllerServerID), server(cfg.Edges[0].ID), server(cfg.Edges[1].ID)}}
				case strings.HasPrefix(r.URL.Path, "/v2/addresses/"):
					v = map[string]any{"result": addr}
				case strings.HasPrefix(r.URL.Path, "/v2/servers/"):
					v = map[string]any{"result": server(strings.Split(r.URL.Path, "/")[3])}
				case strings.HasPrefix(r.URL.Path, "/v2/projects/"):
					v = map[string]any{"result": map[string]any{"id": cfg.ProjectID, "status": "ACTIVE"}}
				default:
					t.Errorf("unexpected GET %s", r.URL)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(v)
			}))
			defer s.Close()
			c, _ := newClient("unit-secret", s.URL, s.Client())
			v, e := c.Snapshot(context.Background(), cfg)
			if (e != nil) != tc.wantErr {
				t.Fatalf("snapshot=%+v err=%v", v, e)
			}
		})
	}
}

func TestClientRefusesRedirect(t *testing.T) {
	hits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(202) }))
	defer other.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer s.Close()
	c, _ := newClient("unit-secret", s.URL, s.Client())
	if c.Stop(context.Background(), config().Edges[0].ID) == nil || hits != 0 {
		t.Fatal("followed native mutation redirect")
	}
}
