package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadOnlyIngressInventoryRejectsPartialPages(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			methods := []string{}
			cfg := config()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				w.Header().Set("Content-Type", "application/json")
				var body any
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers"):
					body = map[string]any{"count": 1, "result": []any{map[string]any{"id": "worker", "project": cfg.ProjectID}}}
				case strings.HasSuffix(r.URL.Path, "/volumes"):
					count := 1
					if partial {
						count = 2
					}
					body = map[string]any{"count": count, "result": []any{map[string]any{"id": "boot", "bootable": true}}}
				case strings.HasSuffix(r.URL.Path, "/addresses"):
					body = map[string]any{"count": 0, "result": []any{}}
				default:
					body = map[string]any{"result": map[string]any{"id": cfg.ProjectID, "status": "ACTIVE"}}
				}
				json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			client, err := newClient("fixture-token", server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			inv, err := client.Inventory(context.Background(), cfg.ProjectID)
			if (err != nil) != partial {
				t.Fatalf("inventory completeness: %+v %v", inv, err)
			}
			if !partial && (len(inv.Servers) != 1 || len(inv.Volumes) != 1) {
				t.Fatal("inventory data lost")
			}
			for _, method := range methods {
				if method != "GET" {
					t.Fatal("ingress performed cloud write")
				}
			}
		})
	}
}
