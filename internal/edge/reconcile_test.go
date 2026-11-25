package edge

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeCloud struct {
	view        View
	calls       []string
	fail, delay bool
	err         error
}

func (f *fakeCloud) Snapshot(context.Context, Config) (View, error) { return f.view, f.err }
func (f *fakeCloud) Stop(_ context.Context, id string) error {
	f.calls = append(f.calls, "stop:"+id)
	if !f.delay {
		s := f.view.Servers[id]
		s.Status = "STOPPED"
		s.SwitchStatus = "OFF"
		f.view.Servers[id] = s
	}
	if f.fail {
		return errors.New("lost native response")
	}
	return nil
}
func (f *fakeCloud) Detach(_ context.Context, id string) error {
	f.calls = append(f.calls, "detach:"+id)
	if !f.delay {
		f.view.Address.Status = "DOWN"
		f.view.Address.Type = "FREE"
		f.view.Address.Owner = ""
		f.view.Address.Entity = ""
	}
	if f.fail {
		return errors.New("lost native response")
	}
	return nil
}
func (f *fakeCloud) Attach(_ context.Context, id, target string, bw int) error {
	f.calls = append(f.calls, "attach:"+id+":"+target)
	if !f.delay {
		f.view.Address.Status = "ACTIVE"
		f.view.Address.Type = "FIXED"
		f.view.Address.Owner = target
		f.view.Address.Entity = "server"
		f.view.Address.Bandwidth = bw
	}
	if f.fail {
		return errors.New("lost native response")
	}
	return nil
}
func fixture(t *testing.T) (*Engine, *fakeCloud, time.Time) {
	t.Helper()
	c := testConfig()
	c.Automatic = true
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	s, e := OpenStore(filepath.Join(t.TempDir(), "state"), "host")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	f := &fakeCloud{view: View{Servers: map[string]Server{}, Address: Address{ID: c.AddressID, IP: c.PublicIP, Status: "ACTIVE", Type: "FIXED", Entity: "server", Owner: c.Edges[0].ID, External: true, Version: 4, Bandwidth: 100}}}
	for _, id := range []string{c.ControllerServerID, c.Edges[0].ID, c.Edges[1].ID} {
		f.view.Servers[id] = Server{ID: id, ProjectID: c.ProjectID, Status: "ACTIVE", SwitchStatus: "ON"}
	}
	eng := &Engine{Config: c, Store: s, Cloud: f}
	now := time.Unix(1000, 0).UTC()
	if e = eng.Initialize(context.Background(), "host"); e != nil {
		t.Fatal(e)
	}
	if e = eng.Arm(context.Background(), now, obs(c, now, true, true, true)); e != nil {
		t.Fatal(e)
	}
	return eng, f, now
}
func obs(c Config, at time.Time, p, a, b bool) Observation {
	return Observation{At: at, Public: p, Private: map[string]bool{c.Edges[0].ID: a, c.Edges[1].ID: b}, PrivateUnreachable: map[string]bool{c.Edges[0].ID: !a, c.Edges[1].ID: !b}}
}
func tick(t *testing.T, e *Engine, at time.Time, o Observation) State {
	t.Helper()
	if err := e.Tick(context.Background(), at, o); err != nil {
		t.Fatal(err)
	}
	s, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func toStopping(t *testing.T, e *Engine, start time.Time) time.Time {
	t.Helper()
	for i := 0; i <= 30; i += 5 {
		now := start.Add(time.Duration(i) * time.Second)
		tick(t, e, now, obs(e.Config, now, false, false, true))
	}
	s, _ := e.Store.Load()
	if s.Phase != Stopping {
		t.Fatalf("did not suspect: %+v", s)
	}
	return start.Add(35 * time.Second)
}

func TestSharedOrSinglePathFailureNeverFences(t *testing.T) {
	for _, flags := range [][3]bool{{true, true, true}, {false, true, true}, {true, false, true}, {false, false, false}} {
		e, f, start := fixture(t)
		for i := 0; i < 120; i += 5 {
			at := start.Add(time.Duration(i) * time.Second)
			tick(t, e, at, obs(e.Config, at, flags[0], flags[1], flags[2]))
		}
		if len(f.calls) != 0 {
			t.Fatalf("healthy/ambiguous path triggered native calls: %v", f.calls)
		}
	}
}
func TestSuspectRequiresContinuousFreshObservations(t *testing.T) {
	for _, gap := range []bool{false, true} {
		e, f, start := fixture(t)
		for i := 0; i <= 20; i += 5 {
			at := start.Add(time.Duration(i) * time.Second)
			tick(t, e, at, obs(e.Config, at, false, false, true))
		}
		at := start.Add(25 * time.Second)
		if gap {
			at = start.Add(60 * time.Second)
		}
		tick(t, e, at, obs(e.Config, at, !gap, !gap, true))
		for i := 5; i <= 25; i += 5 {
			n := at.Add(time.Duration(i) * time.Second)
			tick(t, e, n, obs(e.Config, n, false, false, true))
		}
		if len(f.calls) != 0 {
			t.Fatal("suspicion survived a healthy/gapped observation")
		}
	}
}
func TestFailoverFencesThenMovesExactIPAndDisarms(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	for i := 0; i < 8; i++ {
		n := at.Add(time.Duration(i*5) * time.Second)
		tick(t, e, n, obs(e.Config, n, i >= 5, false, true))
	}
	st, _ := e.Store.Load()
	if st.Phase != Completed || st.Armed || st.ActiveID != e.Config.Edges[1].ID || !f.view.Address.On(e.Config.Edges[1].ID) || !f.view.Servers[e.Config.Edges[0].ID].Stopped() {
		t.Fatalf("bad final state: %+v %+v", st, f.view)
	}
	want := []string{"stop:" + e.Config.Edges[0].ID, "detach:" + e.Config.AddressID, "attach:" + e.Config.AddressID + ":" + e.Config.Edges[1].ID}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("unsafe native sequence %v", f.calls)
	}
	before := len(f.calls)
	n := at.Add(time.Hour)
	tick(t, e, n, obs(e.Config, n, false, true, false))
	if len(f.calls) != before {
		t.Fatal("automatic failback")
	}
}
func TestStopOffDuringStoppingDoesNotPermitDetach(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	f.delay = true
	tick(t, e, at, obs(e.Config, at, false, false, true))
	s := f.view.Servers[e.Config.Edges[0].ID]
	s.Status = "STOPPING"
	s.SwitchStatus = "OFF"
	f.view.Servers[s.ID] = s
	for i := 5; i < 60; i += 5 {
		n := at.Add(time.Duration(i) * time.Second)
		tick(t, e, n, obs(e.Config, n, false, false, true))
	}
	if len(f.calls) != 1 || !f.view.Address.On(e.Config.Edges[0].ID) {
		t.Fatalf("OFF-only caused move: %v", f.calls)
	}
}
func TestUncertainNativeResultBlocksEvenMatchingLaterState(t *testing.T) {
	for _, kind := range []string{"stop", "detach", "attach"} {
		t.Run(kind, func(t *testing.T) {
			e, f, start := fixture(t)
			at := toStopping(t, e, start)
			for i := 0; i < 7; i++ {
				st, _ := e.Store.Load()
				if string(st.Phase) == map[string]string{"stop": "Stopping", "detach": "Detaching", "attach": "Attaching"}[kind] && st.Pending == nil {
					f.fail = true
				}
				n := at.Add(time.Duration(i*5) * time.Second)
				tick(t, e, n, obs(e.Config, n, false, false, true))
				if f.fail {
					break
				}
			}
			st, _ := e.Store.Load()
			if st.Phase != RecoveryRequired {
				t.Fatalf("uncertain %s advanced: %+v", kind, st)
			}
			before := len(f.calls)
			f.fail = false
			for i := 0; i < 5; i++ {
				n := at.Add(time.Duration((i+20)*5) * time.Second)
				tick(t, e, n, obs(e.Config, n, false, false, true))
			}
			if len(f.calls) != before {
				t.Fatal("replayed uncertain command")
			}
		})
	}
}
func TestRestartReconcilesOnlyAcceptedPending(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		e, f, start := fixture(t)
		at := toStopping(t, e, start)
		s, _ := e.Store.Load()
		s.Pending = &Pending{Kind: "stop", Accepted: accepted, DispatchedAt: at}
		e.Store.Save(s)
		v := f.view.Servers[e.Config.Edges[0].ID]
		v.Status = "STOPPED"
		v.SwitchStatus = "OFF"
		f.view.Servers[v.ID] = v
		restarted := &Engine{Config: e.Config, Store: e.Store, Cloud: f}
		st := tick(t, restarted, at, obs(e.Config, at, false, false, true))
		if accepted && st.Phase != Detaching {
			t.Fatalf("accepted stop not reconciled: %+v", st)
		}
		if !accepted && st.Phase != RecoveryRequired {
			t.Fatal("unknown dispatch resumed")
		}
		if len(f.calls) != 0 {
			t.Fatal("restart resent stop")
		}
	}
}

