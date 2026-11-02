package automation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTriggerJSONOnlyTypeFields(t *testing.T) {
	cases := []struct {
		tr   Trigger
		want string
	}{
		{Trigger{Type: TriggerPageChanged, Params: Params{SettleHours: 6, Pinned: true, Days: 3}}, `{"type":"page_changed"}`},
		{Trigger{Type: TriggerFinishedReading, Params: Params{MinPages: 2, SettleHours: 6, Pinned: true}}, `{"type":"finished_reading","minPages":2,"settleHours":6}`},
		{Trigger{Type: TriggerPageTagged, Params: Params{Tag: "todo", SettleHours: 6}}, `{"type":"page_tagged","tag":"todo"}`},
		{Trigger{Type: TriggerPinnedIs, Params: Params{Pinned: false}}, `{"type":"pinned_is","pinned":false}`},
		{Trigger{Type: "nope", Params: Params{Days: 4}}, `{"type":"nope"}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.tr)
		if err != nil || string(b) != c.want {
			t.Errorf("%s: got %s %v, want %s", c.tr.Type, b, err, c.want)
		}
		// a pointer marshals the same
		if pb, _ := json.Marshal(&c.tr); string(pb) != string(b) {
			t.Errorf("%s: pointer %s, value %s", c.tr.Type, pb, b)
		}
	}
}

func TestTriggerJSONHasNoMinAge(t *testing.T) {
	typ := reflect.TypeOf(Trigger{})
	for i := 0; i < typ.NumField(); i++ {
		if strings.Contains(strings.ToLower(typ.Field(i).Name), "minage") {
			t.Errorf("Trigger has a min-age field %s", typ.Field(i).Name)
		}
	}
	var tr Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":30}`), &tr); err != nil {
		t.Fatal(err)
	}
	want := Trigger{Type: TriggerNotOpened, Params: Params{Days: 30, SettleHours: 6, Pinned: true}}
	if tr != want {
		t.Errorf("got %+v, want %+v", tr, want)
	}
	b, _ := json.Marshal(tr)
	if string(b) != `{"type":"not_opened_for_days","days":30}` || strings.Contains(strings.ToLower(string(b)), "minage") {
		t.Errorf("re-marshal %s", b)
	}
}

