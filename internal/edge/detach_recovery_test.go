package edge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func detachFixture(t *testing.T) (*Engine, *fakeCloud, time.Time, Observation) {
	e, f, now, o := acceptedAttachFixture(t)
	s, _ := e.Store.Load()
	s.Pending.Kind = "detach"
	e.Store.Save(s)
	f.view.Address.Type = "FREE"
	f.view.Address.Status = "DOWN"
	f.view.Address.Owner = ""
	f.view.Address.Entity = ""
	o.Public = false
	return e, f, now, o
}
func TestResumeAcceptedDetachDryAndDurable(t *testing.T) {
	e, f, now, o := detachFixture(t)
	before, _ := e.Store.Load()
	after, err := e.ResumeAcceptedDetach(context.Background(), now, o, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.Store.Load()
	if !reflect.DeepEqual(got, before) || len(f.calls) != 0 || after.Phase != Attaching || !after.Armed || after.Pending != nil {
		t.Fatal("bad dry run")
	}
	after, err = e.ResumeAcceptedDetach(context.Background(), now, o, false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = e.Store.Load()
	if !reflect.DeepEqual(got, after) || len(f.calls) != 0 || after.ActiveID != before.ActiveID || after.SourceID != before.SourceID || after.TargetID != before.TargetID || !after.LastCompleted.Equal(before.LastCompleted) {
		t.Fatal("resume must preserve workflow and send no API mutation")
	}
	if _, err = e.ResumeAcceptedDetach(context.Background(), now, o, false); err == nil {
		t.Fatal("replayed resume")
	}
}
func TestResumeAcceptedDetachRefusesUnsafeEvidence(t *testing.T) {
	for _, kind := range []string{"unknown", "attach", "absent", "armed", "phase", "future", "wrong-source", "source-on", "source-private-up", "target-off", "target-unhealthy", "controller-off", "fixed", "bandwidth", "stale", "cloud"} {
		t.Run(kind, func(t *testing.T) {
			e, f, now, o := detachFixture(t)
			s, _ := e.Store.Load()
			switch kind {
			case "unknown":
				s.Pending.Accepted = false
			case "attach":
				s.Pending.Kind = "attach"
			case "absent":
				s.Pending = nil
			case "armed":
				s.Armed = true
			case "phase":
				s.Phase = Completed
				s.Pending = nil
			case "future":
				s.Pending.DispatchedAt = now.Add(time.Second)
			case "wrong-source":
				s.ActiveID = s.TargetID
			case "source-on":
				x := f.view.Servers[s.SourceID]
				x.Status = "ACTIVE"
				x.SwitchStatus = "ON"
				f.view.Servers[s.SourceID] = x
			case "source-private-up":
				o.Private[s.SourceID] = true
			case "target-off":
				x := f.view.Servers[s.TargetID]
				x.Status = "STOPPED"
				x.SwitchStatus = "OFF"
				f.view.Servers[s.TargetID] = x
			case "target-unhealthy":
				o.Private[s.TargetID] = false
			case "controller-off":
				x := f.view.Servers[e.Config.ControllerServerID]
				x.Status = "STOPPED"
				f.view.Servers[e.Config.ControllerServerID] = x
			case "fixed":
				f.view.Address.Type = "FIXED"
				f.view.Address.Entity = "server"
				f.view.Address.Owner = s.TargetID
			case "bandwidth":
				f.view.Address.Bandwidth++
			case "stale":
				o.At = now.Add(-time.Hour)
			case "cloud":
				f.err = errors.New("unavailable")
			}
			if err := e.Store.Save(s); err != nil {
				t.Fatal(err)
			}
			if _, err := e.ResumeAcceptedDetach(context.Background(), now, o, false); err == nil {
				t.Fatal("unsafe resume")
			}
			got, _ := e.Store.Load()
			if !reflect.DeepEqual(got, s) || len(f.calls) != 0 {
				t.Fatal("rejection mutated state")
			}
		})
	}
}
