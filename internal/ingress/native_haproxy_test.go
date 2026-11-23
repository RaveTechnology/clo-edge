package ingress

import (
	"context"
	"fmt"
	"github.com/RaveTechnology/clo-edge/internal/edge"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const nativeHAProxyImage = "docker.io/library/haproxy:2.8@sha256:f9e68f79bc73bd2e50527f8263e6a58ce875a2db695423c268bf95ee7230d641"

func TestNativeHAProxyGenerationRuntimeAndGracefulReload(t *testing.T) {
	if os.Getenv("CLO_INGRESS_HAPROXY_TEST") != "1" {
		t.Skip("explicit isolated Docker runtime acceptance")
	}
	cfg, _ := fixture(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "current.cfg")
	render := func(targets []string, generation string) string {
		t.Helper()
		text, err := edge.RenderHAProxyBackends(cfg.Edge, cfg.Edge.Edges[0].ID, targets, generation)
		if err != nil {
			t.Fatal(err)
		}
		text = strings.ReplaceAll(text, "/run/haproxy/", "/tmp/")
		text = strings.ReplaceAll(text, cfg.Edge.Edges[0].PrivateIP+":8405", "127.0.0.1:18405")
		text = strings.ReplaceAll(text, "0.0.0.0:80\n", "127.0.0.1:18080\n")
		text = strings.ReplaceAll(text, "0.0.0.0:443\n", "127.0.0.1:18443\n")
		return text
	}
	first := []string{"10.0.0.30"}
	if err := os.WriteFile(path, []byte(render(first, "native-first")), 0600); err != nil {
		t.Fatal(err)
	}
	for _, targets := range [][]string{nil, first, {"10.0.0.30", "10.0.0.31", "10.0.0.32"}} {
		cmd := exec.Command("docker", "run", "--rm", "--network", "none", "-i", "--entrypoint", "haproxy", nativeHAProxyImage, "-c", "-f", "/dev/stdin")
		cmd.Stdin = strings.NewReader(render(targets, "parser"))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("real HAProxy parser rejected dynamic targets: %s %v", output, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := []string{"create", "--network", "none", "--user", "0", "--entrypoint", "haproxy", nativeHAProxyImage, "-W", "-db", "-f", "/tmp/current.cfg"}
	raw, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("isolated HAProxy create: %s %v", raw, err)
	}
	container := strings.TrimSpace(string(raw))
	defer exec.Command("docker", "rm", "-f", container).Run()
	probeSource := filepath.Join(directory, "probe.go")
	probeProgram := `package main
import("fmt";"io";"net";"os";"time")
func main(){c,e:=net.DialTimeout("unix",os.Args[1],time.Second);if e!=nil{os.Exit(1)};defer c.Close();c.SetDeadline(time.Now().Add(2*time.Second));fmt.Fprintln(c,"show servers state");b,e:=io.ReadAll(io.LimitReader(c,1<<20));if e!=nil{os.Exit(2)};os.Stdout.Write(b)}
`
	if err := os.WriteFile(probeSource, []byte(probeProgram), 0600); err != nil {
		t.Fatal(err)
	}
	probeBinary := filepath.Join(directory, "probe")
	build := exec.CommandContext(ctx, "go", "build", "-o", probeBinary, probeSource)
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("runtime probe build: %s %v", out, err)
	}
	for _, file := range []string{path, probeBinary} {
		if out, err := exec.CommandContext(ctx, "docker", "cp", file, container+":/tmp/"+filepath.Base(file)).CombinedOutput(); err != nil {
			t.Fatalf("copy isolated fixture: %s %v", out, err)
		}
	}
	if out, err := exec.CommandContext(ctx, "docker", "start", container).CombinedOutput(); err != nil {
		t.Fatalf("isolated HAProxy start: %s %v", out, err)
	}
	read := func(generation string, targets []string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var last error
		for time.Now().Before(deadline) {
			output, err := exec.CommandContext(ctx, "docker", "exec", container, "/tmp/probe", "/tmp/clo-ingress-"+generation+".sock").CombinedOutput()
			if err == nil {
				last = VerifyServersState(string(output), targets)
			} else {
				last = fmt.Errorf("%s %w", output, err)
			}
			if last == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("real generation %s not accepted: %v", generation, last)
	}
	read("native-first", first)
	second := []string{"10.0.0.31", "10.0.0.32"}
	candidate := filepath.Join(directory, "next.cfg")
	os.WriteFile(candidate, []byte(render(second, "native-second")), 0600)
	if out, err := exec.CommandContext(ctx, "docker", "cp", candidate, container+":/tmp/next.cfg").CombinedOutput(); err != nil {
		t.Fatalf("copy next config: %s %v", out, err)
	}
	if out, err := exec.CommandContext(ctx, "docker", "exec", container, "/bin/sh", "-c", "mv /tmp/next.cfg /tmp/current.cfg").CombinedOutput(); err != nil {
		t.Fatalf("atomic fixture config: %s %v", out, err)
	}
	if out, err := exec.CommandContext(ctx, "docker", "kill", "--signal=USR2", container).CombinedOutput(); err != nil {
		t.Fatalf("graceful HAProxy reload: %s %v", out, err)
	}
	read("native-second", second)
}
