package edge

import (
	"context"
	"errors"
	"time"
)

// ResumeAcceptedDetach acknowledges a terminal, durably accepted detach.
// It submits no cloud request: the normal engine owns the next attach intent.
// It cannot settle a lost acknowledgement or restart Watching automation.
func (e *Engine) ResumeAcceptedDetach(ctx context.Context, now time.Time, o Observation, dry bool) (State, error) {
	s, err := e.load()
	if err != nil {
		return State{}, err
	}
	reject := func() (State, error) {
		return State{}, errors.New("resume requires a bound accepted detach, terminal fence, free address and fresh target health")
	}
	if s.Phase != RecoveryRequired || s.Armed || s.Pending == nil || !s.Pending.Accepted || s.Pending.Kind != "detach" {
		return reject()
	}
	target, ok := e.Config.Other(s.SourceID)
	if !ok || s.ActiveID != s.SourceID || target.ID != s.TargetID || s.PhaseSince.IsZero() || s.PhaseSince.After(now) || s.Pending.DispatchedAt.Before(s.PhaseSince) || s.Pending.DispatchedAt.After(now) {
		return reject()
	}
	if err = ctx.Err(); err != nil {
		return State{}, err
	}
	started := time.Now()
	v, err := e.Cloud.Snapshot(ctx, e.Config)
	if err != nil {
		return State{}, err
	}
	if err = v.Validate(e.Config); err != nil {
		return State{}, err
	}
	if !v.Servers[s.SourceID].Stopped() || !v.Servers[s.TargetID].Running() || !v.Servers[e.Config.ControllerServerID].Running() || !v.Address.Free() || !e.fresh(o, now.Add(time.Since(started))) || o.Private[s.SourceID] || !o.PrivateUnreachable[s.SourceID] || !o.Private[s.TargetID] {
		return reject()
	}
	if err = ctx.Err(); err != nil {
		return State{}, err
	}
	s.Phase = Attaching
	s.Armed = true
	s.Pending = nil
	s.PhaseSince = now
	s.Reason = ""
	s.SuspectSince = time.Time{}
	s.LastObservation = o.At
	if !dry {
		if err = e.Store.Save(s); err != nil {
			return State{}, err
		}
	}
	return s, nil
}
