package autoorganize

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

func TestTriggerDefaultsWhenAbsent(t *testing.T) {
	var tr V1Trigger
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
	// not_opened_for_days: an absent minAgeDays is 7, an explicit 0 is kept
	var absent, zero V1Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days"}`), &absent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":30,"minAgeEnabled":true,"minAgeDays":0}`), &zero); err != nil {
		t.Fatal(err)
	}
	cfg := &V1Config{Rules: []V1Rule{{Trigger: V1Trigger{Type: TriggerFinishedReading}}, {Trigger: absent}, {Trigger: zero}}}
	cfg.Normalize()
	if cfg.Rules[0].Trigger.MinPages != 3 || cfg.Rules[0].Scope != ScopePdfEpub || cfg.Rules[0].ID == "" {
		t.Errorf("normalize: %+v", cfg.Rules[0])
	}
	if cfg.Rules[1].Trigger.Days != 30 || !cfg.Rules[1].Trigger.MinAgeEnabled || cfg.Rules[1].Trigger.MinAgeDays != 7 {
		t.Errorf("normalize not_opened: %+v", cfg.Rules[1].Trigger)
	}
	if cfg.Rules[2].Trigger.MinAgeDays != 0 || !cfg.Rules[2].Trigger.MinAgeEnabled {
		t.Errorf("explicit minAgeDays 0 must be kept: %+v", cfg.Rules[2].Trigger)
	}
	// and it survives a save and reload (no omitempty turning 0 back into 7)
	b, _ := json.Marshal(cfg.Rules[2].Trigger)
	var back V1Trigger
	if err := json.Unmarshal(b, &back); err != nil || back.MinAgeDays != 0 {
		t.Errorf("minAgeDays 0 lost in a round trip: %s -> %+v", b, back)
	}
	if cfg.Settings.Mode != ModeDry || cfg.Settings.IntervalMinutes != 15 {
		t.Errorf("settings defaults: %+v", cfg.Settings)
	}
}

func TestRuleValidation(t *testing.T) {
	base := func() V1Rule {
		r := seedV1()
		return r
	}
	cases := []struct {
		name   string
		mutate func(r *V1Rule)
		want   string
	}{
		{"seed rule is valid", func(r *V1Rule) {}, ""},
		{"destination equals source", func(r *V1Rule) {
			r.Destination = Destination{FolderRef: FolderRef{ID: "rr"}}
		}, "same as the source"},
		{"root to root", func(r *V1Rule) {
			r.Source = FolderRef{ID: RootID}
			r.Destination = Destination{FolderRef: FolderRef{ID: RootID}}
			r.Trigger = V1Trigger{Type: TriggerPinnedIs, Pinned: true}
			r.Scope = ScopeAll
		}, "same as the source"},
		{"root source is pickable", func(r *V1Rule) { r.Source = FolderRef{ID: RootID} }, ""},
		{"new subfolder needs a name", func(r *V1Rule) { r.Destination.CreateIfMissing.Name = "" }, "needs a name"},
		{"finished_reading locked to pdf/epub", func(r *V1Rule) { r.Scope = ScopeAll }, "only applies to PDF"},
		{"read_progress locked to pdf/epub", func(r *V1Rule) {
			r.Trigger = V1Trigger{Type: TriggerReadProgress, Percent: 80}
			r.Scope = ScopeNotebooks
		}, "only applies to PDF"},
		{"percent range", func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerReadProgress, Percent: 101} }, "between 1 and 100"},
		{"min pages", func(r *V1Rule) { r.Trigger.MinPages = 0 }, "at least 1"},
		{"invalid regex", func(r *V1Rule) {
			r.Trigger = V1Trigger{Type: TriggerNameMatches, Pattern: "/[a-/"}
		}, "invalid regular expression"},
		{"empty pattern", func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerNameMatches} }, "pattern cannot be empty"},
		{"empty tag", func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerTagIs, Tag: "  "} }, "tag cannot be empty"},
		{"tag_is the opt-out tag", func(r *V1Rule) {
			r.Trigger = V1Trigger{Type: TriggerTagIs, Tag: "No_Automation"}
			r.Scope = ScopeAll
		}, "always skipped"},
		{"size_over_mb is not a trigger", func(r *V1Rule) { r.Trigger = V1Trigger{Type: "size_over_mb"} }, "unknown trigger"},
		{"copy is not an action", func(r *V1Rule) { r.Action = V1Action{Type: "copy"} }, "unsupported action"},
		{"trash as source", func(r *V1Rule) { r.Source = FolderRef{ID: "trash"} }, "trash"},
		{"days", func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerNotModified} }, "days must be"},
		{"unknown scope", func(r *V1Rule) { r.Scope = "pdf" }, "unknown scope"},
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
	cfg := &V1Config{Settings: Settings{Mode: ModeLive, IntervalMinutes: 7}}
	if err := cfg.Validate(); err == nil {
		t.Error("interval 7 must be rejected")
	}
	cfg = &V1Config{Settings: Settings{Mode: "maybe", IntervalMinutes: 15}}
	if err := cfg.Validate(); err == nil {
		t.Error("unknown mode must be rejected")
	}
	for _, m := range AllowedIntervals {
		cfg = &V1Config{Settings: Settings{Mode: ModeDry, IntervalMinutes: m}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("interval %d: %v", m, err)
		}
	}
	cfg = &V1Config{Settings: Settings{Mode: ModeDry, IntervalMinutes: 15}, Rules: []V1Rule{seedV1(), seedV1()}}
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
	mk := func(id, from, to string, enabled bool) automation.Rule {
		r := moveRule(id, from, to, trig(TriggerPinnedIs, P{Pinned: true}))
		r.Enabled = enabled
		return r
	}
	destOf := func(r *automation.Rule) (string, bool) { return r.Move().To.ID, true }

	cfg := &automation.Config{Rules: []automation.Rule{mk("a", "inbox", "news", true), mk("b", "inbox", "archive", true), mk("c", "archive", "inbox", true)}}
	got := LoopWarnings(cfg, destOf)
	want := map[string][]int{"b": {3}, "c": {2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("two rule loop: got %v want %v", got, want)
	}

	cfg = &automation.Config{Rules: []automation.Rule{mk("a", "x", "y", true), mk("b", "y", "z", true), mk("c", "z", "x", true)}}
	got = LoopWarnings(cfg, destOf)
	if len(got) != 3 || !reflect.DeepEqual(got["a"], []int{2, 3}) {
		t.Errorf("three rule loop: %v", got)
	}

	cfg = &automation.Config{Rules: []automation.Rule{mk("b", "inbox", "archive", true), mk("c", "archive", "inbox", false)}}
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

	cfg.Rules = []automation.Rule{seedRule()}
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

	first := &automation.Config{Revision: 0, Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}, Rules: []automation.Rule{seedRule()}}
	saved, err := store.SaveConfig("u", first)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Revision)
	}

	// a second tab still holding revision 0
	stale := &automation.Config{Revision: 0, Settings: Settings{Enabled: false, Mode: ModeDry, IntervalMinutes: 15}}
	current, err := store.SaveConfig("u", stale)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("err = %v, want ErrStaleRevision", err)
	}
	if current.Revision != 1 || !current.Settings.Enabled {
		t.Fatalf("stale save must not change the file, current = %+v", current)
	}

	second := &automation.Config{Revision: 1, Settings: Settings{Enabled: true, Mode: ModeLive, IntervalMinutes: 30}, Rules: []automation.Rule{seedRule()}}
	if saved, err = store.SaveConfig("u", second); err != nil || saved.Revision != 2 {
		t.Fatalf("second save: %v rev %d", err, saved.Revision)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "u", ConfigFileName+".bak"))
	if err != nil {
		t.Fatal(err)
	}
	var prev automation.Config
	json.Unmarshal(bak, &prev)
	if prev.Version != automation.Version || prev.Revision != 1 || prev.Settings.Mode != ModeDry {
		t.Errorf(".bak should hold revision 1, got %+v", prev)
	}

	invalid := &automation.Config{Revision: 2, Settings: Settings{Mode: ModeLive, IntervalMinutes: 15},
		Rules: []automation.Rule{{When: automation.When{Subject: automation.SubjectDocument, Trigger: trig("size_over_mb", P{})}}}}
	if _, err := store.SaveConfig("u", invalid); err == nil {
		t.Fatal("invalid config must be rejected")
	}
	if cur, _ := store.LoadConfig("u"); cur.Revision != 2 {
		t.Errorf("rejected config was written: rev %d", cur.Revision)
	}
}

func pipelineV1() V1Rule {
	return V1Rule{
		ID: "pipe", Enabled: true,
		Scope:   ScopeNotebooks,
		Trigger: V1Trigger{Type: TriggerPageChanged},
		Action:  V1Action{Type: ActionPipeline},
		Watch:   []WatchRef{{ID: "nb1", Kind: "folder", Name: "Notebooks"}},
	}
}

func TestPipelineRuleValidates(t *testing.T) {
	r := pipelineV1()
	if err := r.Validate(); err != nil {
		t.Fatalf("pipeline rule must validate: %v", err)
	}
	if !r.IsPipeline() {
		t.Fatal("IsPipeline false")
	}
	mv := seedV1()
	if mv.IsPipeline() {
		t.Fatal("move rule reported as pipeline")
	}
	r.Watch = append(r.Watch, WatchRef{ID: "doc1", Kind: "document"})
	if err := r.Validate(); err != nil {
		t.Fatalf("folder + document watch must validate: %v", err)
	}
	// a root folder watch is allowed (empty id is the root)
	r.Watch = []WatchRef{{ID: RootID, Kind: "folder"}}
	if err := r.Validate(); err != nil {
		t.Fatalf("root watch must validate: %v", err)
	}
	r.Watch = []WatchRef{{ID: "x", Kind: "page"}}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("bad kind: %v", err)
	}
	r = pipelineV1()
	r.Trigger = V1Trigger{Type: TriggerNameMatches, Pattern: "*"}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "page_changed") {
		t.Fatalf("pipeline needs page_changed: %v", err)
	}
	r = pipelineV1()
	r.Scope = "pdf"
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("scope still checked: %v", err)
	}
}

func TestPipelineRuleRequiresWatch(t *testing.T) {
	r := pipelineV1()
	r.Watch = nil
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "watch") {
		t.Fatalf("empty watch: %v", err)
	}
}

func TestPipelineRuleRejectsTrashWatch(t *testing.T) {
	r := pipelineV1()
	r.Watch = []WatchRef{{ID: "trash", Kind: "folder"}}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "trash") {
		t.Fatalf("trash watch: %v", err)
	}
}

func TestPageChangedRejectedOnMoveRule(t *testing.T) {
	r := seedV1()
	r.Trigger = V1Trigger{Type: TriggerPageChanged}
	r.Scope = ScopeAll
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "page_changed") {
		t.Fatalf("page_changed on a move rule: %v", err)
	}
}

func TestNormalizeFillsPipelineDefaults(t *testing.T) {
	cfg := &V1Config{Rules: []V1Rule{{Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline},
		Watch: []WatchRef{{ID: "a", Kind: "folder"}}}}}
	cfg.Normalize()
	r := cfg.Rules[0]
	if r.Action.Model != DefaultPipelineModel || r.Action.Prompt != DefaultPipelinePrompt {
		t.Fatalf("defaults not filled: %+v", r.Action)
	}
	if r.Scope != ScopeAll || r.ID == "" {
		t.Fatalf("common defaults: %+v", r)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("normalized pipeline config invalid: %v", err)
	}
	// explicit values are kept
	cfg = &V1Config{Rules: []V1Rule{{Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline, Model: "m/x", Prompt: "p"},
		Watch: []WatchRef{{ID: "a", Kind: "folder"}}}}}
	cfg.Normalize()
	if cfg.Rules[0].Action.Model != "m/x" || cfg.Rules[0].Action.Prompt != "p" {
		t.Fatalf("explicit values overwritten: %+v", cfg.Rules[0].Action)
	}
	// whitespace-only is empty; padding is trimmed
	cfg = &V1Config{Rules: []V1Rule{
		{Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline, Model: "  \t", Prompt: " \n "},
			Watch: []WatchRef{{ID: "a", Kind: "folder"}}},
		{Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline, Model: " m/x\n", Prompt: "\n p "},
			Watch: []WatchRef{{ID: "a", Kind: "folder"}}},
	}}
	cfg.Normalize()
	if a := cfg.Rules[0].Action; a.Model != DefaultPipelineModel || a.Prompt != DefaultPipelinePrompt {
		t.Fatalf("whitespace-only not defaulted: %+v", a)
	}
	if a := cfg.Rules[1].Action; a.Model != "m/x" || a.Prompt != "p" {
		t.Fatalf("not trimmed: %+v", a)
	}
	// move rules get no model or prompt
	mv := &V1Config{Rules: []V1Rule{seedV1()}}
	mv.Normalize()
	if mv.Rules[0].Action.Model != "" || mv.Rules[0].Action.Prompt != "" {
		t.Fatalf("move rule got pipeline defaults: %+v", mv.Rules[0].Action)
	}
}

func TestConfigJSONRoundTripWithWatch(t *testing.T) {
	cfg := &V1Config{Revision: 2, Settings: Settings{Enabled: true, Mode: ModeLive, IntervalMinutes: 15},
		Rules: []V1Rule{pipelineV1(), seedV1()}}
	cfg.Normalize()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var back V1Config
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, &back) {
		t.Fatalf("round trip differs:\n%+v\n%+v", cfg, &back)
	}
	if !strings.Contains(string(b), `"watch":[{"id":"nb1","kind":"folder","name":"Notebooks"}]`) {
		t.Errorf("watch not serialised: %s", b)
	}
	// a move rule serialises without watch, prompt or model
	mb, _ := json.Marshal(seedV1())
	for _, k := range []string{`"watch"`, `"prompt"`, `"model"`} {
		if strings.Contains(string(mb), k) {
			t.Errorf("move rule JSON contains %s: %s", k, mb)
		}
	}
}

func TestNormalizePipelineScopeDefaultsToAll(t *testing.T) {
	cfg := &V1Config{Rules: []V1Rule{
		{Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline}, Watch: []WatchRef{{ID: "a", Kind: WatchFolder}}},
		{Scope: ScopeNotebooks, Trigger: V1Trigger{Type: TriggerPageChanged}, Action: V1Action{Type: ActionPipeline}, Watch: []WatchRef{{ID: "a", Kind: WatchFolder}}},
	}}
	cfg.Normalize()
	if cfg.Rules[0].Scope != ScopeAll || cfg.Rules[1].Scope != ScopeNotebooks {
		t.Fatalf("scopes = %q, %q", cfg.Rules[0].Scope, cfg.Rules[1].Scope)
	}
}