func TestTriggerJSONDefaults(t *testing.T) {
	var tr Trigger
	if err := json.Unmarshal([]byte(`{"type":"read_progress","percent":50}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.SettleHours != 6 || !tr.Pinned {
		t.Errorf("absent settleHours/pinned: %+v", tr)
	}
	if err := json.Unmarshal([]byte(`{"type":"pinned_is","pinned":false,"settleHours":0}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.SettleHours != 0 || tr.Pinned {
		t.Errorf("explicit zero values lost: %+v", tr)
	}
}

func TestConditionMinAgeZeroKept(t *testing.T) {
	c := Condition{Type: CondMinAgeDays, Params: Params{Days: 0, Pinned: true}}
	b, err := json.Marshal(c)
	if err != nil || string(b) != `{"type":"min_age_days","days":0}` {
		t.Fatalf("got %s %v", b, err)
	}
	var back Condition
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != (Condition{Type: CondMinAgeDays}) {
		t.Errorf("round trip %+v", back)
	}
	if b2, _ := json.Marshal(back); string(b2) != string(b) {
		t.Errorf("round trip %s, want %s", b2, b)
	}
}

func kindConds(r *Rule) []string {
	var out []string
	for _, c := range r.Conditions {
		if c.Type == CondKindIs {
			out = append(out, c.Kind)
		}
	}
	return out
}

func TestNormalizeAddsPdfKindDropsAll(t *testing.T) {
	kindAll := Condition{Type: CondKindIs, Params: Params{Kind: KindAll}}
	tagged := Condition{Type: CondTagIs, Params: Params{Tag: "keep"}}
	c := &Config{Rules: []Rule{
		// page-based, no kind: pdf_epub is added first
		{When: When{Subject: SubjectDocument, Trigger: Trigger{Type: TriggerFinishedReading}}, Conditions: []Condition{tagged}},
		// page-based, kind all: dropped, then pdf_epub added
		{When: When{Subject: SubjectDocument, Trigger: Trigger{Type: TriggerReadProgress}}, Conditions: []Condition{kindAll}},
		// not page-based, kind all: dropped, nothing added
		{When: When{Subject: SubjectDocument, Trigger: Trigger{Type: TriggerNotOpened}}, Conditions: []Condition{kindAll, tagged}},
		// page-based with notebooks: kept as is (Validate rejects it)
		{When: When{Subject: SubjectDocument, Trigger: Trigger{Type: TriggerFinishedReading}}, Conditions: []Condition{{Type: CondKindIs, Params: Params{Kind: KindNotebooks}}}},
		// page rule, kind all: dropped
		{When: When{Subject: SubjectPage, Trigger: Trigger{Type: TriggerPageChanged}}, Conditions: []Condition{kindAll}},
	}}
	c.Normalize()
	want := [][]string{{KindPdfEpub}, {KindPdfEpub}, nil, {KindNotebooks}, nil}
	for i := range c.Rules {
		if got := kindConds(&c.Rules[i]); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("rule %d kinds %v, want %v", i, got, want[i])
		}
	}
	if r := c.Rules[0]; len(r.Conditions) != 2 || r.Conditions[0].Type != CondKindIs || r.Conditions[1] != tagged {
		t.Errorf("rule 0 conditions %+v", r.Conditions)
	}
	if r := c.Rules[2]; len(r.Conditions) != 1 || r.Conditions[0] != tagged {
		t.Errorf("rule 2 conditions %+v", r.Conditions)
	}
}

func TestNormalizeFills(t *testing.T) {
	c := &Config{Version: 1, Rules: []Rule{
		{When: When{Subject: SubjectDocument, Trigger: Trigger{Type: TriggerNotOpened}},
			Conditions: []Condition{{Type: CondNotOpened}, {Type: CondMinAgeDays}},
			Actions:    []Action{{Type: ActionMove, To: &Destination{CreateIfMissing: &CreateIfMissing{Name: "  Old "}}}}},
		{When: When{Subject: SubjectPage, Trigger: Trigger{Type: TriggerPageTagged, Params: Params{Tag: " todo "}}},
			Conditions: []Condition{{Type: CondPageTagIs, Params: Params{Tag: " x "}}},
			Actions:    []Action{{Type: ActionSaveNotes, Prompt: "  ", Model: " m "}, {Type: ActionExtractTasks}}},
		{ID: "keep"},
	}}
	c.Normalize()
	if c.Version != Version || c.Settings.Mode != ModeDry || c.Settings.IntervalMinutes != DefaultInterval {
		t.Errorf("config %+v", c)
	}
	d, p, e := c.Rules[0], c.Rules[1], c.Rules[2]
	if d.ID == "" || p.ID == "" || d.ID == p.ID || e.ID != "keep" {
		t.Errorf("ids %q %q %q", d.ID, p.ID, e.ID)
	}
	if d.When.Trigger.Days != 30 || d.Conditions[0].Days != 30 || d.Conditions[1].Days != 0 {
		t.Errorf("defaults %+v %+v", d.When.Trigger, d.Conditions)
	}
	if cm := d.Actions[0].To.CreateIfMissing; cm.Name != "Old" || cm.Parent != ParentSource {
		t.Errorf("createIfMissing %+v", cm)
	}
	if p.When.Trigger.Tag != "todo" || p.Conditions[0].Tag != "x" {
		t.Errorf("tags %+v %+v", p.When.Trigger, p.Conditions)
	}
	if a := p.Actions[0]; a.Prompt != DefaultPipelinePrompt || a.Model != "m" {
		t.Errorf("save_notes %+v", a)
	}
	if a := p.Actions[1]; a.Prompt != DefaultPipelinePrompt || a.Model != DefaultPipelineModel {
		t.Errorf("extract_tasks %+v", a)
	}
	if e.When.In == nil || e.Conditions == nil || e.Actions == nil {
		t.Errorf("nil slices %+v", e)
	}
	if b, _ := json.Marshal(e); !strings.Contains(string(b), `"in":[]`) || !strings.Contains(string(b), `"conditions":[]`) || !strings.Contains(string(b), `"actions":[]`) {
		t.Errorf("empty slices marshal as %s", b)
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig(7)
	if c.Version != Version || c.Settings != (Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: DefaultInterval}) || c.Rules == nil {
		t.Errorf("%+v", c)
	}
	if DefaultConfig(30).Settings.IntervalMinutes != 30 {
		t.Error("valid interval not kept")
	}
	if err := DefaultConfig(60).Validate(); err != nil {
		t.Error(err)
	}
}

func TestAccessors(t *testing.T) {
	doc := Rule{
		When: When{Subject: SubjectDocument, In: []Place{{ID: "f1", Kind: PlaceFolder, Name: "Inbox"}}, Trigger: Trigger{Type: TriggerNotOpened}},
		Conditions: []Condition{
			{Type: CondTagIs, Params: Params{Tag: "a"}},
			{Type: CondKindIs, Params: Params{Kind: KindNotebooks}},
			{Type: CondMinAgeDays, Params: Params{Days: 3}},
			{Type: CondPageTagIs, Params: Params{Tag: "p"}},
			{Type: "unknown"},
		},
		Actions: []Action{{Type: ActionMove, To: &Destination{FolderRef: FolderRef{ID: "f2"}}}},
	}
	if !doc.IsDocument() || doc.IsPage() {
		t.Error("subject")
	}
	if doc.Source() != (FolderRef{ID: "f1", Name: "Inbox"}) || (&Rule{}).Source() != (FolderRef{}) {
		t.Errorf("source %+v", doc.Source())
	}
	if m := doc.Move(); m == nil || m != &doc.Actions[0] || doc.PageAction() != nil {
		t.Error("move action")
	}
	if doc.Kind() != KindNotebooks || (&Rule{}).Kind() != KindAll {
		t.Error("kind")
	}
	got := doc.DocConditions()
	if len(got) != 2 || got[0].Type != CondTagIs || got[1].Type != CondMinAgeDays {
		t.Errorf("doc conditions %+v", got)
	}
	if !doc.SkipsPinned() {
		t.Error("skips pinned")
	}
	doc.Conditions = append(doc.Conditions, Condition{Type: CondPinnedIs})
	if doc.SkipsPinned() {
		t.Error("pinned_is condition still skips pinned")
	}
	if (&Rule{When: When{Trigger: Trigger{Type: TriggerPinnedIs}}}).SkipsPinned() {
		t.Error("pinned_is trigger still skips pinned")
	}

	page := Rule{
		When: When{Subject: SubjectPage, Trigger: Trigger{Type: TriggerPageTagged, Params: Params{Tag: "To-Do"}}},
		Conditions: []Condition{
			{Type: CondPageTagIs, Params: Params{Tag: "b"}},
			{Type: CondPageTagIs, Params: Params{Tag: "to_do"}},
			{Type: CondPageTagIs, Params: Params{Tag: " "}},
		},
		Actions: []Action{{Type: ActionSaveNotes}},
	}
	if !page.IsPage() || page.IsDocument() || page.PageAction() != &page.Actions[0] || page.Move() != nil {
		t.Error("page rule accessors")
	}
	if tags := page.PageTags(); !reflect.DeepEqual(tags, []string{"b", "to do"}) {
		t.Errorf("page tags %v", tags)
	}
	page.When.Trigger = Trigger{Type: TriggerPageChanged, Params: Params{Tag: "ignored"}}
	page.Conditions = nil
	if tags := page.PageTags(); tags != nil {
		t.Errorf("page_changed tags %v", tags)
	}
}
