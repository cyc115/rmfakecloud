package pagewatch

import (
	"os"
	"path/filepath"
	"testing"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(func(string) string { return dir }), filepath.Join(dir, StateFileName)
}

func stateWith(feed string) *State {
	st := NewState()
	st.Feed["d/p"] = feed
	return st
}

func TestSaveStateKeepsPreviousAsBackup(t *testing.T) {
	s, path := newTestStore(t)
	if err := s.SaveState(uid, stateWith("h1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Fatalf("backup after the first save: %v", err)
	}
	if err := s.SaveState(uid, stateWith("h2")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + BackupSuffix)
	if err != nil {
		t.Fatal(err)
	}
	bak, err := parseState(b)
	if err != nil || bak.Feed["d/p"] != "h1" {
		t.Fatalf("backup = %+v %v", bak, err)
	}
}

func TestCorruptStateFallsBackToBackup(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	s, path := newTestStore(t)
	s.SaveState(uid, stateWith("h1"))
	s.SaveState(uid, stateWith("h2"))
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := s.LoadState(uid)
	if err != nil || st.Feed["d/p"] != "h1" {
		t.Fatalf("state %+v err %v", st, err)
	}
	var errs int
	for _, en := range hook.AllEntries() {
		if en.Level == log.ErrorLevel && en.Data["stage"] == "detect" {
			errs++
		}
	}
	if errs != 1 {
		t.Fatalf("%d errors, want 1", errs)
	}
	// the corrupt file never replaces the good backup
	if err := s.SaveState(uid, stateWith("h3")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path + BackupSuffix)
	if bak, err := parseState(b); err != nil || bak.Feed["d/p"] != "h1" {
		t.Fatalf("backup = %+v %v", bak, err)
	}
}

func TestCorruptStateAndBackupIsAnError(t *testing.T) {
	s, path := newTestStore(t)
	os.WriteFile(path, []byte("{broken"), 0600)
	if _, err := s.LoadState(uid); err == nil {
		t.Fatal("no error without a backup")
	}
	os.WriteFile(path+BackupSuffix, []byte("nope"), 0600)
	if _, err := s.LoadState(uid); err == nil {
		t.Fatal("no error with a corrupt backup")
	}
}
