package edge

import (
	"context"
	"errors"
	"time"
)

// CompleteAcceptedAttach finishes only an acknowledged attach whose terminal
// postcondition can now be proved. It never submits, repeats or cancels cloud
// commands. Unknown dispatch outcomes and other recovery phases stay blocked.
func (e *Engine) CompleteAcceptedAttach(ctx context.Context, now time.Time, o Observation, dryRun bool) (State, error) {
	s, err := e.load()
	if err != nil {
		return State{}, err
	}
	reject := func(message string) (State, error) { return State{}, errors.New(message) }
	if s.Phase != RecoveryRequired || s.Armed || s.Pending == nil || !s.Pending.Accepted || s.Pending.Kind != "attach" {
		return reject("recovery requires a disarmed, durably accepted pending attach")
	}
	return e.completeFencedTransfer(ctx, now, o, s, dryRun)
}

func (e *Engine) completeFencedTransfer(ctx context.Context, now time.Time, o Observation, s State, dryRun bool) (State, error) {
	var err error
	reject := func(message string) (State, error) { return State{}, errors.New(message) }
	target, ok := e.Config.Other(s.SourceID)
	if !ok || s.ActiveID != s.SourceID || target.ID != s.TargetID || s.PhaseSince.IsZero() || s.PhaseSince.After(now) || (s.Pending != nil && (s.Pending.DispatchedAt.Before(s.PhaseSince) || s.Pending.DispatchedAt.After(now))) {
		return reject("accepted attach workflow binding is invalid")
	}
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	v, err := e.Cloud.Snapshot(ctx, e.Config)
	if err != nil {
		return State{}, err
	}
	if err = v.Validate(e.Config); err != nil {
		return State{}, err
	}
	if !v.Servers[s.SourceID].Stopped() || !v.Servers[s.TargetID].Running() || !v.Servers[e.Config.ControllerServerID].Running() || !v.Address.On(s.TargetID) {
		return reject("accepted attach lacks terminal source fence and target IPv4 ownership")
	}
	if !e.fresh(o, now.Add(time.Since(started))) || !o.Public || !o.Private[s.TargetID] || o.Private[s.SourceID] || !o.PrivateUnreachable[s.SourceID] {
		return reject("recovery requires fresh public/target health and an unreachable source health endpoint")
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	s.ActiveID = s.TargetID
	s.Phase = Completed
	s.Armed = false
	s.Pending = nil
	s.SuspectSince = time.Time{}
	s.LastObservation = o.At
	s.LastCompleted = now
	s.PhaseSince = now
	s.Reason = ""
	if !dryRun {
		if err = e.Store.Save(s); err != nil {
			return State{}, err
		}
	}
	return s, nil
}
