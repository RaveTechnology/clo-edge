package ingress

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"reflect"
	"time"
)

type Runtime interface {
	Discover(context.Context) (Snapshot, error)
	Prepare(context.Context, Candidate) error
	SetReady(context.Context, Candidate) error
	BackendReady(context.Context, Candidate) (bool, error)
	Publish(context.Context, Publication) (PublishResult, error)
	Export(context.Context, State) error
}
type Engine struct {
	Config  Config
	Store   *Store
	Runtime Runtime
}

func (e *Engine) Tick(ctx context.Context, now time.Time) (returned error) {
	if err := e.Config.Validate(); err != nil {
		return err
	}
	st, err := e.Store.Load()
	if os.IsNotExist(err) {
		st = State{Version: 1, MachineID: e.Store.machine, ScopeHash: e.Store.scope, Phase: "Starting"}
		err = nil
	}
	if err != nil {
		return err
	}
	st.LastAttempt = now
	defer func() {
		if returned != nil {
			st.LastError = "reconciliation failed; inspect the failing stage"
			_ = e.Store.Save(st)
		}
	}()
	snapshot, err := e.Runtime.Discover(ctx)
	if err != nil {
		return err
	}
	available, err := Verify(e.Config, snapshot)
	if err != nil {
		return err
	}
	st.ObservedCandidates = len(available)
	if !e.Config.Automatic {
		st.Phase = "Observing"
		st.LastError = ""
		return e.Store.Save(st)
	}
	publish := func(targets []Identity) error {
		if st.Pending == nil || !reflect.DeepEqual(st.Pending.Targets, targets) {
			st.Pending = &Publication{Generation: uuid.NewString(), Targets: targets}
		}
		st.Phase = "Publishing"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		result, err := e.Runtime.Publish(ctx, *st.Pending)
		if err != nil {
			return err
		}
		if len(result.AppliedEdges) == 0 {
			return errors.New("no owned edge accepted publication")
		}
		st.Targets = append([]Identity{}, targets...)
		st.PublicationResult = result
		st.Pending = nil
		return e.Store.Save(st)
	}
	survivors := Survivors(st.Targets, available)
	pendingInvalid := st.Pending != nil && len(Survivors(st.Pending.Targets, available)) != len(st.Pending.Targets)
	if len(survivors) != len(st.Targets) || pendingInvalid {
		if err := publish(survivors); err != nil {
			return err
		}
	}
	preferred := append([]Identity{}, st.Targets...)
	if st.Pending != nil {
		preferred = append(preferred, st.Pending.Targets...)
	}
	selected := Select(available, preferred, e.Config.MaxBackends)
	ready := []Identity{}
	for _, candidate := range selected {
		identity := candidate.Identity
		st.Preparing = &identity
		st.Phase = "Preparing"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		if err := e.Runtime.Prepare(ctx, candidate); err != nil {
			return err
		}
		st.Phase = "Labeling"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		if err := e.Runtime.SetReady(ctx, candidate); err != nil {
			return err
		}
		st.Phase = "WaitingForTraefik"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		ok, err := e.Runtime.BackendReady(ctx, candidate)
		if err != nil {
			return err
		}
		if ok {
			ready = append(ready, candidate.Identity)
		}
	}
	if st.Pending != nil || !reflect.DeepEqual(st.Targets, ready) || len(st.PublicationResult.PendingEdges) > 0 {
		if err := publish(ready); err != nil {
			return err
		}
	}
	st.Preparing = nil
	st.LastError = ""
	st.Phase = "Ready"
	if len(ready) == 0 {
		st.Phase = "WaitingForNodes"
	}
	if err := e.Store.Save(st); err != nil {
		return err
	}
	if err := e.Runtime.Export(ctx, st); err != nil {
		return err
	}
	st.LastSuccess = now
	return e.Store.Save(st)
}
