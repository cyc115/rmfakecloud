package autoorganize

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File names, under DataDir/users/<uid>/, deliberately outside sync/
const (
	ConfigFileName = "auto-organize.json"
	StateFileName  = "auto-organize.state.json"
	backupSuffix   = ".bak"
)

// ErrStaleRevision a PUT carried a revision that is not the current one
var ErrStaleRevision = errors.New("config was changed elsewhere (stale revision)")

// Store reads and writes the two per-user files. The config is owned by the
// UI (the sweeper only reads it); the state is owned by the sweeper.
type Store struct {
	userPath        func(uid string) string
	defaultInterval int

	mu      sync.Mutex
	cfgLock map[string]*sync.Mutex
}

// NewStore a store rooted at userPath(uid)
func NewStore(userPath func(uid string) string, defaultInterval int) *Store {
	return &Store{
		userPath:        userPath,
		defaultInterval: defaultInterval,
		cfgLock:         map[string]*sync.Mutex{},
	}
}

func (s *Store) configPath(uid string) string {
	return filepath.Join(s.userPath(uid), ConfigFileName)
}

func (s *Store) statePath(uid string) string {
	return filepath.Join(s.userPath(uid), StateFileName)
}

func (s *Store) lockFor(uid string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.cfgLock[uid]
	if !ok {
		l = &sync.Mutex{}
		s.cfgLock[uid] = l
	}
	return l
}

// LoadConfig the user's config, or the default when there is none yet
func (s *Store) LoadConfig(uid string) (*Config, error) {
	b, err := os.ReadFile(s.configPath(uid))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(s.defaultInterval), nil
	}
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", ConfigFileName, err)
	}
	if cfg.Rules == nil {
		cfg.Rules = []Rule{}
	}
	return cfg, nil
}

// SaveConfig validates and writes cfg if cfg.Revision equals the stored revision.
// The stored revision is incremented; the previous file is kept as one .bak.
func (s *Store) SaveConfig(uid string, cfg *Config) (*Config, error) {
	l := s.lockFor(uid)
	l.Lock()
	defer l.Unlock()

	current, err := s.LoadConfig(uid)
	if err != nil {
		return nil, err
	}
	if cfg.Revision != current.Revision {
		return current, ErrStaleRevision
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Revision = current.Revision + 1

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	path := s.configPath(uid)
	if old, err := os.ReadFile(path); err == nil {
		if err := writeAtomic(path+backupSuffix, old); err != nil {
			return nil, err
		}
	}
	if err := writeAtomic(path, b); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadState the sweeper state; an absent file is an empty state
func (s *Store) LoadState(uid string) (*State, error) {
	st := NewState()
	b, err := os.ReadFile(s.statePath(uid))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", StateFileName, err)
	}
	st.init()
	return st, nil
}

// SaveState writes the state atomically. Only the sweeper calls it.
func (s *Store) SaveState(uid string, st *State) error {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(s.statePath(uid), b)
}

// writeAtomic temp file in the same directory, fsync, rename
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
