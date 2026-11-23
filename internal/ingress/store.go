//go:build linux

package ingress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Publication struct {
	Generation string     `json:"generation"`
	Targets    []Identity `json:"targets"`
}
type PublishResult struct {
	AppliedEdges []string `json:"applied_edges"`
	PendingEdges []string `json:"pending_edges,omitempty"`
}
type State struct {
	Version            int           `json:"version"`
	MachineID          string        `json:"machine_id"`
	ScopeHash          string        `json:"scope_hash"`
	Phase              string        `json:"phase"`
	Targets            []Identity    `json:"targets"`
	Pending            *Publication  `json:"pending,omitempty"`
	Preparing          *Identity     `json:"preparing,omitempty"`
	LastAttempt        time.Time     `json:"last_attempt"`
	LastSuccess        time.Time     `json:"last_success"`
	LastError          string        `json:"last_error,omitempty"`
	ObservedCandidates int           `json:"observed_candidates"`
	PublicationResult  PublishResult `json:"publication_result"`
}
type Store struct {
	dir, machine, scope string
	lock                *os.File
}

func OpenStore(dir, machine, scope string) (*Store, error) {
	if !filepath.IsAbs(dir) || machine == "" || scope == "" {
		return nil, errors.New("absolute private state and fixed identity required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("state directory is not private/owned")
	}
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another ingress manager owns the state lock")
	}
	return &Store{dir: dir, machine: machine, scope: scope, lock: file}, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func hashState(st State) string {
	raw, _ := json.Marshal(st)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *Store) Load() (State, error) {
	var st State
	file, err := os.OpenFile(filepath.Join(s.dir, "state.json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return st, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return st, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return st, errors.New("state file is not private/owned")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return st, errors.New("invalid state size")
	}
	var envelope struct {
		State  State  `json:"state"`
		SHA256 string `json:"sha256"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return st, errors.New("invalid state envelope")
	}
	st = envelope.State
	if hashState(st) != envelope.SHA256 || st.Version != 1 || st.MachineID != s.machine || st.ScopeHash != s.scope || len(st.Targets) > 8 {
		return st, errors.New("corrupt, foreign or rebound ingress journal")
	}
	return st, nil
}
func AtomicJSON(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".clo-ingress-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(append(raw, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (s *Store) Save(st State) error {
	if st.Version != 1 || st.MachineID != s.machine || st.ScopeHash != s.scope || len(st.Targets) > 8 {
		return errors.New("refusing foreign state")
	}
	envelope := struct {
		State  State  `json:"state"`
		SHA256 string `json:"sha256"`
	}{st, hashState(st)}
	return AtomicJSON(filepath.Join(s.dir, "state.json"), envelope, 0600)
}
