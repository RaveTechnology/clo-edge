//go:build linux

package edge

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Phase string

const (
	Watching         Phase = "Watching"
	Stopping         Phase = "Stopping"
	Detaching        Phase = "Detaching"
	Attaching        Phase = "Attaching"
	Verifying        Phase = "Verifying"
	Completed        Phase = "Completed"
	RecoveryRequired Phase = "RecoveryRequired"
)

type Pending struct {
	Kind         string    `json:"kind"`
	Accepted     bool      `json:"accepted"`
	DispatchedAt time.Time `json:"dispatched_at"`
}
type State struct {
	Version         int       `json:"version"`
	MachineID       string    `json:"machine_id"`
	ConfigHash      string    `json:"config_hash"`
	Phase           Phase     `json:"phase"`
	Armed           bool      `json:"armed"`
	ActiveID        string    `json:"active_id"`
	SourceID        string    `json:"source_id,omitempty"`
	TargetID        string    `json:"target_id,omitempty"`
	SuspectSince    time.Time `json:"suspect_since,omitempty"`
	LastObservation time.Time `json:"last_observation,omitempty"`
	PhaseSince      time.Time `json:"phase_since,omitempty"`
	LastCompleted   time.Time `json:"last_completed,omitempty"`
	Pending         *Pending  `json:"pending,omitempty"`
	Reason          string    `json:"reason,omitempty"`
}
type Store struct {
	dir, machine string
	lock         *os.File
}

func OpenStore(dir, machine string) (*Store, error) {
	if !filepath.IsAbs(dir) || machine == "" {
		return nil, errors.New("absolute state directory and machine identity required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("state directory must be private and not a symlink")
	}
	f, e := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another controller holds the state lock")
	}
	return &Store{dir: dir, machine: machine, lock: f}, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func readState(dir string) (State, error) {
	var st State
	f, e := os.OpenFile(filepath.Join(dir, "state.json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return st, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return st, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return st, errors.New("unsafe state file")
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return st, e
	}
	if e = strictJSON(raw, &st); e != nil {
		return st, e
	}
	if e = st.validate(); e != nil {
		return st, e
	}
	return st, nil
}
func Inspect(dir string) (State, error) { return readState(dir) }
func (s *Store) Load() (State, error) {
	st, e := readState(s.dir)
	if e == nil && st.MachineID != s.machine {
		e = errors.New("journal belongs to a different host")
	}
	return st, e
}
func (st State) validate() error {
	if st.Version != 1 || st.MachineID == "" || st.ConfigHash == "" || st.ActiveID == "" {
		return errors.New("invalid state identity/version")
	}
	switch st.Phase {
	case Watching, Stopping, Detaching, Attaching, Verifying, Completed, RecoveryRequired:
	default:
		return errors.New("unknown state phase")
	}
	if st.Pending != nil {
		if st.Pending.DispatchedAt.IsZero() {
			return errors.New("pending command lacks dispatch time")
		}
		switch st.Pending.Kind {
		case "stop", "detach", "attach":
		default:
			return errors.New("unknown pending command")
		}
	}
	return nil
}
func (s *Store) Save(st State) error {
	if st.MachineID != s.machine {
		return errors.New("state host identity changed")
	}
	if e := st.validate(); e != nil {
		return e
	}
	raw, e := json.MarshalIndent(st, "", "  ")
	if e != nil {
		return e
	}
	raw = append(raw, '\n')
	f, e := os.CreateTemp(s.dir, ".state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, filepath.Join(s.dir, "state.json")); e != nil {
		return e
	}
	d, e := os.Open(s.dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
