package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RaveTechnology/clo-edge/internal/edge"
)

// Real HTTP, the production SDK adapter, real fsync journal, and the reconciler.
// The fake is exclusively the cloud boundary, including asynchronous effects.
func TestReconcilerAgainstAsyncNativeHTTP(t *testing.T) {
	for _, lose := range []string{"none", "stop", "detach", "attach"} {
		t.Run(lose, func(t *testing.T) {
			cfg := config()
			cfg.Automatic = true
			var mu sync.Mutex
			var calls []string
			statuses := map[string]string{cfg.ControllerServerID: "ACTIVE", cfg.Edges[0].ID: "ACTIVE", cfg.Edges[1].ID: "ACTIVE"}
			owner := cfg.Edges[0].ID
			addrType, addrStatus := "SERVER", "ACTIVE"
			job := ""
			reads := 0
			server := func(id string) map[string]any {
				power := "ON"
				if statuses[id] != "ACTIVE" {
					power = "OFF"
				}
				return map[string]any{"id": id, "name": "rave-technology-fixture", "project": cfg.ProjectID, "status": statuses[id], "switch_status": power}
			}
			address := func() map[string]any {
				a := map[string]any{"id": cfg.AddressID, "address": cfg.PublicIP, "status": addrStatus, "type": addrType, "version": 4, "external": true, "bandwidth_max_mbps": 1024}
				if owner != "" {
					a["attached_to"] = map[string]string{"entity": "server", "id": owner}
				}
				return a
			}
			svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer unit-secret" {
					t.Error("wrong native credentials")
				}
				if r.Method == "POST" {
					kind := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					calls = append(calls, kind)
					switch kind {
					case "stop":
						if r.URL.Path != "/v2/servers/"+cfg.Edges[0].ID+"/stop" {
							t.Error("stopped unrelated VM")
						}
						statuses[cfg.Edges[0].ID] = "STOPPING"
					case "detach":
						if r.URL.Path != "/v2/addresses/"+cfg.AddressID+"/detach" || statuses[cfg.Edges[0].ID] != "STOPPED" || owner != cfg.Edges[0].ID {
							t.Error("detach before terminal fencing")
						}
					case "attach":
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						if body["id"] != cfg.Edges[1].ID || body["entity"] != "server" || body["bandwidth_max_mbps"] != float64(1024) || owner != "" || addrType != "FREE" || statuses[cfg.Edges[0].ID] != "STOPPED" {
							t.Error("unsafe attach")
						}
					default:
						t.Error("unexpected native mutation")
					}
					job = kind
					reads = 0
					if lose == kind {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							conn.Close()
						}
						return
					}
					w.WriteHeader(202)
					return
				}
				var v any
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers"):
					reads++
					if reads >= 2 {
						switch job {
						case "stop":
							statuses[cfg.Edges[0].ID] = "STOPPED"
							addrStatus = "DOWN"
						case "detach":
							owner = ""
							addrType = "FREE"
							addrStatus = "DOWN"
						case "attach":
							owner = cfg.Edges[1].ID
							addrType = "SERVER"
							addrStatus = "ACTIVE"
						}
						job = ""
					}
					v = map[string]any{"count": 3, "result": []any{server(cfg.ControllerServerID), server(cfg.Edges[0].ID), server(cfg.Edges[1].ID)}}
				case strings.HasSuffix(r.URL.Path, "/addresses"):
					v = map[string]any{"count": 1, "result": []any{address()}}
				case strings.HasPrefix(r.URL.Path, "/v2/servers/"):
					v = map[string]any{"result": server(strings.Split(r.URL.Path, "/")[3])}
				case strings.HasPrefix(r.URL.Path, "/v2/addresses/"):
					v = map[string]any{"result": address()}
				default:
					v = map[string]any{"result": map[string]any{"id": cfg.ProjectID, "status": "ACTIVE"}}
				}
				json.NewEncoder(w).Encode(v)
			}))
			defer svc.Close()
			client, err := newClient("unit-secret", svc.URL, svc.Client())
			if err != nil {
				t.Fatal(err)
			}
			store, err := edge.OpenStore(filepath.Join(t.TempDir(), "state"), "host")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			eng := edge.Engine{Config: cfg, Cloud: client, Store: store}
			ctx := context.Background()
			at := time.Unix(1000, 0)
			if err = eng.Initialize(ctx, "host"); err != nil {
				t.Fatal(err)
			}
			observation := func(now time.Time, healthy bool) edge.Observation {
				return edge.Observation{At: now, Public: healthy, Private: map[string]bool{cfg.Edges[0].ID: healthy, cfg.Edges[1].ID: true}}
			}
			if err = eng.Arm(ctx, at, observation(at, true)); err != nil {
				t.Fatal(err)
			}
			for i := 0; i <= 200; i += 5 {
				now := at.Add(time.Duration(i) * time.Second)
				mu.Lock()
				public := owner == cfg.Edges[1].ID && addrStatus == "ACTIVE"
				mu.Unlock()
				o := observation(now, false)
				o.Public = public
				if err = eng.Tick(ctx, now, o); err != nil {
					t.Fatal(err)
				}
				// A new process incarnation uses the same durable journal between each tick.
				eng = edge.Engine{Config: cfg, Cloud: client, Store: store}
			}
			st, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if lose == "none" {
				if st.Phase != edge.Completed || st.Armed || owner != cfg.Edges[1].ID || !reflect.DeepEqual(calls, []string{"stop", "detach", "attach"}) {
					t.Fatalf("incomplete HTTP workflow: %+v %v", st, calls)
				}
			} else {
				want := map[string][]string{"stop": {"stop"}, "detach": {"stop", "detach"}, "attach": {"stop", "detach", "attach"}}[lose]
				if st.Phase != edge.RecoveryRequired || !reflect.DeepEqual(calls, want) {
					t.Fatalf("lost %s response replayed/advanced: %+v %v", lose, st, calls)
				}
			}
		})
	}
}
