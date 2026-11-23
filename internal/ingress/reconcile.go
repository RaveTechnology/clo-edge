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
	retained := append([]Candidate{}, available...)
	unknown := false
	for _, c := range snapshot.Candidates {
		if unknownBinding(c) {
			unknown = true
		}
	}
	for _, old := range st.Targets {
		if c, ok := retainedUnknown(e.Config, snapshot, old); ok {
			retained = append(retained, c)
		}
	}
	exported := false
	publish := func(targets []Identity, withdraw bool) error {
		if st.Pending == nil || !reflect.DeepEqual(st.Pending.Targets, targets) {
			g := uuid.NewString()
			if st.Generation != "" && reflect.DeepEqual(st.Targets, targets) {
				g = st.Generation
			}
			st.Pending = &Publication{Generation: g, Targets: append([]Identity{}, targets...), WithdrawOnly: withdraw}
		}
		st.Pending.WithdrawOnly = withdraw
		st.Phase = "Publishing"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		result, publishErr := e.Runtime.Publish(ctx, *st.Pending)
		st.PublicationResult = result
		if publishErr != nil {
			_ = e.Store.Save(st)
			return publishErr
		}
		if len(result.AppliedEdges) == 0 {
			return errors.New("no owned edge accepted publication")
		}
		st.Targets = append([]Identity{}, targets...)
		st.Generation = st.Pending.Generation
		st.Pending = nil
		if err := e.Store.Save(st); err != nil {
			return err
		}
		if err := e.Runtime.Export(ctx, st); err != nil {
			return err
		}
		exported = true
		return nil
	}
	survivors := Survivors(st.Targets, retained)
	pendingInvalid := st.Pending != nil && len(Survivors(st.Pending.Targets, retained)) != len(st.Pending.Targets)
	if len(survivors) != len(st.Targets) || pendingInvalid {
		resuming := st.Pending != nil && st.Pending.WithdrawOnly && reflect.DeepEqual(st.Pending.Targets, survivors) && !pendingInvalid && !unknown
		if !resuming {
			if err := publish(survivors, true); err != nil {
				return err
			}
		}
	} else if st.Generation != "" && st.Pending == nil {
		if err := publish(st.Targets, true); err != nil {
			return err
		}
	}
	if unknown {
		return errors.New("current machine binding temporarily unknown; no new adoption")
	}
	preferred := append([]Identity{}, st.Targets...)
	if st.Pending != nil {
		preferred = append(preferred, st.Pending.Targets...)
	}
	ordered := Select(available, preferred, len(available))
	existing, newCandidates := []Candidate{}, []Candidate{}
	for _, c := range ordered {
		found := false
		for _, p := range preferred {
			if p.Equal(c.Identity) {
				found = true
				break
			}
		}
		if found {
			existing = append(existing, c)
		} else {
			newCandidates = append(newCandidates, c)
		}
	}
	if len(newCandidates) > 0 {
		at := st.CandidateCursor % len(newCandidates)
		newCandidates = append(append([]Candidate{}, newCandidates[at:]...), newCandidates[:at]...)
	}
	ordered = append(existing, newCandidates...)
	ready, uncertain := []Identity{}, []Identity{}
	failures := []error{}
	for index, c := range ordered {
		if len(ready) >= e.Config.MaxBackends {
			break
		}
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		if index >= len(existing) {
			st.CandidateCursor++
		}
		identity := c.Identity
		st.Preparing = &identity
		st.Phase = "Preparing"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		attempt, stop := context.WithTimeout(ctx, 15*time.Second)
		err := e.Runtime.Prepare(attempt, c)
		if err == nil {
			st.Phase = "Labeling"
			err = e.Store.Save(st)
			if err == nil {
				err = e.Runtime.SetReady(attempt, c)
			}
		}
		ok := false
		if err == nil {
			st.Phase = "WaitingForTraefik"
			err = e.Store.Save(st)
			if err == nil {
				ok, err = e.Runtime.BackendReady(attempt, c)
			}
		}
		stop()
		if err != nil {
			failures = append(failures, err)
			for _, old := range st.Targets {
				if old.Equal(identity) {
					uncertain = append(uncertain, old)
				}
			}
			continue
		}
		if ok {
			ready = append(ready, identity)
		}
	}
	for _, old := range uncertain {
		if len(ready) < e.Config.MaxBackends {
			ready = append(ready, old)
		}
	}
	if st.Generation == "" && len(ready) == 0 && len(snapshot.Candidates) > 0 {
		st.Phase = "WaitingForTraefik"
		if err := e.Store.Save(st); err != nil {
			return err
		}
		return errors.Join(failures...)
	}
	if st.Pending != nil || !reflect.DeepEqual(st.Targets, ready) || len(st.PublicationResult.PendingEdges) > 0 || !exported {
		if err := publish(ready, false); err != nil {
			return err
		}
	}
	st.Preparing = nil
	st.LastError = ""
	st.Phase = "Ready"
	if len(st.PublicationResult.PendingEdges) > 0 {
		st.Phase = "PendingEdges"
	}
	if len(ready) == 0 {
		st.Phase = "WaitingForNodes"
	}
	if err := e.Store.Save(st); err != nil {
		return err
	}
	st.LastSuccess = now
	if len(ready) == 0 && len(failures) > 0 {
		return errors.Join(failures...)
	}
	return e.Store.Save(st)
}
