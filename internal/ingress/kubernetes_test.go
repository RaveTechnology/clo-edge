package ingress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

type runFunc func(context.Context, string, []string, []byte) ([]byte, error)

func (f runFunc) Run(c context.Context, p string, a []string, b []byte) ([]byte, error) {
	return f(c, p, a, b)
}

const nodeFixture = `{"metadata":{"name":"worker-a","uid":"77777777-7777-4777-8777-777777777777","resourceVersion":"17","labels":{"workload":"general","kubernetes.io/os":"linux","kubernetes.io/arch":"amd64"}},"spec":{},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.20"}],"conditions":[{"type":"Ready","status":"True"}]}}`

func TestKubernetesDiscoveryUsesCurrentNodeAndStableMachineArchiveOnly(t *testing.T) {
	cfg, _ := fixture(t)
	cfg.ManagementKubeconfig = "/private/management"
	cfg.DownstreamKubeconfig = "/private/downstream"
	archive := base64.StdEncoding.EncodeToString(archiveFixture(t))
	seen := []string{}
	runner := runFunc(func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		seen = append(seen, strings.Join(args, " "))
		var v any
		switch {
		case slices.Contains(args, "clusters.provisioning.cattle.io"):
			v = map[string]any{"metadata": map[string]any{"uid": cfg.ProvisioningClusterUID}}
		case slices.Contains(args, "namespace"):
			v = map[string]any{"metadata": map[string]any{"uid": cfg.DownstreamSystemUID}}
		case slices.Contains(args, "nodes"):
			return []byte(`{"items":[` + nodeFixture + `]}`), nil
		case slices.Contains(args, "machines.cluster.x-k8s.io"):
			return []byte(`{"items":[{"metadata":{"name":"worker-a","uid":"88888888-8888-4888-8888-888888888888","labels":{"cluster.x-k8s.io/cluster-name":"rave-prod"}},"spec":{"clusterName":"rave-prod"},"status":{"nodeRef":{"name":"worker-a"}}}]}`), nil
		case slices.Contains(args, "secret"):
			if !slices.Contains(args, "worker-a-machine-state") {
				t.Fatal("read a transient/unowned archive")
			}
			v = map[string]any{"data": map[string]string{"extractedConfig": archive}}
		default:
			return nil, errors.New("unexpected request")
		}
		out, _ := json.Marshal(v)
		return out, nil
	})
	source := Kubernetes{Config: cfg, Runner: runner}
	snapshot, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].NodeUID != "77777777-7777-4777-8777-777777777777" || snapshot.Candidates[0].NodeRefName != "worker-a" || snapshot.Candidates[0].ServerID != "99999999-9999-4999-8999-999999999999" {
		t.Fatalf("lost identity join: %+v", snapshot.Candidates)
	}
	if len(seen) != 5 {
		t.Fatalf("unbounded source calls: %v", seen)
	}
}
func TestLabelMutationPinsUIDAndPreservesOtherNodeFields(t *testing.T) {
	cfg, s := fixture(t)
	patches := []byte{}
	source := Kubernetes{Config: cfg, Runner: runFunc(func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if slices.Contains(args, "patch") {
			index := slices.Index(args, "-p")
			patches = []byte(args[index+1])
			return []byte(`{}`), nil
		}
		return []byte(nodeFixture), nil
	})}
	if err := source.SetReady(context.Background(), s.Candidates[0]); err != nil {
		t.Fatal(err)
	}
	var patch []map[string]any
	if json.Unmarshal(patches, &patch) != nil {
		t.Fatal("no structured patch")
	}
	if len(patch) != 3 || patch[0]["op"] != "test" || patch[0]["value"] != "77777777-7777-4777-8777-777777777777" || patch[1]["value"] != "17" || patch[2]["path"] != "/metadata/labels/clo.rave-technology.io~1ingress-ready" {
		t.Fatalf("unsafe patch: %s", patches)
	}
	mutations := 0
	source.Runner = runFunc(func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		if slices.Contains(args, "patch") {
			mutations++
		}
		return []byte(strings.ReplaceAll(nodeFixture, "77777777-7777-4777-8777-777777777777", "ffffffff-ffff-4fff-8fff-ffffffffffff")), nil
	})
	if source.SetReady(context.Background(), s.Candidates[0]) == nil || mutations != 0 {
		t.Fatal("replaced Node received label")
	}
}

func TestEmptySourceResponseIsNotAnAuthoritativeEmptyInventory(t *testing.T) {
	cfg, _ := fixture(t)
	k := Kubernetes{Config: cfg, Runner: runFunc(func(context.Context, string, []string, []byte) ([]byte, error) { return nil, nil })}
	var out any
	if k.request(context.Background(), false, []string{"get", "nodes"}, &out) == nil {
		t.Fatal("empty transport result interpreted as complete empty source")
	}
}

func TestFreshNodeChangesBlockReadinessMutation(t *testing.T) {
	cfg, s := fixture(t)
	for name, raw := range map[string]string{
		"tainted":            strings.Replace(nodeFixture, `"spec":{}`, `"spec":{"taints":[{"key":"dedicated","value":"voip","effect":"NoSchedule"}]}`, 1),
		"cordoned":           strings.Replace(nodeFixture, `"spec":{}`, `"spec":{"unschedulable":true}`, 1),
		"control-plane":      strings.Replace(nodeFixture, `"workload":"general"`, `"workload":"general","node-role.kubernetes.io/control-plane":"true"`, 1),
		"changed-IP":         strings.ReplaceAll(nodeFixture, "10.0.0.20", "10.0.0.99"),
		"wrong-OS":           strings.ReplaceAll(nodeFixture, `"linux"`, `"windows"`),
		"wrong-architecture": strings.ReplaceAll(nodeFixture, `"amd64"`, `"arm64"`),
	} {
		t.Run(name, func(t *testing.T) {
			mutations := 0
			k := Kubernetes{Config: cfg, Runner: runFunc(func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				if slices.Contains(args, "patch") {
					mutations++
				}
				return []byte(raw), nil
			})}
			if k.SetReady(context.Background(), s.Candidates[0]) == nil || mutations != 0 {
				t.Fatal("changed worker was labeled after discovery")
			}
		})
	}
}
