package autoorganize

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ddvk/rmfakecloud/internal/automation"
	log "github.com/sirupsen/logrus"
)

// File names, under DataDir/users/<uid>/, deliberately outside sync/
const (
	// ConfigFileName the v2 config (automation.json), the only config file
	// the server writes
	ConfigFileName = "automation.json"
	// V1ConfigFileName the v1 config, read once to migrate it; never written
	// by the server (only the rollback CLI writes it)
	V1ConfigFileName = "auto-organize.json"
	StateFileName    = "auto-organize.state.json"
	backupSuffix     = ".bak"
)

// ErrStaleRevision a PUT carried a revision that is not the current one
var ErrStaleRevision = errors.New("config was changed elsewhere (stale revision)")

// Store reads and writes the per-user files. The config is owned by the UI
// (the engines only read it); the state is owned by the sweeper.
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

func (s *Store) v1ConfigPath(uid string) string {
	return filepath.Join(s.userPath(uid), V1ConfigFileName)
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

// parseConfig an automation.json body. Only version 2 is read. It never runs
// Validate (X2): an invalid stored rule is the engines' "Invalid rule", not a
// load error.
func parseConfig(b []byte) (*automation.Config, error) {
	cfg := &automation.Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", ConfigFileName, err)
	}
	if cfg.Version != automation.Version {
		return nil, fmt.Errorf("%s: unsupported format version %d; this server reads version %d", ConfigFileName, cfg.Version, automation.Version)
	}
	if cfg.Rules == nil {
		cfg.Rules = []automation.Rule{}
	}
	return cfg, nil
}

// readConfig automation.json; an error wrapping os.ErrNotExist when absent
func (s *Store) readConfig(uid string) (*automation.Config, error) {
	b, err := os.ReadFile(s.configPath(uid))
	if err != nil {
		return nil, err
	}
	return parseConfig(b)
}

// LoadConfig the user's config. automation.json when it exists; else, under
// the user's config lock, auto-organize.json migrated once (written to
// automation.json); else the default. A config that exists but cannot be
// read, parsed or migrated is an error, never the default (P6): the default
// would let PruneHolds forget every hold and created folder.
func (s *Store) LoadConfig(uid string) (*automation.Config, error) {
	cfg, err := s.readConfig(uid)
	if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	l := s.lockFor(uid)
	l.Lock()
	defer l.Unlock()
	return s.loadOrMigrateLocked(uid)
}

// loadOrMigrateLocked LoadConfig's slow path; the caller holds lockFor(uid).
// It reads automation.json again, since another caller may have migrated
// while this one waited for the lock.
func (s *Store) loadOrMigrateLocked(uid string) (*automation.Config, error) {
	cfg, err := s.readConfig(uid)
	if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	b, err := os.ReadFile(s.v1ConfigPath(uid))
	if errors.Is(err, os.ErrNotExist) {
		return automation.DefaultConfig(s.defaultInterval), nil
	}
	if err != nil {
		return nil, err
	}
	v1 := &V1Config{}
	if err := json.Unmarshal(b, v1); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", V1ConfigFileName, err)
	}
	migrated, issues, err := MigrateV1(v1)
	if err != nil {
		return nil, fmt.Errorf("cannot migrate %s: %w", V1ConfigFileName, err)
	}
	for _, is := range issues {
		log.Warnf("automation: %s: rule %d (%s) disabled in migration: %s", uid, is.Index+1, is.RuleID, is.Reason)
	}
	out, err := json.MarshalIndent(migrated, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(s.configPath(uid), out); err != nil {
		return nil, err
	}
	log.Infof("automation: %s: migrated %d rules from %s (revision %d → %d)", uid, len(migrated.Rules), V1ConfigFileName, v1.Revision, migrated.Revision)
	// what a later load reads: every caller gets the same config
	return parseConfig(out)
}

// MigrateAll loads every user's config once, so each auto-organize.json is
// migrated at startup rather than by the first engine or UI request. It
// returns the users whose config failed to load or migrate.
func (s *Store) MigrateAll(uids []string) map[string]error {
	failed := map[string]error{}
	for _, uid := range uids {
		if _, err := s.LoadConfig(uid); err != nil {
			failed[uid] = err
		}
	}
	return failed
}

// SaveConfig normalizes, validates and writes cfg to automation.json if
// cfg.Revision equals the stored revision. The stored revision is
// incremented; the previous file is kept as one .bak. auto-organize.json is
// never written.
func (s *Store) SaveConfig(uid string, cfg *automation.Config) (*automation.Config, error) {
	l := s.lockFor(uid)
	l.Lock()
	defer l.Unlock()

	// not LoadConfig: it would take the lock this call holds
	current, err := s.loadOrMigrateLocked(uid)
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
