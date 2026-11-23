// clo-ingress reconciles private Rancher workers into the existing edge data path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/RaveTechnology/clo-edge/internal/ingress"
	"github.com/RaveTechnology/clo-edge/internal/provider"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

func main() { syscall.Umask(0077); os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errs io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(out, "clo-ingress "+version)
		return 0
	}
	if len(args) == 0 || (args[0] != "check" && args[0] != "once" && args[0] != "run") {
		fmt.Fprintln(errs, "usage: clo-ingress version|check|once|run --config /private/config.json")
		return 2
	}
	flags := flag.NewFlagSet("clo-ingress", flag.ContinueOnError)
	flags.SetOutput(errs)
	path := flags.String("config", "", "private configuration file")
	if flags.Parse(args[1:]) != nil || *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(errs, "an explicit private --config is required")
		return 2
	}
	raw, err := ingress.ReadPrivateFile(*path, 1<<20)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	cfg, err := ingress.DecodeConfig(raw)
	if err == nil {
		err = cfg.ValidateRuntime()
	}
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	if args[0] == "check" {
		_ = json.NewEncoder(out).Encode(map[string]any{"config_valid": true, "automatic": cfg.Automatic, "scope_hash": cfg.ScopeHash()})
		return 0
	}
	token, err := ingress.ReadPrivateFile(cfg.TokenFile, 1<<20)
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	cloud, err := provider.New(strings.TrimSpace(string(token)))
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	runtime, err := ingress.NewRuntime(cfg, cloud, ingress.ExecRunner{})
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	cfg = runtime.Config
	store, err := ingress.OpenStore(cfg.StateDir, cfg.ExpectedMachineID, cfg.ScopeHash())
	if err != nil {
		fmt.Fprintln(errs, err)
		return 1
	}
	defer store.Close()
	engine := ingress.Engine{Config: cfg, Store: store, Runtime: runtime}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tick := func() error {
		bounded, stop := context.WithTimeout(ctx, 4*time.Minute)
		defer stop()
		err := engine.Tick(bounded, time.Now().UTC())
		if err != nil {
			fmt.Fprintln(errs, "ingress reconciliation:", err)
		}
		return err
	}
	if args[0] == "once" {
		err := tick()
		st, loadErr := store.Load()
		if loadErr != nil {
			fmt.Fprintln(errs, loadErr)
			return 1
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"phase": st.Phase, "targets": len(st.Targets), "observe_only": !cfg.Automatic, "success": err == nil})
		if err != nil {
			return 1
		}
		return 0
	}
	listener, err := net.Listen("tcp", "127.0.0.1:9130")
	if err != nil {
		fmt.Fprintln(errs, "private ingress metrics port unavailable")
		return 1
	}
	server := &http.Server{Handler: metrics(store, cfg.Automatic), ReadHeaderTimeout: 3 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	for {
		_ = tick()
		timer := time.NewTimer(time.Duration(cfg.PollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case err := <-serverDone:
			timer.Stop()
			if !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(errs, "private metrics server failed")
				return 1
			}
			return 0
		case <-timer.C:
		}
	}
}
func metrics(store *ingress.Store, automatic bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" && r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		s, err := store.Load()
		if r.URL.Path == "/readyz" {
			if err != nil || !automatic || s.Phase != "Ready" || s.LastError != "" || time.Since(s.LastSuccess) > 3*time.Minute {
				http.Error(w, "not ready", 503)
				return
			}
			fmt.Fprintln(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		ok := 0
		if err == nil {
			ok = 1
		}
		enabled := 0
		if automatic {
			enabled = 1
		}
		fmt.Fprintf(w, "rave_clo_ingress_manager_state_read_success %d\nrave_clo_ingress_manager_automatic %d\n", ok, enabled)
		if err == nil {
			success := 0
			if s.LastError == "" && !s.LastSuccess.IsZero() {
				success = 1
			}
			stamp := int64(0)
			if !s.LastSuccess.IsZero() {
				stamp = s.LastSuccess.Unix()
			}
			fmt.Fprintf(w, "rave_clo_ingress_manager_last_attempt_success %d\nrave_clo_ingress_manager_last_success_timestamp_seconds %s\nrave_clo_ingress_manager_targets %d\nrave_clo_ingress_manager_pending_edges %d\n", success, strconv.FormatInt(stamp, 10), len(s.Targets), len(s.PublicationResult.PendingEdges))
		}
	})
}
