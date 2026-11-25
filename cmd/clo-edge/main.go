// clo-edge is a fixed-host controller; it must never be deployed as replicas.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RaveTechnology/clo-edge/internal/edge"
	"github.com/RaveTechnology/clo-edge/internal/provider"
)

var version = "dev"

type probes interface {
	Observe(context.Context, edge.Config) edge.Observation
}
type factory func(string) (edge.Provider, error)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, os.Args[1:], os.Stdout, func(t string) (edge.Provider, error) { return provider.New(t) }, nil); err != nil {
		fmt.Fprintln(os.Stderr, "clo-edge:", err)
		os.Exit(1)
	}
}
func tokenFile(path string) (string, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", errors.New("cannot open token file")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("token file must be a private regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 || strings.TrimSpace(string(b)) == "" {
		return "", errors.New("invalid token file")
	}
	return strings.TrimSpace(string(b)), nil
}
func execute(ctx context.Context, args []string, out io.Writer, newCloud factory, probe probes) error {
	if len(args) == 0 {
		return errors.New("command required: init, arm, complete-accepted-attach, resume-accepted-detach, run, inspect, render-haproxy, version")
	}
	cmd := args[0]
	if cmd == "version" {
		fmt.Fprintln(out, version)
		return nil
	}
	fs := flag.NewFlagSet("clo-edge "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "/etc/clo-edge/config.json", "strict JSON configuration")
	stateDir := fs.String("state-dir", "/var/lib/clo-edge", "private local state directory")
	secret := fs.String("token-file", "/etc/clo-edge/api-token", "private token file")
	listen := fs.String("listen", "127.0.0.1:9128", "loopback status/metrics")
	dryRun := fs.Bool("dry-run", false, "evaluate accepted-attach recovery without changing the journal")
	edgeID := fs.String("edge-id", "", "configured edge ID for render-haproxy")
	if e := fs.Parse(args[1:]); e != nil || fs.NArg() != 0 {
		return errors.New("invalid command arguments")
	}
	switch cmd {
	case "init", "arm", "complete-accepted-attach", "resume-accepted-detach", "run", "inspect", "render-haproxy":
	default:
		return errors.New("unknown command")
	}
	if *dryRun && cmd != "complete-accepted-attach" && cmd != "resume-accepted-detach" {
		return errors.New("dry-run is supported only for complete-accepted-attach")
	}
	if cmd == "inspect" {
		st, e := edge.Inspect(*stateDir)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(st)
	}
	if cmd == "run" {
		host, _, e := net.SplitHostPort(*listen)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return errors.New("status listener must use a literal loopback IP")
		}
	}
	raw, e := os.ReadFile(*cfgPath)
	if e != nil {
		return errors.New("cannot read configuration")
	}
	cfg, e := edge.DecodeConfig(raw)
	if e != nil {
		return e
	}
	if cmd == "render-haproxy" {
		conf, e := edge.RenderHAProxy(cfg, *edgeID)
		if e == nil {
			_, e = io.WriteString(out, conf)
		}
		return e
	}
	machine, e := os.ReadFile("/etc/machine-id")
	if e != nil || strings.TrimSpace(string(machine)) == "" {
		return errors.New("fixed host /etc/machine-id is required")
	}
	store, e := edge.OpenStore(*stateDir, strings.TrimSpace(string(machine)))
	if e != nil {
		return e
	}
	defer store.Close()
	if cmd != "init" {
		if _, e = store.Load(); e != nil {
			return e
		}
	}
	token, e := tokenFile(*secret)
	if e != nil {
		return e
	}
	cloud, e := newCloud(token)
	if e != nil {
		return e
	}
	eng := &edge.Engine{Config: cfg, Store: store, Cloud: cloud}
	if probe == nil {
		probe = &edge.Prober{Timeout: time.Duration(cfg.ProbeSeconds) * time.Second}
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if cmd == "init" {
		if e = eng.Initialize(bounded, strings.TrimSpace(string(machine))); e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, `{"phase":"Watching","armed":false}`)
		return e
	}
	if cmd == "arm" {
		o := probe.Observe(bounded, cfg)
		if e = eng.Arm(bounded, time.Now(), o); e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, `{"phase":"Watching","armed":true}`)
		return e
	}
	if cmd == "complete-accepted-attach" || cmd == "resume-accepted-detach" {
		before, err := store.Load()
		if err != nil {
			return err
		}
		o := probe.Observe(bounded, cfg)
		var after edge.State
		if cmd == "complete-accepted-attach" {
			after, err = eng.CompleteAcceptedAttach(bounded, time.Now(), o, *dryRun)
		} else {
			after, err = eng.ResumeAcceptedDetach(bounded, time.Now(), o, *dryRun)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Before edge.State `json:"before"`
			After  edge.State `json:"after"`
			DryRun bool       `json:"dry_run"`
		}{before, after, *dryRun})
	}
	listener, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		st, e := edge.Inspect(*stateDir)
		if e != nil {
			http.Error(w, "state unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		armed := 0
		if st.Armed {
			armed = 1
		}
		fmt.Fprintf(w, "clo_edge_phase{phase=%q} 1\nclo_edge_armed %d\n", st.Phase, armed)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		st, e := edge.Inspect(*stateDir)
		if e != nil || st.Phase == edge.RecoveryRequired || st.Reason != "" {
			http.Error(w, "recovery or cloud inspection required", 503)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	srv := http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 10 * time.Second}
	defer srv.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	ticker := time.NewTicker(time.Duration(cfg.PollSeconds) * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		if ctx.Err() != nil {
			return nil
		}
		o := probe.Observe(ctx, cfg)
		tickCtx, done := context.WithTimeout(ctx, 30*time.Second)
		e = eng.Tick(tickCtx, time.Now(), o)
		done()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		st, e := store.Load()
		if e != nil {
			return e
		}
		status := string(st.Phase) + ":" + st.Reason
		if status != last {
			b, _ := json.Marshal(map[string]any{"phase": st.Phase, "armed": st.Armed, "reason": st.Reason})
			fmt.Fprintln(out, string(b))
			last = status
		}
		select {
		case <-ctx.Done():
			return nil
		case e := <-serveErr:
			return e
		case <-ticker.C:
		}
	}
}
