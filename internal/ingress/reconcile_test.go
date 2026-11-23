package ingress

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeRuntime struct {
	snapshot                                   Snapshot
	discoveryError, prepareError, publishError error
	ready                                      bool
	actions                                    []string
	publications                               []Publication
	failFirstPublish                           bool
	prepareFn                                  func(Candidate) error
	readyFn                                    func(Candidate) (bool, error)
	publishFn                                  func(Publication) (PublishResult, error)
	exports                                    []State
}

func (f *fakeRuntime) Discover(context.Context) (Snapshot, error) {
	return f.snapshot, f.discoveryError
}
func (f *fakeRuntime) Prepare(_ context.Context, c Candidate) error {
	f.actions = append(f.actions, "prepare:"+c.Name)
	if f.prepareFn != nil {
		return f.prepareFn(c)
	}
	return f.prepareError
}
func (f *fakeRuntime) SetReady(_ context.Context, c Candidate) error {
	f.actions = append(f.actions, "label:"+c.Name)
	return nil
}
func (f *fakeRuntime) BackendReady(_ context.Context, c Candidate) (bool, error) {
	f.actions = append(f.actions, "probe:"+c.Name)
	if f.readyFn != nil {
		return f.readyFn(c)
	}
	return f.ready, nil
}
func (f *fakeRuntime) Publish(_ context.Context, p Publication) (PublishResult, error) {
	f.actions = append(f.actions, "publish")
	f.publications = append(f.publications, p)
	if f.publishFn != nil {
		return f.publishFn(p)
	}
	if f.failFirstPublish {
		f.failFirstPublish = false
		return PublishResult{}, errors.New("SSH reply lost")
	}
	return PublishResult{AppliedEdges: []string{"a", "b"}}, f.publishError
}
func (f *fakeRuntime) Export(_ context.Context, s State) error {
	f.actions = append(f.actions, "export")
	f.exports = append(f.exports, s)
	return nil
}
func newEngine(t *testing.T) (*Engine, *fakeRuntime, *Store) {
	t.Helper()
	c, s := fixture(t)
	store, err := OpenStore(filepath.Join(t.TempDir(), "manager"), "fixed-host", c.ScopeHash())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	r := &fakeRuntime{snapshot: s, ready: true}
	return &Engine{Config: c, Store: store, Runtime: r}, r, store
}
func TestPrepareAndPodReadinessGateAllPublication(t *testing.T) {
	e, r, _ := newEngine(t)
	r.prepareError = errors.New("nft rejected")
	if e.Tick(context.Background(), time.Now()) == nil {
		t.Fatal("preparation failure hidden")
	}
	if !reflect.DeepEqual(r.actions, []string{"prepare:worker-a"}) {
		t.Fatalf("unsafe side effect order: %v", r.actions)
	}
	e, r, _ = newEngine(t)
	r.ready = false
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, publication := range r.publications {
		if len(publication.Targets) > 0 {
			t.Fatal("unready Traefik published")
		}
	}
	e, r, s := newEngine(t)
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.actions, []string{"prepare:worker-a", "label:worker-a", "probe:worker-a", "publish", "export"}) {
		t.Fatal(r.actions)
	}
	st, err := s.Load()
	if err != nil || len(st.Targets) != 1 || st.Targets[0].Name != "worker-a" || st.Pending != nil {
		t.Fatalf("unaccepted state: %+v %v", st, err)
	}
}
func TestLostPublicationResponseResumesSameDurableGeneration(t *testing.T) {
	e, r, s := newEngine(t)
	r.failFirstPublish = true
	if e.Tick(context.Background(), time.Now()) == nil {
		t.Fatal("ambiguous publication hidden")
	}
	st, _ := s.Load()
	if st.Pending == nil || len(st.Targets) != 0 {
		t.Fatal("pending publication lost")
	}
	gen := st.Pending.Generation
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(r.publications) != 2 || r.publications[1].Generation != gen {
		t.Fatal("unknown result blindly replaced instead of reconciled")
	}
	st, _ = s.Load()
	if st.Pending != nil || len(st.Targets) != 1 {
		t.Fatal("publication not accepted")
	}
	if st.Generation != gen {
		t.Fatal("accepted live generation not retained for monitoring")
	}
}
func TestRemovedUIDWithdrawnBeforeReplacementLabel(t *testing.T) {
	e, r, _ := newEngine(t)
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	r.actions = nil
	r.snapshot.Candidates = nil
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	last := r.publications[len(r.publications)-1]
	if len(last.Targets) != 0 || len(r.actions) == 0 || r.actions[0] != "publish" {
		t.Fatalf("obsolete IP not withdrawn first: %v", r.actions)
	}
}
func TestDiscoveryFailurePreservesAcceptedTargetsAndObserveOnlyDoesNotMutate(t *testing.T) {
	e, r, s := newEngine(t)
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	r.actions = nil
	r.discoveryError = errors.New("API unavailable")
	if e.Tick(context.Background(), time.Now()) == nil {
		t.Fatal("source failure hidden")
	}
	if len(r.actions) != 0 {
		t.Fatal("mutation during discovery failure")
	}
	st, _ := s.Load()
	if len(st.Targets) != 1 {
		t.Fatal("accepted service discarded on source error")
	}
	e, r, _ = newEngine(t)
	e.Config.Automatic = false
	if err := e.Tick(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(r.actions) != 0 {
		t.Fatal("observe-only mutated external state")
	}
}
