package autoorganize

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// captureLogs records log entries at Info and above for the test
func captureLogs(t *testing.T) *logtest.Hook {
	t.Helper()
	level := log.GetLevel()
	log.SetLevel(log.InfoLevel)
	hook := logtest.NewGlobal()
	t.Cleanup(func() {
		log.SetLevel(level)
		log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	})
	return hook
}

func logLines(hook *logtest.Hook, level log.Level, contains string) []string {
	var out []string
	for _, e := range hook.AllEntries() {
		if e.Level == level && strings.Contains(e.Message, contains) {
			out = append(out, e.Message)
		}
	}
	return out
}

// storeWithV1 a store under a temp dir; uid's auto-organize.json holds v1
// (nil: no file). The v1 file's mtime is set in the past.
func storeWithV1(t *testing.T, v1 []byte, uids ...string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	for _, uid := range uids {
		if err := os.MkdirAll(filepath.Join(dir, uid), 0o700); err != nil {
			t.Fatal(err)
		}
		if v1 == nil {
			continue
		}
		p := filepath.Join(dir, uid, V1ConfigFileName)
		if err := os.WriteFile(p, v1, 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(func(uid string) string { return filepath.Join(dir, uid) }, 0), dir
}

func v1GoldenBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileState(t *testing.T, p string) ([]byte, time.Time) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return b, fi.ModTime()
}

func TestLoadConfigMigratesOnce(t *testing.T) {
	hook := captureLogs(t)
	store, dir := storeWithV1(t, v1GoldenBytes(t), "u")
	v1Path := filepath.Join(dir, "u", V1ConfigFileName)
	v1Bytes, v1Mtime := fileState(t, v1Path)

	cfg, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(dir, "u", ConfigFileName))
	if err != nil {
		t.Fatalf("%s not written: %v", ConfigFileName, err)
	}
	want, err := os.ReadFile("testdata/v2-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded(t, written), decoded(t, want)) {
		t.Fatalf("%s:\n%s\nwant (v2-golden.json):\n%s", ConfigFileName, written, want)
	}
	if b, mt := fileState(t, v1Path); !bytes.Equal(b, v1Bytes) || !mt.Equal(v1Mtime) {
		t.Fatalf("%s touched by the migration", V1ConfigFileName)
	}
	wantLine := "automation: u: migrated 2 rules from auto-organize.json (revision 10 → 11)"
	if got := logLines(hook, log.InfoLevel, "migrated"); !reflect.DeepEqual(got, []string{wantLine}) {
		t.Fatalf("migration log = %q, want %q", got, wantLine)
	}

	again, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, cfg) || again.Revision != 11 || len(again.Rules) != 2 {
		t.Fatalf("second load differs:\n%+v\n%+v", again, cfg)
	}
	if got := logLines(hook, log.InfoLevel, "migrated"); len(got) != 1 {
		t.Fatalf("the second load migrated again: %q", got)
	}
	if b, mt := fileState(t, v1Path); !bytes.Equal(b, v1Bytes) || !mt.Equal(v1Mtime) {
		t.Fatalf("%s touched by the second load", V1ConfigFileName)
	}
}

// TestLoadConfigConcurrentMigration (Review Focus 2): the sweep loop, the page
// watch loop and a UI GET arrive together on the first load. One migration,
// one write, the same config for everyone, no error. Run under -race.
func TestLoadConfigConcurrentMigration(t *testing.T) {
	hook := captureLogs(t)
	store, dir := storeWithV1(t, v1GoldenBytes(t), "u")
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	cfgs := make([]*automation.Config, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cfgs[i], errs[i] = store.LoadConfig("u")
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("load %d: %v", i, errs[i])
		}
		if !reflect.DeepEqual(cfgs[i], cfgs[0]) {
			t.Fatalf("load %d differs:\n%+v\n%+v", i, cfgs[i], cfgs[0])
		}
	}
	if got := logLines(hook, log.InfoLevel, "migrated"); len(got) != 1 {
		t.Fatalf("migrations = %q, want exactly one", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "u"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{V1ConfigFileName, ConfigFileName}) {
		t.Fatalf("files = %v", names)
	}
}

