package autoorganize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTriggerDefaultsWhenAbsent(t *testing.T) {
	var tr Trigger
	if err := json.Unmarshal([]byte(`{"type":"finished_reading"}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.SettleHours != 6 || !tr.MinAgeEnabled || !tr.Pinned {
		t.Errorf("defaults not applied: %+v", tr)
	}
	if err := json.Unmarshal([]byte(`{"type":"finished_reading","settleHours":0,"pinned":false}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.SettleHours != 0 || tr.Pinned {
		t.Errorf("explicit zero values must be kept: %+v", tr)
	}
	cfg := &Config{Rules: []Rule{{Trigger: Trigger{Type: TriggerFinishedReading}}, {Trigger: Trigger{Type: TriggerNotOpened, MinAgeEnabled: true}}}}
	cfg.Normalize()
	if cfg.Rules[0].Trigger.MinPages != 3 || cfg.Rules[0].Scope != ScopePdfEpub || cfg.Rules[0].ID == "" {
		t.Errorf("normalize: %+v", cfg.Rules[0])
	}
	if cfg.Rules[1].Trigger.Days != 30 || cfg.Rules[1].Trigger.MinAgeDays != 7 {
		t.Errorf("normalize not_opened: %+v", cfg.Rules[1].Trigger)
	}
	if cfg.Settings.Mode != ModeDry || cfg.Settings.IntervalMinutes != 15 {
		t.Errorf("settings defaults: %+v", cfg.Settings)
	}
}

func TestRuleValidation(t *testing.T) {
	base := func() Rule {
		r := seedRule()
		return r
	}
	cases := []struct {
		name   string
		mutate func(r *Rule)
		want   string
	}{
		{"seed rule is valid", func(r *Rule) {}, ""},
		{"destination equals source", func(r *Rule) {
			r.Destination = Destination{FolderRef: FolderRef{ID: "rr"}}
		}, "same as the source"},
		{"root to root", func(r *Rule) {
			r.Source = FolderRef{ID: RootID}
			r.Destination = Destination{FolderRef: FolderRef{ID: RootID}}
			r.Trigger = Trigger{Type: TriggerPinnedIs, Pinned: true}
			r.Scope = ScopeAll
		}, "same as the source"},
		{"root source is pickable", func(r *Rule) { r.Source = FolderRef{ID: RootID} }, ""},
		{"new subfolder needs a name", func(r *Rule) { r.Destination.CreateIfMissing.Name = "" }, "needs a name"},
		{"finished_reading locked to pdf/epub", func(r *Rule) { r.Scope = ScopeAll }, "only applies to PDF"},
		{"read_progress locked to pdf/epub", func(r *Rule) {
			r.Trigger = Trigger{Type: TriggerReadProgress, Percent: 80}
			r.Scope = ScopeNotebooks
		}, "only applies to PDF"},
		{"percent range", func(r *Rule) { r.Trigger = Trigger{Type: TriggerReadProgress, Percent: 101} }, "between 1 and 100"},
		{"min pages", func(r *Rule) { r.Trigger.MinPages = 0 }, "at least 1"},
		{"invalid regex", func(r *Rule) {
			r.Trigger = Trigger{Type: TriggerNameMatches, Pattern: "/[a-/"}
		}, "invalid regular expression"},
		{"empty pattern", func(r *Rule) { r.Trigger = Trigger{Type: TriggerNameMatches} }, "pattern cannot be empty"},
		{"empty tag", func(r *Rule) { r.Trigger = Trigger{Type: TriggerTagIs, Tag: "  "} }, "tag cannot be empty"},
		{"size_over_mb is not a trigger", func(r *Rule) { r.Trigger = Trigger{Type: "size_over_mb"} }, "unknown trigger"},
		{"copy is not an action", func(r *Rule) { r.Action = Action{Type: "copy"} }, "unsupported action"},
		{"trash as source", func(r *Rule) { r.Source = FolderRef{ID: "trash"} }, "trash"},
		{"days", func(r *Rule) { r.Trigger = Trigger{Type: TriggerNotModified} }, "days must be"},
		{"unknown scope", func(r *Rule) { r.Scope = "pdf" }, "unknown scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base()
			c.mutate(&r)
			err := r.Validate()
			if c.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
}

func TestSettingsValidation(t *testing.T) {
	cfg := &Config{Settings: Settings{Mode: ModeLive, IntervalMinutes: 7}}
	if err := cfg.Validate(); err == nil {
		t.Error("interval 7 must be rejected")
	}
	cfg = &Config{Settings: Settings{Mode: "maybe", IntervalMinutes: 15}}
	if err := cfg.Validate(); err == nil {
		t.Error("unknown mode must be rejected")
	}
	for _, m := range AllowedIntervals {
		cfg = &Config{Settings: Settings{Mode: ModeDry, IntervalMinutes: m}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("interval %d: %v", m, err)
		}
	}
	cfg = &Config{Settings: Settings{Mode: ModeDry, IntervalMinutes: 15}, Rules: []Rule{seedRule(), seedRule()}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate ids: %v", err)
	}
}

func TestCompilePattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"WSJ *", "WSJ Weekend 2026-10-18", true},
		{"WSJ *", "wsj weekend", true},
		{"WSJ *", "The WSJ Weekend", false},
		{"*report*", "Q3 Market Report final", true},
		{"Q? notes", "Q3 notes", true},
		{"Q? notes", "Q10 notes", false},
		{"a.b", "axb", false},
		{"a.b", "a.b", true},
		{"/^q[0-9] /", "Q3 Outlook", true},
		{"/outlook$/", "Q3 Outlook", true},
		{"/outlook$/", "Outlook Q3", false},
	}
	for _, c := range cases {
		re, err := CompilePattern(c.pattern)
		if err != nil {
			t.Fatalf("%q: %v", c.pattern, err)
		}
		if got := re.MatchString(c.name); got != c.want {
			t.Errorf("%q vs %q = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestNoAutomationTagNormalisation(t *testing.T) {
	for _, tag := range []string{"no automation", "No Automation", "NO AUTOMATION", "no-automation", "no_automation", "  no   automation ", "No-Automation"} {
		if !IsNoAutomationTag(tag) {
			t.Errorf("%q should be the opt-out tag", tag)
		}
	}
	for _, tag := range []string{"noautomation", "automation", "no automations", "no", ""} {
		if IsNoAutomationTag(tag) {
			t.Errorf("%q should not be the opt-out tag", tag)
		}
	}
}

func TestLoopWarnings(t *testing.T) {
	mk := func(id, from, to string, enabled bool) Rule {
		return Rule{ID: id, Enabled: enabled, Source: FolderRef{ID: from}, Destination: Destination{FolderRef: FolderRef{ID: to}}, Action: Action{Type: ActionMove}}
	}
	destOf := func(r *Rule) (string, bool) { return r.Destination.ID, true }

	cfg := &Config{Rules: []Rule{mk("a", "inbox", "news", true), mk("b", "inbox", "archive", true), mk("c", "archive", "inbox", true)}}
	got := LoopWarnings(cfg, destOf)
	want := map[string][]int{"b": {3}, "c": {2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("two rule loop: got %v want %v", got, want)
	}

	cfg = &Config{Rules: []Rule{mk("a", "x", "y", true), mk("b", "y", "z", true), mk("c", "z", "x", true)}}
	got = LoopWarnings(cfg, destOf)
	if len(got) != 3 || !reflect.DeepEqual(got["a"], []int{2, 3}) {
		t.Errorf("three rule loop: %v", got)
	}

	cfg = &Config{Rules: []Rule{mk("b", "inbox", "archive", true), mk("c", "archive", "inbox", false)}}
	if got := LoopWarnings(cfg, destOf); len(got) != 0 {
		t.Errorf("disabled rule must not warn: %v", got)
	}
}

func TestConfigAndStateAreSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(func(uid string) string { return filepath.Join(dir, uid) }, 0)

	cfg, err := store.LoadConfig("u")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 0 || !cfg.Settings.Enabled || cfg.Settings.Mode != ModeDry || cfg.Settings.IntervalMinutes != 15 || len(cfg.Rules) != 0 {
		t.Fatalf("default config: %+v", cfg)
	}

	cfg.Rules = []Rule{seedRule()}
	if _, err := store.SaveConfig("u", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "u", ConfigFileName)); err != nil {
		t.Fatal("config file missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "u", StateFileName)); !os.IsNotExist(err) {
		t.Fatal("saving the config must not create the state file")
	}

	st := NewState()
	st.CreatedFolders["seed"] = "folder-1"
	if err := store.SaveState("u", st); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "u", ConfigFileName))
	if strings.Contains(string(b), "folder-1") {
		t.Fatal("state leaked into the config file")
	}
	loaded, err := store.LoadState("u")
	if err != nil || loaded.CreatedFolders["seed"] != "folder-1" {
		t.Fatalf("state round trip: %v %v", loaded, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "u"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSaveConfigRevisionAndBackup(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(func(uid string) string { return filepath.Join(dir, uid) }, 0)

	first := &Config{Revision: 0, Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}, Rules: []Rule{seedRule()}}
	saved, err := store.SaveConfig("u", first)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Revision)
	}

	// a second tab still holding revision 0
	stale := &Config{Revision: 0, Settings: Settings{Enabled: false, Mode: ModeDry, IntervalMinutes: 15}}
	current, err := store.SaveConfig("u", stale)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("err = %v, want ErrStaleRevision", err)
	}
	if current.Revision != 1 || !current.Settings.Enabled {
		t.Fatalf("stale save must not change the file, current = %+v", current)
	}

	second := &Config{Revision: 1, Settings: Settings{Enabled: true, Mode: ModeLive, IntervalMinutes: 30}, Rules: []Rule{seedRule()}}
	if saved, err = store.SaveConfig("u", second); err != nil || saved.Revision != 2 {
		t.Fatalf("second save: %v rev %d", err, saved.Revision)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "u", ConfigFileName+".bak"))
	if err != nil {
		t.Fatal(err)
	}
	var prev Config
	json.Unmarshal(bak, &prev)
	if prev.Revision != 1 || prev.Settings.Mode != ModeDry {
		t.Errorf(".bak should hold revision 1, got %+v", prev)
	}

	invalid := &Config{Revision: 2, Settings: Settings{Mode: ModeLive, IntervalMinutes: 15}, Rules: []Rule{{Trigger: Trigger{Type: "size_over_mb"}}}}
	if _, err := store.SaveConfig("u", invalid); err == nil {
		t.Fatal("invalid config must be rejected")
	}
	if cur, _ := store.LoadConfig("u"); cur.Revision != 2 {
		t.Errorf("rejected config was written: rev %d", cur.Revision)
	}
}