type failingJournal struct{ Journal }

func (f failingJournal) Save(State) error { return errors.New("fsync failed") }
func TestWriteBarrierFailurePreventsPOST(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	e.Store = failingJournal{e.Store}
	if e.Tick(context.Background(), at, obs(e.Config, at, false, false, true)) == nil {
		t.Fatal("ignored disk failure")
	}
	if len(f.calls) != 0 {
		t.Fatal("POST before durable journal")
	}
}
func TestAcceptedCommandDeadlineRequiresRecovery(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	f.delay = true
	tick(t, e, at, obs(e.Config, at, false, false, true))
	n := at.Add(181 * time.Second)
	st := tick(t, e, n, obs(e.Config, n, false, false, true))
	if st.Phase != RecoveryRequired || len(f.calls) != 1 {
		t.Fatal("repeated timed-out native job")
	}
}
func TestConfigChangeAndForeignOwnerCloseWriteGate(t *testing.T) {
	e, f, at := fixture(t)
	e.Config.PublicHost = "other.example"
	if e.Tick(context.Background(), at, obs(e.Config, at, false, false, true)) == nil {
		t.Fatal("changed config adopted")
	}
	if len(f.calls) != 0 {
		t.Fatal("changed config mutated")
	}
}

func TestForeignOwnerRequiresRecovery(t *testing.T) {
	e, f, at := fixture(t)
	f.view.Address.Owner = "foreign"
	st := tick(t, e, at, obs(e.Config, at, false, false, true))
	if st.Phase != RecoveryRequired || len(f.calls) != 0 {
		t.Fatal("foreign attachment accepted")
	}
}
func TestCloudOutageResetsSuspicion(t *testing.T) {
	e, f, start := fixture(t)
	for i := 0; i <= 20; i += 5 {
		at := start.Add(time.Duration(i) * time.Second)
		tick(t, e, at, obs(e.Config, at, false, false, true))
	}
	f.err = errors.New("API unavailable")
	at := start.Add(25 * time.Second)
	tick(t, e, at, obs(e.Config, at, false, false, true))
	f.err = nil
	for i := 30; i <= 50; i += 5 {
		at = start.Add(time.Duration(i) * time.Second)
		st := tick(t, e, at, obs(e.Config, at, false, false, true))
		if st.Phase != Watching {
			t.Fatal("cloud outage bridged suspect window")
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("outage caused mutation")
	}
}
func TestStaleObservationCannotTrigger(t *testing.T) {
	e, f, start := fixture(t)
	for i := 0; i <= 90; i += 5 {
		at := start.Add(time.Duration(i) * time.Second)
		tick(t, e, at, obs(e.Config, start, false, false, true))
	}
	if len(f.calls) != 0 {
		t.Fatal("stale/replayed health triggered fencing")
	}
}
func TestSourceRestartBeforeAttachRequiresRecovery(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	for i := 0; i < 4; i++ {
		n := at.Add(time.Duration(i*5) * time.Second)
		tick(t, e, n, obs(e.Config, n, false, false, true))
	}
	st, _ := e.Store.Load()
	if st.Phase != Attaching {
		t.Fatalf("fixture phase=%s", st.Phase)
	}
	s := f.view.Servers[e.Config.Edges[0].ID]
	s.Status = "ACTIVE"
	s.SwitchStatus = "ON"
	f.view.Servers[s.ID] = s
	n := at.Add(25 * time.Second)
	st = tick(t, e, n, obs(e.Config, n, false, false, true))
	if st.Phase != RecoveryRequired || !f.view.Address.Free() || len(f.calls) != 2 {
		t.Fatal("source restart allowed attach")
	}
}
func TestAcceptedAttachRestartDoesNotMoveBack(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	for i := 0; i < 5; i++ {
		n := at.Add(time.Duration(i*5) * time.Second)
		tick(t, e, n, obs(e.Config, n, false, false, true))
	}
	st, _ := e.Store.Load()
	if st.Phase != Attaching || st.Pending == nil || !st.Pending.Accepted {
		t.Fatalf("fixture=%+v", st)
	}
	restarted := &Engine{Config: e.Config, Cloud: f, Store: e.Store}
	n := at.Add(25 * time.Second)
	st = tick(t, restarted, n, obs(e.Config, n, true, false, true))
	if st.Phase != Verifying || len(f.calls) != 3 {
		t.Fatal("restarted attach replayed native mutation")
	}
	n = n.Add(5 * time.Second)
	st = tick(t, restarted, n, obs(e.Config, n, true, false, true))
	if st.Phase != Completed || st.Armed {
		t.Fatal("attach recovery failed")
	}
	if e.Arm(context.Background(), n, obs(e.Config, n, true, true, true)) == nil {
		t.Fatal("cooldown bypassed")
	}
}
func TestRecoveredActiveBeforeStopCancelsFence(t *testing.T) {
	e, f, start := fixture(t)
	at := toStopping(t, e, start)
	st := tick(t, e, at, obs(e.Config, at, true, true, true))
	if len(f.calls) != 0 || st.Phase != Watching {
		t.Fatal("fenced recovered active")
	}
}

func TestPoweredOffSourceCanFailOver(t *testing.T) {
	e, f, start := fixture(t)
	src := e.Config.Edges[0].ID
	s := f.view.Servers[src]
	s.Status = "STOPPED"
	s.SwitchStatus = "OFF"
	f.view.Servers[src] = s
	f.view.Address.Status = "DOWN"
	at := toStopping(t, e, start)
	for i := 0; i < 7; i++ {
		n := at.Add(time.Duration(i*5) * time.Second)
		tick(t, e, n, obs(e.Config, n, i >= 4, false, true))
	}
	st, _ := e.Store.Load()
	if st.Phase != Completed || !f.view.Address.On(e.Config.Edges[1].ID) || len(f.calls) != 2 {
		t.Fatalf("powered-off edge not recovered: %+v %v", st, f.calls)
	}
}