// TestLoadConfigMigrationFailureFailsClosed (Review Focus 3, P6): a corrupt
// auto-organize.json is an error, never the default config. The sweep stops
// before it reads or writes its state, so holds and created folders survive.
func TestLoadConfigMigrationFailureFailsClosed(t *testing.T) {
	_, storage := newHarness(t, uid, true)
	svc, _, _ := newService(t, storage)
	dir := storage.UserDataPath(uid)
	st := NewState()
	st.CreatedFolders["seed"] = "folder-1"
	st.SetHold(Hold{RuleID: "seed", DocID: "d", Source: "rr", MovedTo: "folder-1", Reason: "undo"})
	if err := svc.Store().SaveState(uid, st); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, StateFileName)
	before, _ := fileState(t, statePath)
	if err := os.WriteFile(filepath.Join(dir, V1ConfigFileName), []byte(`{"revision": 3, "rules": [{"id": `), 0o600); err != nil {
		t.Fatal(err)
	}

	if cfg, err := svc.Store().LoadConfig(uid); err == nil || !strings.Contains(err.Error(), V1ConfigFileName) {
		t.Fatalf("LoadConfig = %+v, %v; want a parse error naming %s", cfg, err, V1ConfigFileName)
	}
	if _, err := svc.RunNow(uid); err == nil {
		t.Fatal("RunNow swept with a corrupt config")
	}
	if _, err := svc.View(uid); err == nil {
		t.Fatal("View answered with a corrupt config")
	}
	if after, _ := fileState(t, statePath); !bytes.Equal(before, after) {
		t.Fatalf("state changed:\n%s\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); !os.IsNotExist(err) {
		t.Fatalf("%s written after a failed migration: %v", ConfigFileName, err)
	}
	// a migration error (not only a parse error) fails closed too
	if err := os.WriteFile(filepath.Join(dir, V1ConfigFileName), []byte(`{"rules": [{"id": "x", "action": {"type": "print"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().LoadConfig(uid); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("unmigratable action: %v", err)
	}
	if after, _ := fileState(t, statePath); !bytes.Equal(before, after) {
		t.Fatal("state changed")
	}
}

func TestLoadConfigRejectsCorruptOrOtherVersion(t *testing.T) {
	store, dir := storeWithV1(t, v1GoldenBytes(t), "u")
	for _, body := range []string{`{"version": 3, "rules": []}`, `{"rules": []}`, `{"version": 2, "rules": [`} {
		if err := os.WriteFile(filepath.Join(dir, "u", ConfigFileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg, err := store.LoadConfig("u"); err == nil {
			t.Errorf("%s loaded: %+v", body, cfg)
		}
	}
}

// TestLoadConfigLogsDisabledRules X3: a move rule v1 skipped as invalid
// migrates disabled, with one Warn per rule
func TestLoadConfigLogsDisabledRules(t *testing.T) {
	hook := captureLogs(t)
	v1 := `{"revision": 1, "settings": {"enabled": true, "mode": "live", "intervalMinutes": 15}, "rules": [
		{"id": "bad", "enabled": true, "scope": "pdf_epub", "source": {"id": "a"},
		 "trigger": {"type": "name_matches", "pattern": "/[/"}, "destination": {"id": "b"}, "action": {"type": "move"}}]}`
	store, _ := storeWithV1(t, []byte(v1), "u")
	cfg, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules[0].Enabled {
		t.Fatal("invalid v1 rule migrated enabled")
	}
	got := logLines(hook, log.WarnLevel, "disabled in migration")
	if len(got) != 1 || !strings.HasPrefix(got[0], "automation: u: rule 1 (bad) disabled in migration: ") {
		t.Fatalf("warn lines = %q", got)
	}
}

// TestSaveConfigNeverWritesV1 the server writes automation.json only: a save
// migrates an existing auto-organize.json first and leaves it byte-unchanged,
// and never creates one
func TestSaveConfigNeverWritesV1(t *testing.T) {
	store, dir := storeWithV1(t, v1GoldenBytes(t), "u")
	v1Path := filepath.Join(dir, "u", V1ConfigFileName)
	v1Bytes, v1Mtime := fileState(t, v1Path)

	cfg := &automation.Config{Revision: 11, Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}, Rules: []automation.Rule{seedRule()}}
	saved, err := store.SaveConfig("u", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 12 {
		t.Fatalf("revision = %d, want 12 (the migrated 11 + 1)", saved.Revision)
	}
	if b, mt := fileState(t, v1Path); !bytes.Equal(b, v1Bytes) || !mt.Equal(v1Mtime) {
		t.Fatalf("%s written by SaveConfig", V1ConfigFileName)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "u", ConfigFileName+backupSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if prev, err := parseConfig(bak); err != nil || prev.Revision != 11 {
		t.Fatalf(".bak = %+v, %v; want the migrated revision 11", prev, err)
	}
	// a stale revision (the v1 one) is refused, nothing written
	if _, err := store.SaveConfig("u", &automation.Config{Revision: 10, Settings: cfg.Settings}); err != ErrStaleRevision {
		t.Fatalf("stale save: %v", err)
	}

	fresh := &automation.Config{Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}, Rules: []automation.Rule{seedRule()}}
	if _, err := store.SaveConfig("fresh", fresh); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{V1ConfigFileName, V1ConfigFileName + backupSuffix} {
		if _, err := os.Stat(filepath.Join(dir, "fresh", name)); !os.IsNotExist(err) {
			t.Fatalf("%s created: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh", ConfigFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAllUsers(t *testing.T) {
	store, dir := storeWithV1(t, v1GoldenBytes(t), "admin", "1")
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken", V1ConfigFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := store.MigrateAll([]string{"admin", "1", "broken", "none"})
	if len(failed) != 1 || failed["broken"] == nil {
		t.Fatalf("failed = %v, want broken only", failed)
	}
	for _, uid := range []string{"admin", "1"} {
		b, err := os.ReadFile(filepath.Join(dir, uid, ConfigFileName))
		if err != nil {
			t.Fatalf("%s: %v", uid, err)
		}
		if cfg, err := parseConfig(b); err != nil || len(cfg.Rules) != 2 || cfg.Revision != 11 {
			t.Fatalf("%s: %+v %v", uid, cfg, err)
		}
	}
	// a user with no config keeps none: the default is not written
	if _, err := os.Stat(filepath.Join(dir, "none", ConfigFileName)); !os.IsNotExist(err) {
		t.Fatalf("default written for a user without config: %v", err)
	}
}
