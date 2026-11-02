package autoorganize

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

func loadV1Golden(t *testing.T) *V1Config {
	t.Helper()
	b, err := os.ReadFile("testdata/v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var c V1Config
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// decoded JSON, for semantic comparison
func decoded(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// mustMigrate migrates v1, which must not hold a rule v1 skipped as invalid
func mustMigrate(t *testing.T, v1 *V1Config) *automation.Config {
	t.Helper()
	c, issues := migrateIssues(t, v1)
	if len(issues) != 0 {
		t.Fatalf("issues %+v", issues)
	}
	return c
}

func migrateIssues(t *testing.T, v1 *V1Config) (*automation.Config, []RuleIssue) {
	t.Helper()
	c, issues, err := MigrateV1(v1)
	if err != nil {
		t.Fatal(err)
	}
	return c, issues
}

// v1From parses a v1 config JSON, so absent fields get the v1 defaults
func v1From(t *testing.T, rules string) *V1Config {
	t.Helper()
	var c V1Config
	if err := json.Unmarshal([]byte(`{"revision":3,"settings":{"enabled":true,"mode":"dry","intervalMinutes":15},"rules":[`+rules+`]}`), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestMigrateV1Golden(t *testing.T) {
	v1 := loadV1Golden(t)
	before, _ := json.Marshal(v1)
	got := mustMigrate(t, v1)
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/v2-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded(t, b), decoded(t, want)) {
		t.Errorf("MigrateV1(v1-golden) =\n%s\nwant\n%s", b, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if after, _ := json.Marshal(v1); string(after) != string(before) {
		t.Error("MigrateV1 changed its input")
	}
}

func TestMigrateV1Mapping(t *testing.T) {
	const src = `"source":{"id":"f1","name":"Inbox"},"destination":{"id":"f2"},"action":{"type":"move"}`
	const pdf = `"scope":"pdf_epub",`
	cases := []struct {
		name  string
		rules string
		check func(t *testing.T, r *automation.Rule)
	}{
		{"page_tagged without watch", `{"id":"r1","enabled":true,"trigger":{"type":"page_tagged","tag":" todo "},"action":{"type":"pipeline"}}`,
			func(t *testing.T, r *automation.Rule) {
				if !r.IsPage() || r.When.In == nil || len(r.When.In) != 0 {
					t.Errorf("in %#v", r.When.In)
				}
				if r.When.Trigger != (automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: "todo"}}) {
					t.Errorf("trigger %+v", r.When.Trigger)
				}
				if len(r.Conditions) != 0 {
					t.Errorf("scope all gave conditions %+v", r.Conditions)
				}
				if a := r.Actions; len(a) != 1 || a[0] != (automation.Action{Type: automation.ActionExtractTasks, Prompt: DefaultPipelinePrompt, Model: DefaultPipelineModel}) {
					t.Errorf("actions %+v", a)
				}
			}},
		{"tasks false", `{"id":"r1","enabled":true,"trigger":{"type":"page_changed"},"action":{"type":"pipeline","prompt":"P","model":"M","tasks":false},"watch":[{"id":"d1","kind":"document"}]}`,
			func(t *testing.T, r *automation.Rule) {
				if a := r.Actions; len(a) != 1 || a[0] != (automation.Action{Type: automation.ActionSaveNotes, Prompt: "P", Model: "M"}) {
					t.Errorf("actions %+v", a)
				}
				if !reflect.DeepEqual(r.When.In, []automation.Place{{ID: "d1", Kind: automation.PlaceDocument}}) {
					t.Errorf("in %+v", r.When.In)
				}
			}},
		{"not_opened min age on", `{"id":"r1","enabled":true,` + pdf + `"trigger":{"type":"not_opened_for_days","days":30,"minAgeEnabled":true,"minAgeDays":0},` + src + `}`,
			func(t *testing.T, r *automation.Rule) {
				want := []automation.Condition{
					{Type: automation.CondKindIs, Params: automation.Params{Kind: automation.KindPdfEpub}},
					{Type: automation.CondMinAgeDays, Params: automation.Params{Days: 0}},
				}
				if !reflect.DeepEqual(r.Conditions, want) {
					t.Errorf("conditions %+v", r.Conditions)
				}
				if r.When.Trigger != (automation.Trigger{Type: automation.TriggerNotOpened, Params: automation.Params{Days: 30}}) {
					t.Errorf("trigger %+v", r.When.Trigger)
				}
			}},
		{"not_opened min age off", `{"id":"r1","enabled":true,"scope":"all","trigger":{"type":"not_opened_for_days","days":30,"minAgeEnabled":false,"minAgeDays":7},` + src + `}`,
			func(t *testing.T, r *automation.Rule) {
				if len(r.Conditions) != 0 {
					t.Errorf("conditions %+v", r.Conditions)
				}
			}},
		{"finished_reading min age on", `{"id":"r1","enabled":true,` + pdf + `"trigger":{"type":"finished_reading","minPages":3,"minAgeEnabled":true,"minAgeDays":5},` + src + `}`,
			func(t *testing.T, r *automation.Rule) {
				for _, c := range r.Conditions {
					if c.Type == automation.CondMinAgeDays {
						t.Errorf("finished_reading got %+v (P1)", c)
					}
				}
			}},
		{"scope notebooks", `{"id":"r1","enabled":true,"scope":"notebooks","trigger":{"type":"tag_is","tag":"done"},` + src + `}`,
			func(t *testing.T, r *automation.Rule) {
				if r.Kind() != automation.KindNotebooks || len(r.Conditions) != 1 {
					t.Errorf("conditions %+v", r.Conditions)
				}
				if !r.IsDocument() || !reflect.DeepEqual(r.When.In, []automation.Place{{ID: "f1", Kind: automation.PlaceFolder, Name: "Inbox"}}) {
					t.Errorf("when %+v", r.When)
				}
				if m := r.Move(); m == nil || !reflect.DeepEqual(m.To, &automation.Destination{FolderRef: automation.FolderRef{ID: "f2"}}) {
					t.Errorf("move %+v", m)
				}
			}},
		{"pipeline without trigger", `{"id":"r1","enabled":true,"trigger":{"settleHours":2},"action":{"type":"pipeline"},"watch":[{"id":"","kind":"folder"}]}`,
			func(t *testing.T, r *automation.Rule) {
				if r.When.Trigger != (automation.Trigger{Type: automation.TriggerPageChanged}) || !r.Enabled {
					t.Errorf("trigger %+v enabled %v", r.When.Trigger, r.Enabled)
				}
			}},
		{"pipeline finished_reading", `{"id":"r1","enabled":true,"trigger":{"type":"finished_reading","minPages":4,"tag":"x"},"action":{"type":"pipeline"},"watch":[{"id":"","kind":"folder"}]}`,
			func(t *testing.T, r *automation.Rule) {
				if r.When.Trigger != (automation.Trigger{Type: automation.TriggerPageChanged}) || !r.Enabled {
					t.Errorf("trigger %+v enabled %v", r.When.Trigger, r.Enabled)
				}
				if r.Kind() != automation.KindAll {
					t.Errorf("kind %q", r.Kind())
				}
			}},
		{"pinned_is keeps pinned false", `{"id":"r1","enabled":true,` + pdf + `"trigger":{"type":"pinned_is","pinned":false},` + src + `}`,
			func(t *testing.T, r *automation.Rule) {
				if r.When.Trigger != (automation.Trigger{Type: automation.TriggerPinnedIs}) {
					t.Errorf("trigger %+v", r.When.Trigger)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustMigrate(t, v1From(t, c.rules))
			if len(got.Rules) != 1 || got.Rules[0].ID != "r1" {
				t.Fatalf("rules %+v", got.Rules)
			}
			c.check(t, &got.Rules[0])
		})
	}

	// disabled stays disabled; names, order and ids are kept
	got := mustMigrate(t, v1From(t, `{"id":"b","name":"second","enabled":false,"trigger":{"type":"tag_is","tag":"x"},`+src+`},`+
		`{"id":"a","name":"first","enabled":true,"trigger":{"type":"page_changed"},"action":{"type":"pipeline"},"watch":[{"id":"","kind":"folder"}]}`))
	if len(got.Rules) != 2 {
		t.Fatalf("rules %+v", got.Rules)
	}
	if r := got.Rules[0]; r.ID != "b" || r.Name != "second" || r.Enabled {
		t.Errorf("rule 0 %+v", r)
	}
	if r := got.Rules[1]; r.ID != "a" || r.Name != "first" || !r.Enabled {
		t.Errorf("rule 1 %+v", r)
	}
	if got.Version != automation.Version || got.Revision != 4 || got.Settings != (automation.Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}) {
		t.Errorf("config %+v", got)
	}
}

// TestMigrateV1AbsentFieldsUseV1Defaults Review Focus 1: a file from an older
// build or a hand edit migrates to what the engine ran with
func TestMigrateV1AbsentFieldsUseV1Defaults(t *testing.T) {
	got := mustMigrate(t, v1From(t,
		`{"id":"n","enabled":true,"scope":"pdf_epub","source":{"id":"f1"},"trigger":{"type":"not_opened_for_days","days":30},"destination":{"id":"f2"},"action":{"type":"move"}},`+
			`{"id":"f","enabled":true,"scope":"pdf_epub","source":{"id":"f1"},"trigger":{"type":"finished_reading","minPages":2},"destination":{"id":"f2"},"action":{"type":"move"}}`))
	n, f := &got.Rules[0], &got.Rules[1]
	want := []automation.Condition{
		{Type: automation.CondKindIs, Params: automation.Params{Kind: automation.KindPdfEpub}},
		{Type: automation.CondMinAgeDays, Params: automation.Params{Days: 7}},
	}
	if !reflect.DeepEqual(n.Conditions, want) {
		t.Errorf("not_opened conditions %+v, want %+v", n.Conditions, want)
	}
	if f.When.Trigger.SettleHours != 6 {
		t.Errorf("finished_reading settle %d", f.When.Trigger.SettleHours)
	}
	if !n.Enabled || !f.Enabled {
		t.Errorf("valid rules disabled: %v %v", n.Enabled, f.Enabled)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// TestMigrateV1EmptyWatchNotRejected X2: page_changed with no watch matched
// nothing in v1 and migrates to in=[]; only Validate (on PUT) rejects it
func TestMigrateV1EmptyWatchNotRejected(t *testing.T) {
	got := mustMigrate(t, v1From(t, `{"id":"p","enabled":true,"trigger":{"type":"page_changed"},"action":{"type":"pipeline"}}`))
	if in := got.Rules[0].When.In; in == nil || len(in) != 0 {
		t.Errorf("in %#v", in)
	}
	if err := got.Validate(); err == nil || !strings.Contains(err.Error(), "a page rule needs at least one place") {
		t.Errorf("Validate: %v", err)
	}
}

// TestMigrateV1InvalidRawRuleDisabled X3: v1 never normalized on load, so the
// planner skipped an enabled move rule that failed v1 Validate as stored. It
// migrates disabled (id and order kept) and comes back as a RuleIssue; the
// valid rules stay enabled. Pipeline rules are not checked (never validated).
func TestMigrateV1InvalidRawRuleDisabled(t *testing.T) {
	v1 := loadV1Golden(t)
	var extra []V1Rule
	if err := json.Unmarshal([]byte(`[
		{"id":"noscope","enabled":true,"source":{"id":"f1"},"trigger":{"type":"tag_is","tag":"x"},"destination":{"id":"f2"},"action":{"type":"move"}},
		{"id":"noaction","enabled":true,"scope":"all","source":{"id":"f1"},"trigger":{"type":"tag_is","tag":"x"},"destination":{"id":"f2"}},
		{"id":"off","enabled":false,"source":{"id":"f1"},"trigger":{"type":"tag_is","tag":"x"},"destination":{"id":"f2"}},
		{"id":"pipe","enabled":true,"trigger":{"type":"page_changed"},"action":{"type":"pipeline"},"watch":[{"id":"","kind":"folder"}]}
	]`), &extra); err != nil {
		t.Fatal(err)
	}
	// the invalid rule sits between the golden rules, so order is checked too
	v1.Rules = []V1Rule{v1.Rules[0], extra[0], v1.Rules[1], extra[1], extra[2], extra[3]}
	got, issues := migrateIssues(t, v1)
	ids := []string{}
	enabled := []bool{}
	for _, r := range got.Rules {
		ids = append(ids, r.ID)
		enabled = append(enabled, r.Enabled)
	}
	wantIDs := []string{"0a1b2c3d-0000-4000-8000-000000000001", "noscope", "0a1b2c3d-0000-4000-8000-000000000002", "noaction", "off", "pipe"}
	if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(enabled, []bool{true, false, true, false, false, true}) {
		t.Errorf("ids %v enabled %v", ids, enabled)
	}
	if len(issues) != 2 || issues[0].Index != 1 || issues[0].RuleID != "noscope" || !strings.Contains(issues[0].Reason, `unknown scope ""`) ||
		issues[1].Index != 3 || issues[1].RuleID != "noaction" || !strings.Contains(issues[1].Reason, `unsupported action ""`) {
		t.Errorf("issues %+v", issues)
	}
	// the rest of the rule is still migrated (with the v1 defaults)
	if r := got.Rules[1]; r.Kind() != automation.KindPdfEpub || r.Move() == nil {
		t.Errorf("disabled rule %+v", r)
	}
}

func TestMigrateV1UnknownActionErrors(t *testing.T) {
	_, _, err := MigrateV1(v1From(t, `{"id":"r1","enabled":true,"trigger":{"type":"tag_is","tag":"x"},"action":{"type":"copy"}}`))
	if err == nil || !strings.Contains(err.Error(), `"copy"`) || !strings.Contains(err.Error(), "r1") {
		t.Errorf("err %v", err)
	}
	if _, _, err := MigrateV1(nil); err == nil {
		t.Error("nil config migrated")
	}
}

func TestMigrateV1Deterministic(t *testing.T) {
	a, _ := json.Marshal(mustMigrate(t, loadV1Golden(t)))
	b, _ := json.Marshal(mustMigrate(t, loadV1Golden(t)))
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n%s", a, b)
	}
}

// withoutRevision the decoded config with the revision removed
func withoutRevision(t *testing.T, c *automation.Config) any {
	t.Helper()
	b, _ := json.Marshal(c)
	v := decoded(t, b).(map[string]any)
	delete(v, "revision")
	return v
}

func TestToV1RoundTrip(t *testing.T) {
	v1 := loadV1Golden(t)
	v2 := mustMigrate(t, v1)
	back, issues := ToV1(v2, v1)
	if len(issues) != 0 {
		t.Fatalf("issues %+v", issues)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("v1 Validate: %v", err)
	}
	if back.Revision != 12 {
		t.Errorf("revision %d, want max(10, 11)+1", back.Revision)
	}
	again := mustMigrate(t, back)
	if !reflect.DeepEqual(withoutRevision(t, again), withoutRevision(t, v2)) {
		a, _ := json.Marshal(again)
		b, _ := json.Marshal(v2)
		t.Errorf("v2 -> v1 -> v2 =\n%s\nwant\n%s", a, b)
	}

	// the explicit v1 fields
	r := back.Rules[0].Trigger
	if r.SettleHours != 6 || !r.MinAgeEnabled || r.MinAgeDays != 0 || !r.Pinned {
		t.Errorf("move trigger %+v", r)
	}
	if p := back.Rules[1]; p.Action.Type != ActionPipeline || p.Scope != ScopeAll || len(p.Watch) != 2 || p.Action.Tasks != nil {
		t.Errorf("pipeline rule %+v", p)
	}
	// a newer base revision wins
	if c, _ := ToV1(v2, &V1Config{Revision: 40}); c.Revision != 41 {
		t.Errorf("revision %d with base 40", c.Revision)
	}
	if c, _ := ToV1(v2, nil); c.Revision != 12 {
		t.Errorf("revision %d with no base", c.Revision)
	}
}

func TestToV1MapsEveryV1Form(t *testing.T) {
	v1 := v1From(t,
		`{"id":"n","enabled":true,"scope":"pdf_epub","source":{"id":"f1"},"trigger":{"type":"not_opened_for_days","days":30,"minAgeEnabled":true,"minAgeDays":3},"destination":{"id":"f2"},"action":{"type":"move"}},`+
			`{"id":"o","enabled":true,"scope":"all","source":{"id":"f1"},"trigger":{"type":"not_opened_for_days","days":30,"minAgeEnabled":false},"destination":{"id":"f2"},"action":{"type":"move"}},`+
			`{"id":"p","enabled":true,"scope":"notebooks","source":{"id":"f1"},"trigger":{"type":"pinned_is","pinned":false},"destination":{"id":"f2"},"action":{"type":"move"}},`+
			`{"id":"g","enabled":true,"scope":"notebooks","trigger":{"type":"page_tagged","tag":"todo"},"action":{"type":"pipeline","prompt":"P","model":"M","tasks":false}}`)
	v2 := mustMigrate(t, v1)
	back, issues := ToV1(v2, v1)
	if len(issues) != 0 {
		t.Fatalf("issues %+v", issues)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	if tr := back.Rules[0].Trigger; !tr.MinAgeEnabled || tr.MinAgeDays != 3 {
		t.Errorf("min age on %+v", tr)
	}
	if tr := back.Rules[1].Trigger; tr.MinAgeEnabled || tr.MinAgeDays != 0 {
		t.Errorf("min age off %+v", tr)
	}
	if r := back.Rules[2]; r.Trigger.Pinned || r.Scope != ScopeNotebooks {
		t.Errorf("pinned_is %+v", r)
	}
	if a := back.Rules[3].Action; a.ExtractsTasks() || a.Prompt != "P" || a.Model != "M" || back.Rules[3].Trigger.Tag != "todo" || back.Rules[3].Watch != nil {
		t.Errorf("save notes %+v %+v", a, back.Rules[3])
	}
	again := mustMigrate(t, back)
	if !reflect.DeepEqual(withoutRevision(t, again), withoutRevision(t, v2)) {
		t.Error("round trip differs")
	}
}

func TestToV1ReportsInexpressible(t *testing.T) {
	base := mustMigrate(t, loadV1Golden(t))
	doc, page := base.Rules[0], base.Rules[1]
	kind := func(k string) automation.Condition {
		return automation.Condition{Type: automation.CondKindIs, Params: automation.Params{Kind: k}}
	}
	cases := []struct {
		name   string
		rule   automation.Rule
		reason string
	}{
		{"two conditions", func() automation.Rule {
			r := doc
			r.Conditions = []automation.Condition{kind(automation.KindPdfEpub), kind(automation.KindNotebooks)}
			return r
		}(), "at most one kind_is"},
		{"min age on finished_reading", func() automation.Rule {
			r := doc
			r.Conditions = []automation.Condition{kind(automation.KindPdfEpub), {Type: automation.CondMinAgeDays}}
			return r
		}(), "min_age_days"},
		{"tag_is condition", func() automation.Rule {
			r := doc
			r.Conditions = []automation.Condition{kind(automation.KindPdfEpub), {Type: automation.CondTagIs, Params: automation.Params{Tag: "x"}}}
			return r
		}(), `"tag_is"`},
		{"page_tag_is", func() automation.Rule {
			r := page
			r.Conditions = []automation.Condition{{Type: automation.CondPageTagIs, Params: automation.Params{Tag: "x"}}}
			return r
		}(), `"page_tag_is"`},
		{"two actions", func() automation.Rule {
			r := page
			r.Actions = []automation.Action{{Type: automation.ActionExtractTasks}, {Type: automation.ActionSaveNotes}}
			return r
		}(), "2 actions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.rule.ID = "bad"
			cfg := &automation.Config{Version: 2, Revision: 1, Settings: base.Settings,
				Rules: []automation.Rule{doc, c.rule, page}}
			out, issues := ToV1(cfg, nil)
			if len(issues) != 1 {
				t.Fatalf("issues %+v", issues)
			}
			is := issues[0]
			if is.Index != 1 || is.RuleID != "bad" || !strings.Contains(is.Reason, c.reason) {
				t.Errorf("issue %+v, want index 1, id bad, reason with %q", is, c.reason)
			}
			if len(out.Rules) != 2 || out.Rules[0].ID != doc.ID || out.Rules[1].ID != page.ID {
				t.Errorf("rules %+v", out.Rules)
			}
			if err := out.Validate(); err != nil {
				t.Errorf("v1 Validate: %v", err)
			}
		})
	}
}

// TestMigrateV1LiveShape the shapes Mike's live config has (synthetic ids,
// names and prompt): a named and an unnamed pipeline rule, a hyphenated page
// tag, the root watch entry, and two move rules with Done-style created
// destinations. Ids and order are kept, nothing is flagged, the result is valid.
func TestMigrateV1LiveShape(t *testing.T) {
	b, err := os.ReadFile("testdata/v1-live-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var v1 V1Config
	if err := json.Unmarshal(b, &v1); err != nil {
		t.Fatal(err)
	}
	got := mustMigrate(t, &v1)
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(got.Rules) != len(v1.Rules) {
		t.Fatalf("%d rules, want %d", len(got.Rules), len(v1.Rules))
	}
	for i, r := range got.Rules {
		if r.ID != v1.Rules[i].ID {
			t.Errorf("rule %d id %s, want %s", i, r.ID, v1.Rules[i].ID)
		}
	}
	if got.Rules[0].Name != "extract tagged pages" || got.Rules[1].Name != "" {
		t.Errorf("names %q %q", got.Rules[0].Name, got.Rules[1].Name)
	}
	if tr := got.Rules[0].When.Trigger; tr.Type != automation.TriggerPageTagged || tr.Params.Tag != "x-extract" {
		t.Errorf("rule 1 trigger %+v", tr)
	}
	for _, i := range []int{2, 3} {
		if got.Rules[i].IsPage() {
			t.Errorf("rule %d is a page rule", i)
		}
	}
	// the new golden: generated once with UPDATE_LIVE_SHAPE=1, then asserted
	out, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const goldPath = "testdata/v2-live-shape.json"
	if os.Getenv("UPDATE_LIVE_SHAPE") != "" {
		if err := os.WriteFile(goldPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded(t, out), decoded(t, want)) {
		t.Errorf("MigrateV1(v1-live-shape) =\n%s\nwant\n%s", out, want)
	}
}
