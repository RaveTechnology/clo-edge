package ingress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

type metadata struct {
	Name              string            `json:"name"`
	UID               string            `json:"uid"`
	ResourceVersion   string            `json:"resourceVersion"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
	Labels            map[string]string `json:"labels"`
	OwnerReferences   []struct {
		Kind string `json:"kind"`
		UID  string `json:"uid"`
	} `json:"ownerReferences"`
}
type condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}
type kubeNode struct {
	Metadata metadata `json:"metadata"`
	Spec     struct {
		Unschedulable bool              `json:"unschedulable"`
		Taints        []json.RawMessage `json:"taints"`
	} `json:"spec"`
	Status struct {
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}
type capiMachine struct {
	Metadata metadata `json:"metadata"`
	Spec     struct {
		ClusterName string `json:"clusterName"`
	} `json:"spec"`
	Status struct {
		NodeRef *struct {
			Name string `json:"name"`
			UID  string `json:"uid"`
		} `json:"nodeRef"`
	} `json:"status"`
}
type Kubernetes struct {
	Config Config
	Runner Runner
}

func (k *Kubernetes) request(ctx context.Context, management bool, args []string, dst any) error {
	path := k.Config.DownstreamKubeconfig
	if management {
		path = k.Config.ManagementKubeconfig
	}
	binary := k.Config.Kubectl
	if binary == "" {
		binary = "/usr/local/bin/kubectl"
	}
	argv := []string{"--kubeconfig=" + path, "--request-timeout=25s"}
	if k.Config.StateDir != "" {
		argv = append(argv, "--cache-dir="+filepath.Join(k.Config.StateDir, "kubectl-cache"))
	}
	argv = append(argv, args...)
	argv = append(argv, "-o", "json")
	raw, err := k.Runner.Run(ctx, binary, argv, nil)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		if slices.Contains(args, "--ignore-not-found=true") {
			return nil
		}
		return errors.New("empty Kubernetes response is not authoritative")
	}
	if len(raw) > 16<<20 || json.Unmarshal(raw, dst) != nil {
		return errors.New("invalid bounded Kubernetes response")
	}
	return nil
}
func ready(conditions []condition) bool {
	for _, c := range conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}
func (k *Kubernetes) Discover(ctx context.Context) (Snapshot, error) {
	var out Snapshot
	var cluster, system struct {
		Metadata metadata `json:"metadata"`
	}
	if err := k.request(ctx, true, []string{"-n", k.Config.Namespace, "get", "clusters.provisioning.cattle.io", k.Config.ClusterName}, &cluster); err != nil {
		return out, err
	}
	if err := k.request(ctx, false, []string{"get", "namespace", "kube-system"}, &system); err != nil {
		return out, err
	}
	out.ProvisioningClusterUID = cluster.Metadata.UID
	out.DownstreamSystemUID = system.Metadata.UID
	if out.ProvisioningClusterUID != k.Config.ProvisioningClusterUID || out.DownstreamSystemUID != k.Config.DownstreamSystemUID {
		return out, errors.New("Kubernetes source cluster identity changed")
	}
	var nodes struct {
		Items []kubeNode `json:"items"`
	}
	var machines struct {
		Items []capiMachine `json:"items"`
	}
	if err := k.request(ctx, false, []string{"get", "nodes"}, &nodes); err != nil {
		return out, err
	}
	if len(nodes.Items) > 1024 {
		return out, errors.New("node inventory bound exceeded")
	}
	if err := k.request(ctx, true, []string{"-n", k.Config.Namespace, "get", "machines.cluster.x-k8s.io", "-l", "cluster.x-k8s.io/cluster-name=" + k.Config.ClusterName}, &machines); err != nil {
		return out, err
	}
	active := map[string]capiMachine{}
	for _, m := range machines.Items {
		if m.Metadata.DeletionTimestamp != nil {
			continue
		}
		if m.Spec.ClusterName != k.Config.ClusterName || m.Metadata.Labels["cluster.x-k8s.io/cluster-name"] != k.Config.ClusterName {
			return out, errors.New("foreign active Machine in cluster inventory")
		}
		if _, ok := active[m.Metadata.Name]; ok {
			return out, errors.New("duplicate Machine name")
		}
		active[m.Metadata.Name] = m
	}
	for _, node := range nodes.Items {
		if node.Metadata.Labels["workload"] != "general" || node.Metadata.DeletionTimestamp != nil {
			continue
		}
		c := Candidate{Identity: Identity{Name: node.Metadata.Name, NodeUID: node.Metadata.UID}, Ready: ready(node.Status.Conditions), Schedulable: !node.Spec.Unschedulable && len(node.Spec.Taints) == 0, Workload: node.Metadata.Labels["workload"], OS: node.Metadata.Labels["kubernetes.io/os"], Arch: node.Metadata.Labels["kubernetes.io/arch"]}
		_, c.ControlPlane = node.Metadata.Labels["node-role.kubernetes.io/control-plane"]
		for _, a := range node.Status.Addresses {
			if a.Type == "InternalIP" {
				if c.PrivateIP != "" {
					return out, errors.New("ambiguous private Node addresses")
				}
				c.PrivateIP = a.Address
			}
		}
		m, ok := active[c.Name]
		if !ok || m.Status.NodeRef == nil {
			c.Pending = true
			out.Candidates = append(out.Candidates, c)
			continue
		}
		c.MachineUID = m.Metadata.UID
		c.NodeRefName = m.Status.NodeRef.Name
		c.NodeRefUID = m.Status.NodeRef.UID
		if !c.Ready || !c.Schedulable || c.ControlPlane {
			out.Candidates = append(out.Candidates, c)
			continue
		}
		var secret struct {
			Data map[string]string `json:"data"`
		}
		if err := k.request(ctx, true, []string{"-n", k.Config.Namespace, "get", "secret", c.Name + "-machine-state", "--ignore-not-found=true"}, &secret); err != nil {
			return out, err
		}
		data, ok := secret.Data["extractedConfig"]
		if !ok {
			c.Pending = true
			out.Candidates = append(out.Candidates, c)
			continue
		}
		if len(data) > 12<<20 {
			return out, errors.New("archive encoding bound exceeded")
		}
		blob, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return out, errors.New("invalid machine archive encoding")
		}
		driver, err := ParseArchive(blob, c.Name)
		if err != nil {
			return out, err
		}
		if driver.IPAddress != c.PrivateIP {
			return out, errors.New("current Node and machine archive address differ")
		}
		owner := driver.Ownership
		c.ProjectID = owner.ProjectID
		c.ProviderName = owner.ProviderName
		c.Attempt = owner.Attempt
		c.ServerID = owner.ServerID
		c.BootIDs = owner.BootVolumeIDs
		c.PrivateKey = driver.PrivateKey
		c.Pending = owner.Pending || owner.Deleting || owner.Removed || owner.ServerID == "" || len(owner.BootVolumeIDs) == 0
		out.Candidates = append(out.Candidates, c)
	}
	return out, nil
}
func (k *Kubernetes) node(ctx context.Context, c Candidate) (kubeNode, error) {
	var node kubeNode
	err := k.request(ctx, false, []string{"get", "node", c.Name}, &node)
	if err != nil {
		return node, err
	}
	if node.Metadata.Name != c.Name || node.Metadata.UID != c.NodeUID || node.Metadata.DeletionTimestamp != nil || !ready(node.Status.Conditions) || node.Metadata.Labels["workload"] != "general" {
		return node, errors.New("selected Node incarnation is no longer ready/owned")
	}
	return node, nil
}
func (k *Kubernetes) SetReady(ctx context.Context, c Candidate) error {
	node, err := k.node(ctx, c)
	if err != nil {
		return err
	}
	key := "clo.rave-technology.io/ingress-ready"
	if node.Metadata.Labels[key] == "true" {
		return nil
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": c.NodeUID}, {"op": "test", "path": "/metadata/resourceVersion", "value": node.Metadata.ResourceVersion}, {"op": "add", "path": "/metadata/labels/clo.rave-technology.io~1ingress-ready", "value": "true"}}
	raw, _ := json.Marshal(patch)
	var response json.RawMessage
	if err = k.request(ctx, false, []string{"patch", "node", c.Name, "--type=json", "-p", string(raw)}, &response); err != nil {
		// Reading the exact incarnation can settle a lost patch response; never replay it blindly.
		current, readErr := k.node(ctx, c)
		if readErr != nil || current.Metadata.Labels[key] != "true" {
			return err
		}
	}
	return nil
}
func (k *Kubernetes) BackendReady(ctx context.Context, c Candidate) (bool, error) {
	if _, err := k.node(ctx, c); err != nil {
		return false, err
	}
	namespace := k.Config.TraefikNamespace
	if namespace == "" {
		namespace = "ingress-system"
	}
	var ds struct {
		Metadata metadata `json:"metadata"`
	}
	if err := k.request(ctx, false, []string{"-n", namespace, "get", "daemonset", "traefik"}, &ds); err != nil {
		return false, err
	}
	if ds.Metadata.UID != k.Config.TraefikUID {
		return false, errors.New("Traefik DaemonSet identity changed")
	}
	var pods struct {
		Items []struct {
			Metadata metadata `json:"metadata"`
			Spec     struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Conditions []condition `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := k.request(ctx, false, []string{"-n", namespace, "get", "pods", "-l", "app.kubernetes.io/name=traefik", "--field-selector=spec.nodeName=" + c.Name}, &pods); err != nil {
		return false, err
	}
	count := 0
	for _, pod := range pods.Items {
		if pod.Metadata.DeletionTimestamp != nil {
			continue
		}
		owned := false
		for _, ref := range pod.Metadata.OwnerReferences {
			owned = owned || (ref.Kind == "DaemonSet" && ref.UID == ds.Metadata.UID)
		}
		if !owned || pod.Spec.NodeName != c.Name {
			return false, fmt.Errorf("unexpected Pod identity for selected ingress Node")
		}
		if ready(pod.Status.Conditions) {
			count++
		}
	}
	return count == 1, nil
}
