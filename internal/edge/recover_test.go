package edge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func acceptedAttachFixture(t *testing.T) (*Engine, *fakeCloud, time.Time, Observation) {
	e, f, now := fixture(t)
	s, _ := e.Store.Load()
	s.Phase = RecoveryRequired
	s.Armed = false
	s.SourceID = e.Config.Edges[0].ID
	s.TargetID = e.Config.Edges[1].ID
	s.PhaseSince = now.Add(-time.Minute)
	s.Pending = &Pending{Kind: "attach", Accepted: true, DispatchedAt: now.Add(-time.Minute)}
	s.Reason = "operator started source during accepted attach"
	if err := e.Store.Save(s); err != nil {
		t.Fatal(err)
	}
	src := f.view.Servers[s.SourceID]
	src.Status = "STOPPED"
	src.SwitchStatus = "OFF"
	f.view.Servers[s.SourceID] = src
	f.view.Address.Owner = s.TargetID
	return e, f, now, obs(e.Config, now, true, false, true)
}
func TestCompleteAcceptedAttachPersistsCompletionWithoutCloudWrites(t *testing.T) {
	e, f, now, o := acceptedAttachFixture(t)
	before, _ := e.Store.Load()
	s, err := e.CompleteAcceptedAttach(context.Background(), now, o, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != Completed || s.Armed || s.Pending != nil || s.ActiveID != before.TargetID || !s.LastCompleted.Equal(now) || s.SourceID != before.SourceID || s.TargetID != before.TargetID {
		t.Fatalf("bad completion: %+v", s)
	}
	got, _ := e.Store.Load()
	if !reflect.DeepEqual(got, s) {
		t.Fatal("completion not durable")
	}
	if len(f.calls) != 0 {
		t.Fatal("recovery must not send native mutations")
	}
	if _, err = e.CompleteAcceptedAttach(context.Background(), now, o, false); err == nil {
		t.Fatal("stable journal must not be recovered again")
	}
}
func TestCompleteAcceptedAttachDryRunPreservesJournal(t *testing.T) {
	e, f, now, o := acceptedAttachFixture(t)
	before, _ := e.Store.Load()
	after, err := e.CompleteAcceptedAttach(context.Background(), now, o, true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Phase != Completed {
		t.Fatal("dry run must evaluate same target state")
	}
	got, _ := e.Store.Load()
	if !reflect.DeepEqual(before, got) || len(f.calls) != 0 {
		t.Fatal("dry run mutated state")
	}
}
func TestCompleteAcceptedAttachRejectsUnsafeEvidence(t *testing.T) {
	for _, name := range []string{"unaccepted", "wrong-kind", "no-pending", "wrong-source", "wrong-target", "source-on", "source-half-off", "target-off", "controller-off", "wrong-owner", "stale", "future", "public-down", "target-down", "source-private-up", "cloud-error", "config-changed", "future-dispatch", "wrong-phase", "armed"} {
		t.Run(name, func(t *testing.T) {
			e, f, now, o := acceptedAttachFixture(t)
			s, _ := e.Store.Load()
			switch name {
			case "unaccepted":
				s.Pending.Accepted = false
			case "wrong-kind":
				s.Pending.Kind = "detach"
			case "no-pending":
				s.Pending = nil
			case "wrong-source":
				s.SourceID = s.TargetID
			case "wrong-target":
				s.TargetID = s.SourceID
			case "source-on":
				v := f.view.Servers[s.SourceID]
				v.Status = "ACTIVE"
				v.SwitchStatus = "ON"
				f.view.Servers[s.SourceID] = v
			case "source-half-off":
				v := f.view.Servers[s.SourceID]
				v.SwitchStatus = "ON"
				f.view.Servers[s.SourceID] = v
			case "target-off":
				v := f.view.Servers[s.TargetID]
				v.Status = "STOPPED"
				v.SwitchStatus = "OFF"
				f.view.Servers[s.TargetID] = v
			case "controller-off":
				v := f.view.Servers[e.Config.ControllerServerID]
				v.Status = "STOPPED"
				v.SwitchStatus = "OFF"
				f.view.Servers[v.ID] = v
			case "wrong-owner":
				f.view.Address.Owner = s.SourceID
			case "stale":
				o.At = now.Add(-time.Hour)
			case "future":
				o.At = now.Add(time.Hour)
			case "public-down":
				o.Public = false
			case "target-down":
				o.Private[s.TargetID] = false
			case "source-private-up":
				o.Private[s.SourceID] = true
			case "cloud-error":
				f.err = errors.New("provider unavailable")
			case "config-changed":
				e.Config.BandwidthMbps++
			case "future-dispatch":
				s.Pending.DispatchedAt = now.Add(time.Hour)
			case "wrong-phase":
				s.Phase = Attaching
				s.Armed = true
			case "armed":
				s.Armed = true
			}
			if err := e.Store.Save(s); err != nil {
				t.Fatal(err)
			}
			before, _ := e.Store.Load()
			if _, err := e.CompleteAcceptedAttach(context.Background(), now, o, false); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			got, _ := e.Store.Load()
			if !reflect.DeepEqual(before, got) || len(f.calls) != 0 {
				t.Fatal("rejection mutated journal/cloud")
			}
		})
	}
}

func TestCompleteAcceptedAttachCancellationAndDiskFailure(t *testing.T) {
	for _, disk := range []bool{false, true} {
		e, f, now, o := acceptedAttachFixture(t)
		before, _ := e.Store.Load()
		ctx := context.Background()
		if disk {
			e.Store = failingJournal{e.Store}
		} else {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		if _, err := e.CompleteAcceptedAttach(ctx, now, o, false); err == nil {
			t.Fatal("cancelled/undurable completion accepted")
		}
		got, _ := e.Store.Load()
		if !reflect.DeepEqual(before, got) || len(f.calls) != 0 {
			t.Fatal("failed completion changed state")
		}
	}
}
