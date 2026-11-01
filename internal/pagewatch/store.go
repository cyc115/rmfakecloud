package pagewatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	log "github.com/sirupsen/logrus"
)

// File names, under the user's data directory next to the auto-organize files
const (
	StateFileName = "page-watch.state.json"
	// BackupSuffix the previous good state, kept next to the state file
	BackupSuffix = ".bak"
	RunsFileName  = "page-watch.runs.json"
	// MaxRuns the run log keeps this many entries, newest first
	MaxRuns = 50
)

// RunEntry one finished run. Feed/Pipeline count the events planned (Pipeline
// includes the ones dry mode holds back), Accepted the ones rm-pages took
// (feed and pipeline), PipelineAccepted the pipeline ones among them.
type RunEntry struct {
	// At unix milliseconds
	At               int64  `json:"at"`
	Trigger          string `json:"trigger"`
	Feed             int    `json:"feed"`
	Pipeline         int    `json:"pipeline"`
	Accepted         int    `json:"accepted"`
	PipelineAccepted int    `json:"pipelineAccepted"`
	Skipped          int    `json:"skipped"`
	Mode             string `json:"mode"`
	Error            string `json:"error,omitempty"`
}

// Store reads and writes the page watch's two per-user files. The service is
// their only writer, one run at a time per user.
type Store struct {
	userPath func(uid string) string
	mu       sync.Mutex
}

// NewStore a store rooted at userPath(uid)
func NewStore(userPath func(uid string) string) *Store {
	return &Store{userPath: userPath}
}

func (s *Store) statePath(uid string) string { return filepath.Join(s.userPath(uid), StateFileName) }
func (s *Store) runsPath(uid string) string  { return filepath.Join(s.userPath(uid), RunsFileName) }

// LoadState the baselines; an absent file is an empty state. A state file
// that does not parse falls back to the .bak (the previous good state), with
// an Error; the events since then are re-sent under the same trace ids and
// rm-pages recognises them. Only when both are unreadable is it an error.
func (s *Store) LoadState(uid string) (*State, error) {
	path := s.statePath(uid)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return nil, err
	}
	st, perr := parseState(b)
	if perr == nil {
		return st, nil
	}
	perr = fmt.Errorf("cannot parse %s: %w", StateFileName, perr)
	bb, err := os.ReadFile(path + BackupSuffix)
	if err != nil {
		return nil, fmt.Errorf("%w (no usable %s%s: %v)", perr, StateFileName, BackupSuffix, err)
	}
	bak, err := parseState(bb)
	if err != nil {
		return nil, fmt.Errorf("%w (%s%s does not parse either: %v)", perr, StateFileName, BackupSuffix, err)
	}
	log.WithFields(log.Fields{"stage": "detect", "user": uid}).Error(logPrefix, perr, "; using ", StateFileName, BackupSuffix)
	return bak, nil
}

func parseState(b []byte) (*State, error) {
	st := &State{}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	return st.Clone(), nil // Clone fills nil maps
}

// SaveState writes the baselines atomically; the state it replaces becomes
// the .bak when it parses (a corrupt file never overwrites a good backup)
func (s *Store) SaveState(uid string, st *State) error {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	path := s.statePath(uid)
	if cur, err := os.ReadFile(path); err == nil {
		if _, perr := parseState(cur); perr == nil {
			if err := writeAtomic(path+BackupSuffix, cur); err != nil {
				return fmt.Errorf("cannot keep %s%s: %w", StateFileName, BackupSuffix, err)
			}
		}
	}
	return writeAtomic(path, b)
}

// LoadRuns the run log, newest first; an absent file is empty
func (s *Store) LoadRuns(uid string) ([]RunEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadRunsLocked(uid)
}

func (s *Store) loadRunsLocked(uid string) ([]RunEntry, error) {
	b, err := os.ReadFile(s.runsPath(uid))
	if errors.Is(err, os.ErrNotExist) {
		return []RunEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []RunEntry
	if err := json.Unmarshal(b, &runs); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", RunsFileName, err)
	}
	return runs, nil
}

// AppendRun adds a run at the front and keeps the newest MaxRuns
func (s *Store) AppendRun(uid string, r RunEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs, err := s.loadRunsLocked(uid)
	if err != nil {
		// an unreadable log must not stop new runs from being recorded
		runs = nil
	}
	runs = append([]RunEntry{r}, runs...)
	if len(runs) > MaxRuns {
		runs = runs[:MaxRuns]
	}
	b, err := json.MarshalIndent(runs, "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(s.runsPath(uid), b)
}

// writeAtomic temp file in the same directory, fsync, rename (as autoorganize does)
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
