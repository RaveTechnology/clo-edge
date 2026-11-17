package edge

import (
	"context"
	"errors"
	"os"
	"time"
)

type Journal interface {
	Load() (State, error)
	Save(State) error
}
type Engine struct {
	Config Config
	Store  Journal
	Cloud  Provider
}

func (e *Engine) Initialize(ctx context.Context, machine string) error {
	if _, err := e.Store.Load(); !os.IsNotExist(err) {
		return errors.New("initialization requires missing state; existing/corrupt state is never replaced")
	}
	v, err := e.Cloud.Snapshot(ctx, e.Config)
	if err != nil {
		return err
	}
	if err = v.Validate(e.Config); err != nil {
		return err
	}
	if !v.Address.On(v.Address.Owner) {
		return errors.New("initialization requires an active IPv4 on a configured edge")
	}
	return e.Store.Save(State{Version: 1, MachineID: machine, ConfigHash: e.Config.Hash(), Phase: Watching, ActiveID: v.Address.Owner})
}
func (e *Engine) load() (State, error) {
	s, err := e.Store.Load()
	if err != nil {
		return s, err
	}
	if s.ConfigHash != e.Config.Hash() {
		return s, errors.New("configuration differs from commissioned journal")
	}
	if _, ok := e.Config.Other(s.ActiveID); !ok {
		return s, errors.New("journal active edge is not configured")
	}
	return s, nil
}
func (e *Engine) fresh(o Observation, now time.Time) bool {
	_, a := o.Private[e.Config.Edges[0].ID]
	_, b := o.Private[e.Config.Edges[1].ID]
	return a && b && !o.At.IsZero() && !o.At.After(now) && now.Sub(o.At) <= time.Duration(2*e.Config.PollSeconds+e.Config.ProbeSeconds)*time.Second
}
func (e *Engine) Arm(ctx context.Context, now time.Time, o Observation) error {
	s, err := e.load()
	if err != nil {
		return err
	}
	if !e.Config.Automatic {
		return errors.New("automatic operation is disabled in configuration")
	}
	if (s.Phase != Watching && s.Phase != Completed) || s.Pending != nil {
		return errors.New("incomplete/recovery operation cannot be armed")
	}
	if !e.fresh(o, now) || !o.Public || !o.Private[e.Config.Edges[0].ID] || !o.Private[e.Config.Edges[1].ID] {
		return errors.New("arming requires fresh public and both private health checks")
	}
	if !s.LastCompleted.IsZero() && now.Sub(s.LastCompleted) < time.Duration(e.Config.CooldownSeconds)*time.Second {
		return errors.New("post-failover cooldown has not elapsed")
	}
	v, err := e.Cloud.Snapshot(ctx, e.Config)
	if err != nil {
		return err
	}
	if err = v.Validate(e.Config); err != nil {
		return err
	}
	if !v.Address.On(s.ActiveID) {
		return errors.New("address owner differs from journal")
	}
	for _, id := range []string{e.Config.ControllerServerID, e.Config.Edges[0].ID, e.Config.Edges[1].ID} {
		if !v.Servers[id].Running() {
			return errors.New("arming requires all dedicated VMs running")
		}
	}
	s.Phase = Watching
	s.Armed = true
	s.SourceID = ""
	s.TargetID = ""
	s.SuspectSince = time.Time{}
	s.LastObservation = time.Time{}
	s.Reason = ""
	s.PhaseSince = now
	return e.Store.Save(s)
}
func (e *Engine) recover(s State, reason string) error {
	s.Phase = RecoveryRequired
	s.Armed = false
	s.Reason = reason
	s.SuspectSince = time.Time{}
	return e.Store.Save(s)
}
func (e *Engine) transition(s State, p Phase, now time.Time) error {
	s.Phase = p
	s.PhaseSince = now
	s.Pending = nil
	s.Reason = ""
	return e.Store.Save(s)
}
func (e *Engine) dispatch(ctx context.Context, s State, kind string, now time.Time) error {
	s.Pending = &Pending{Kind: kind, DispatchedAt: now}
	if err := e.Store.Save(s); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	switch kind {
	case "stop":
		err = e.Cloud.Stop(ctx, s.SourceID)
	case "detach":
		err = e.Cloud.Detach(ctx, e.Config.AddressID)
	case "attach":
		err = e.Cloud.Attach(ctx, e.Config.AddressID, s.TargetID, e.Config.BandwidthMbps)
	default:
		return errors.New("unknown command")
	}
	if err != nil {
		return e.recover(s, "native "+kind+" outcome uncertain; inspect and settle provider jobs manually")
	}
	s.Pending.Accepted = true
	return e.Store.Save(s)
}
func (e *Engine) Tick(ctx context.Context, now time.Time, o Observation) error {
	start := time.Now()
	s, err := e.load()
	if err != nil {
		return err
	}
	if s.Phase == RecoveryRequired {
		return nil
	}
	if s.Pending != nil && !s.Pending.Accepted {
		return e.recover(s, "unacknowledged dispatch; no automatic replay or follow-up")
	}
	moving := s.Phase != Watching && s.Phase != Completed
	if moving {
		target, ok := e.Config.Other(s.SourceID)
		expected := map[Phase]string{Stopping: "stop", Detaching: "detach", Attaching: "attach", Verifying: ""}[s.Phase]
		if !ok || target.ID != s.TargetID || s.SourceID != s.ActiveID || !s.Armed || s.PhaseSince.IsZero() || s.PhaseSince.After(now) || (s.Pending != nil && s.Pending.Kind != expected) {
			return e.recover(s, "invalid workflow journal")
		}
		if now.Sub(s.PhaseSince) > time.Duration(e.Config.ConvergeSeconds)*time.Second {
			return e.recover(s, "native convergence deadline exceeded; settle pending job manually")
		}
	}
	v, err := e.Cloud.Snapshot(ctx, e.Config)
	now = now.Add(time.Since(start))
	if err != nil {
		s.SuspectSince = time.Time{}
		s.LastObservation = time.Time{}
		s.Reason = "cloud state unavailable; no mutation permitted"
		return e.Store.Save(s)
	}
	if err = v.Validate(e.Config); err != nil {
		return e.recover(s, "cloud resource identity or ownership changed")
	}
	if !v.Servers[e.Config.ControllerServerID].Running() {
		return e.recover(s, "controller VM is not active")
	}
	if s.Phase == Watching || s.Phase == Completed {
		if !v.Address.On(s.ActiveID) {
			return e.recover(s, "IPv4 ownership changed outside controller")
		}
		if s.Pending != nil {
			return e.recover(s, "unexpected pending command")
		}
		other, _ := e.Config.Other(s.ActiveID)
		fresh := e.fresh(o, now) && o.At.After(s.LastObservation)
		continuous := s.LastObservation.IsZero() || o.At.Sub(s.LastObservation) <= time.Duration(2*e.Config.PollSeconds+e.Config.ProbeSeconds)*time.Second
		bad := s.Phase == Watching && s.Armed && e.Config.Automatic && fresh && !o.Public && !o.Private[s.ActiveID] && o.Private[other.ID] && v.Servers[other.ID].Running()
		if !continuous {
			s.SuspectSince = time.Time{}
		}
		if !bad {
			s.SuspectSince = time.Time{}
		} else if s.SuspectSince.IsZero() {
			s.SuspectSince = o.At
		}
		if fresh {
			s.LastObservation = o.At
		} else {
			s.LastObservation = time.Time{}
		}
		s.Reason = ""
		if bad && o.At.Sub(s.SuspectSince) >= time.Duration(e.Config.SuspectSeconds)*time.Second && (s.LastCompleted.IsZero() || now.Sub(s.LastCompleted) >= time.Duration(e.Config.CooldownSeconds)*time.Second) {
			s.SourceID = s.ActiveID
			s.TargetID = other.ID
			s.SuspectSince = time.Time{}
			return e.transition(s, Stopping, now)
		}
		return e.Store.Save(s)
	}
	source := v.Servers[s.SourceID]
	target := v.Servers[s.TargetID]
	switch s.Phase {
	case Stopping:
		if source.Stopped() {
			return e.transition(s, Detaching, now)
		}
		if s.Pending != nil {
			return nil
		}
		if !source.Running() {
			return nil
		} // A provider/operator shutdown may already be in progress.
		if !e.fresh(o, now) {
			return nil
		}
		if o.Public || o.Private[s.SourceID] {
			s.SuspectSince = time.Time{}
			return e.transition(s, Watching, now)
		}
		if !target.Running() || !o.Private[s.TargetID] {
			return e.recover(s, "standby no longer ready before fencing")
		}
		if !v.Address.On(s.SourceID) {
			return e.recover(s, "source no longer owns active IPv4 before fencing")
		}
		return e.dispatch(ctx, s, "stop", now)
	case Detaching, Attaching:
		if !source.Stopped() {
			return e.recover(s, "source is not terminal STOPPED/OFF")
		}
		if s.Pending != nil {
			if s.Phase == Detaching && v.Address.Free() {
				return e.transition(s, Attaching, now)
			}
			if s.Phase == Attaching && v.Address.On(s.TargetID) {
				return e.transition(s, Verifying, now)
			}
			if s.Phase == Detaching && v.Address.Owner != s.SourceID {
				return e.recover(s, "unexpected owner while detaching")
			}
			if s.Phase == Attaching && !v.Address.Free() && v.Address.Owner != s.TargetID {
				return e.recover(s, "unexpected owner while attaching")
			}
			return nil
		}
		if !e.fresh(o, now) || !target.Running() || !o.Private[s.TargetID] {
			return e.recover(s, "standby health unavailable before IPv4 mutation")
		}
		if s.Phase == Detaching {
			if v.Address.Owner != s.SourceID || v.Address.Type != "SERVER" || v.Address.Entity != "server" {
				return e.recover(s, "source IPv4 ownership changed before detach")
			}
			return e.dispatch(ctx, s, "detach", now)
		}
		if !v.Address.Free() {
			return e.recover(s, "IPv4 is not confirmed FREE/DOWN before attach")
		}
		return e.dispatch(ctx, s, "attach", now)
	case Verifying:
		if !source.Stopped() || !target.Running() || !v.Address.On(s.TargetID) {
			return e.recover(s, "final native state does not match failover")
		}
		if e.fresh(o, now) && o.Public && o.Private[s.TargetID] {
			s.ActiveID = s.TargetID
			s.Armed = false
			s.LastCompleted = now
			return e.transition(s, Completed, now)
		}
	}
	return nil
}
