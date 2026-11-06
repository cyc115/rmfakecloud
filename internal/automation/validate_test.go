package automation

import (
	"encoding/json"
	"errors"
	"testing"
)

const (
	docID  = "0a1b2c3d-0000-4000-8000-0000000000a1"
	pageID = "0a1b2c3d-0000-4000-8000-0000000000a2"
)

// validConfig a valid document rule (index 0) and a valid page rule (index 1)
func validConfig() *Config {
	return &Config{
		Version:  Version,
		Settings: Settings{Enabled: true, Mode: ModeLive, IntervalMinutes: 60},
		Rules: []Rule{
			{
				ID: docID, Enabled: true,
				When: When{Subject: SubjectDocument, In: []Place{{ID: "f1", Kind: PlaceFolder}},
					Trigger: Trigger{Type: TriggerFinishedReading, Params: Params{MinPages: 3, SettleHours: 6}}},
				Conditions: []Condition{{Type: CondKindIs, Params: Params{Kind: KindPdfEpub}}},
				Actions:    []Action{{Type: ActionMove, To: &Destination{FolderRef: FolderRef{ID: "f2"}}}},
			},
			{
				ID: pageID, Enabled: true,
				When: When{Subject: SubjectPage, In: []Place{{ID: "d1", Kind: PlaceDocument}, {ID: RootID, Kind: PlaceFolder}},
					Trigger: Trigger{Type: TriggerPageChanged}},
				Conditions: []Condition{},
				Actions:    []Action{{Type: ActionExtractTasks, Prompt: "p", Model: "m"}},
			},
		},
	}
}

func TestValidateTable(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("base config: %v", err)
	}
	const (
		settings = -1
		doc      = 0
		page     = 1
	)
	type row struct {
		name    string
		rule    int // the rule mutate gets, and the RuleIndex expected
		mutate  func(*Rule)
		cfg     func(*Config) // for settings rows
		wantMsg string
	}
	cond := func(typ string, p Params) Condition { return Condition{Type: typ, Params: p} }
	rows := []row{
		{"mode", settings, nil, func(c *Config) { c.Settings.Mode = "fast" }, "mode must be live or dry"},
		{"interval", settings, nil, func(c *Config) { c.Settings.IntervalMinutes = 7 }, "interval must be one of 5, 10, 15, 30, 60 minutes"},
		{"version", settings, nil, func(c *Config) { c.Version = 1 }, "unsupported automation format version 1; this server reads version 2"},
		{"duplicate id", page, func(r *Rule) { r.ID = docID }, nil, "duplicate rule id"},
		{"subject", doc, func(r *Rule) { r.When.Subject = "folder" }, nil, "subject must be document or page"},
		{"unknown trigger", doc, func(r *Rule) { r.When.Trigger.Type = "opened" }, nil, `unknown trigger "opened"`},
		{"page trigger on document", doc, func(r *Rule) { r.When.Trigger = Trigger{Type: TriggerPageChanged} }, nil,
			`trigger "page_changed" works on pages; it needs a page rule`},
		{"document trigger on page", page, func(r *Rule) { r.When.Trigger = Trigger{Type: TriggerNotOpened, Params: Params{Days: 3}} }, nil,
			`trigger "not_opened_for_days" works on documents; it needs a document rule`},
		{"trigger params", doc, func(r *Rule) { r.When.Trigger.MinPages = 0 }, nil, "minimum pages must be at least 1"},
		{"page trigger params", page, func(r *Rule) { r.When.Trigger = Trigger{Type: TriggerPageTagged, Params: Params{Tag: " "}} }, nil, "tag cannot be empty"},
		{"document no folder", doc, func(r *Rule) { r.When.In = []Place{} }, nil,
			"a document rule watches exactly one folder (a move: the documents directly inside it; a tag rule: every folder inside it too)"},
		{"document two folders", doc, func(r *Rule) { r.When.In = append(r.When.In, Place{ID: "f3", Kind: PlaceFolder}) }, nil,
			"a document rule watches exactly one folder (a move: the documents directly inside it; a tag rule: every folder inside it too)"},
		{"document watches a document", doc, func(r *Rule) { r.When.In[0].Kind = PlaceDocument }, nil,
			"a document rule watches exactly one folder (a move: the documents directly inside it; a tag rule: every folder inside it too)"},
		{"page_changed no place", page, func(r *Rule) { r.When.In = []Place{} }, nil,
			"a page rule needs at least one place; send the root folder for anywhere"},
		{"watched document id", page, func(r *Rule) { r.When.In[0].ID = "" }, nil, "a watched document needs an id"},
		{"place kind", page, func(r *Rule) { r.When.In[1].Kind = "shelf" }, nil, `unknown place kind "shelf"`},
		{"watch trash", page, func(r *Rule) { r.When.In[1].ID = "trash" }, nil, "cannot watch the trash"},
		{"source trash", doc, func(r *Rule) { r.When.In[0].ID = "trash" }, nil, "source cannot be the trash"},
		{"no action", doc, func(r *Rule) { r.Actions = []Action{} }, nil, MsgOneAction},
		{"two actions", page, func(r *Rule) { r.Actions = append(r.Actions, Action{Type: ActionSaveNotes}) }, nil, MsgOneAction},
		{"unknown action", doc, func(r *Rule) { r.Actions[0].Type = "copy" }, nil, `unknown action "copy"`},
		{"move on page", page, func(r *Rule) { r.Actions = []Action{{Type: ActionMove, To: &Destination{}}} }, nil,
			"move works on whole documents, so a page rule cannot move; use a document trigger"},
		{"extract on document", doc, func(r *Rule) { r.Actions = []Action{{Type: ActionExtractTasks}} }, nil,
			"extract tasks works on pages, which a document rule does not see; use a page trigger"},
		{"save notes on document", doc, func(r *Rule) { r.Actions = []Action{{Type: ActionSaveNotes}} }, nil,
			"save notes works on pages, which a document rule does not see; use a page trigger"},
		{"move no destination", doc, func(r *Rule) { r.Actions[0].To = nil }, nil, "move needs a destination"},
		{"destination trash", doc, func(r *Rule) { r.Actions[0].To.ID = "trash" }, nil, "destination cannot be the trash"},
		{"subfolder name", doc, func(r *Rule) { r.Actions[0].To.CreateIfMissing = &CreateIfMissing{Parent: ParentSource} }, nil,
			"new subfolder needs a name"},
		{"subfolder trash", doc, func(r *Rule) { r.Actions[0].To.CreateIfMissing = &CreateIfMissing{Name: "Done", Parent: "trash"} }, nil,
			"new subfolder cannot be created in the trash"},
		{"destination is source", doc, func(r *Rule) { r.Actions[0].To.ID = "f1" }, nil, "destination is the same as the source"},
		{"unknown condition", doc, func(r *Rule) { r.Conditions = append(r.Conditions, cond("opened_on", Params{})) }, nil,
			`unknown condition "opened_on"`},
		{"document-only condition on page", page, func(r *Rule) { r.Conditions = append(r.Conditions, cond(CondMinAgeDays, Params{})) }, nil,
			`condition "min_age_days" does not apply to page rules`},
		{"page-only condition on document", doc, func(r *Rule) { r.Conditions = append(r.Conditions, cond(CondPageTagIs, Params{Tag: "x"})) }, nil,
			`condition "page_tag_is" does not apply to document rules`},
		{"condition twice", doc, func(r *Rule) { r.Conditions = append(r.Conditions, cond(CondKindIs, Params{Kind: KindPdfEpub})) }, nil,
			`condition "kind_is" appears twice`},
		{"condition params", doc, func(r *Rule) { r.Conditions = append(r.Conditions, cond(CondTagIs, Params{Tag: " "})) }, nil,
			"tag cannot be empty"},
		{"condition kind all", page, func(r *Rule) { r.Conditions = append(r.Conditions, cond(CondKindIs, Params{Kind: KindAll})) }, nil,
			`unknown kind "all"`},
		{"page-based on notebooks", doc, func(r *Rule) { r.Conditions[0].Kind = KindNotebooks }, nil, "this trigger only applies to PDF & EPUB"},
		{"page-based without kind", doc, func(r *Rule) { r.Conditions = []Condition{} }, nil, "this trigger only applies to PDF & EPUB"},
		{"add tag on page", page, func(r *Rule) { r.Actions = []Action{{Type: ActionAddTag, Tag: "new"}} }, nil,
			"add tag works on whole documents, so a page rule cannot add tag; use a document trigger"},
		{"remove tag empty", doc, func(r *Rule) { r.Actions = []Action{{Type: ActionRemoveTag, Tag: " - "}} }, nil, "tag cannot be empty"},
		{"add tag opt-out", doc, func(r *Rule) { r.Actions = []Action{{Type: ActionAddTag, Tag: "No Automation"}} }, nil,
			"documents tagged “no automation” are always skipped; pick another tag"},
		{"time compare", doc, func(r *Rule) {
			r.When.Trigger = Trigger{Type: TriggerCreatedTime, Params: Params{Compare: "before", Amount: 1, Unit: UnitDays}}
			r.Conditions = []Condition{}
		}, nil, "compare must be within or older"},
		{"time amount", page, func(r *Rule) {
			r.Conditions = append(r.Conditions, cond(CondModifiedTime, Params{Compare: CompareOlder, Amount: 0, Unit: UnitHours}))
		}, nil, "the amount of time must be at least 1"},
		{"time unit", doc, func(r *Rule) {
			r.When.Trigger = Trigger{Type: TriggerModifiedTime, Params: Params{Compare: CompareWithin, Amount: 3, Unit: "minutes"}}
			r.Conditions = []Condition{}
		}, nil, "unit must be hours, days or weeks"},
	}
	for _, rw := range rows {
		t.Run(rw.name, func(t *testing.T) {
			c := validConfig()
			wantID := ""
			if rw.cfg != nil {
				rw.cfg(c)
			}
			if rw.mutate != nil {
				rw.mutate(&c.Rules[rw.rule])
				wantID = c.Rules[rw.rule].ID
			}
			err := c.Validate()
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("got %v, want a *ValidationError", err)
			}
			if ve.Message != rw.wantMsg || ve.RuleIndex != rw.rule || ve.RuleID != wantID {
				t.Errorf("got {%d %q %q}, want {%d %q %q}", ve.RuleIndex, ve.RuleID, ve.Message, rw.rule, wantID, rw.wantMsg)
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	cases := map[string]func(*Rule){
		"page_tagged anywhere": func(r *Rule) {
			r.When = When{Subject: SubjectPage, In: []Place{}, Trigger: Trigger{Type: TriggerPageTagged, Params: Params{Tag: "todo"}}}
		},
		"repeatable conditions": func(r *Rule) {
			r.Conditions = []Condition{{Type: CondTagIs, Params: Params{Tag: "a"}}, {Type: CondTagIs, Params: Params{Tag: "b"}},
				{Type: CondPageTagIs, Params: Params{Tag: "x"}}, {Type: CondPageTagIs, Params: Params{Tag: "y"}}}
		},
		"save notes": func(r *Rule) { r.Actions[0].Type = ActionSaveNotes },
	}
	for name, mutate := range cases {
		c := validConfig()
		mutate(&c.Rules[1])
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	c := validConfig()
	c.Rules[0].Actions[0].To = &Destination{CreateIfMissing: &CreateIfMissing{Name: "Done", Parent: ParentSource}}
	c.Rules[0].When.In[0].ID = RootID
	if err := c.Validate(); err != nil {
		t.Errorf("root source, new subfolder: %v", err)
	}
	// the "new" tag pair: created in the last 24 hours, then more than 24 hours ago
	c = validConfig()
	c.Rules[0].When.Trigger = Trigger{Type: TriggerCreatedTime, Params: Params{Compare: CompareWithin, Amount: 24, Unit: UnitHours}}
	c.Rules[0].Conditions = []Condition{{Type: CondCreatedTime, Params: Params{Compare: CompareOlder, Amount: 1, Unit: UnitHours}},
		{Type: CondCreatedTime, Params: Params{Compare: CompareWithin, Amount: 2, Unit: UnitWeeks}}}
	c.Rules[0].Actions = []Action{{Type: ActionAddTag, Tag: "new"}}
	if err := c.Validate(); err != nil {
		t.Errorf("tag rule with time trigger and conditions: %v", err)
	}
	// defaults fill an empty time trigger with "in the last 24 hours"
	c.Rules[0].When.Trigger.Params = Params{}
	c.Rules[0].Actions[0].Tag = "  new "
	c.Normalize()
	if p := c.Rules[0].When.Trigger.Params; p.Compare != CompareWithin || p.Amount != 24 || p.Unit != UnitHours || c.Rules[0].Actions[0].Tag != "new" {
		t.Errorf("normalized: %+v %q", p, c.Rules[0].Actions[0].Tag)
	}
	b, _ := json.Marshal(c.Rules[0].When.Trigger)
	if string(b) != `{"type":"created_time","compare":"within","amount":24,"unit":"hours"}` {
		t.Errorf("trigger JSON = %s", b)
	}
}

func TestValidationErrorJSON(t *testing.T) {
	b, _ := json.Marshal(&ValidationError{RuleIndex: -1, Message: "m"})
	if string(b) != `{"ruleIndex":-1,"message":"m"}` {
		t.Errorf("%s", b)
	}
	if e := (&ValidationError{RuleIndex: 2, RuleID: "x", Message: "m"}); e.Error() != "rule 3: m" {
		t.Errorf("%s", e.Error())
	}
}

func TestOrTriggers(t *testing.T) {
	orRule := func(conds []Condition, or ...Trigger) Rule {
		r := validConfig().Rules[0]
		r.When.OrTriggers = or
		r.Conditions = conds
		return r
	}
	tagRead := Trigger{Type: TriggerTagIs, Params: Params{Tag: "read"}}
	pdf := []Condition{{Type: CondKindIs, Params: Params{Kind: KindPdfEpub}}}

	if err := (&Rule{}).Validate(); err == nil {
		t.Fatal("sanity: an empty rule must fail")
	}
	r := orRule(pdf, tagRead)
	if err := r.Validate(); err != nil {
		t.Errorf("finished reading OR tagged read, PDFs: %v", err)
	}
	// mixed triggers need no kind: the page-based one just never matches a notebook
	r = orRule([]Condition{}, tagRead)
	if err := r.Validate(); err != nil {
		t.Errorf("mixed triggers, any kind: %v", err)
	}
	r = orRule([]Condition{{Type: CondKindIs, Params: Params{Kind: KindNotebooks}}}, tagRead)
	if err := r.Validate(); err == nil {
		t.Error("a page-based trigger on a notebooks-only rule must fail")
	}
	r = orRule([]Condition{}, Trigger{Type: TriggerReadProgress, Params: Params{Percent: 90}})
	if err := r.Validate(); err == nil {
		t.Error("all page-based triggers still need PDF & EPUB")
	}
	for name, bad := range map[string]Trigger{
		"unknown":      {Type: "nope"},
		"page trigger": {Type: TriggerPageChanged},
		"bad params":   {Type: TriggerTagIs, Params: Params{Tag: " "}},
	} {
		r = orRule(pdf, bad)
		if err := r.Validate(); err == nil {
			t.Errorf("%s or-trigger must fail", name)
		}
	}
	p := validConfig().Rules[1]
	p.When.OrTriggers = []Trigger{{Type: TriggerPageTagged, Params: Params{Tag: "x"}}}
	if err := p.Validate(); err == nil {
		t.Error("a page rule with or-triggers must fail")
	}
}

func TestOrTriggersNormalizeAndJSON(t *testing.T) {
	cfg := validConfig()
	cfg.Rules[0].Conditions = nil
	cfg.Rules[0].When.OrTriggers = []Trigger{{Type: TriggerNotOpened}}
	cfg.Normalize()
	r := cfg.Rules[0]
	if r.When.OrTriggers[0].Days != 30 {
		t.Errorf("or-trigger defaults not filled: %+v", r.When.OrTriggers[0])
	}
	if r.Kind() != KindAll {
		t.Errorf("mixed triggers must not be locked to PDF & EPUB: %+v", r.Conditions)
	}
	b, _ := json.Marshal(r.When)
	if want := `{"subject":"document","in":[{"id":"f1","kind":"folder"}],"trigger":{"type":"finished_reading","minPages":3,"settleHours":6},"orTriggers":[{"type":"not_opened_for_days","days":30}]}`; string(b) != want {
		t.Errorf("json\n got %s\nwant %s", b, want)
	}
	// a single-trigger rule is stored exactly as before: no orTriggers key, kind_is added
	one := validConfig()
	one.Rules[0].Conditions = nil
	one.Rules[0].When.OrTriggers = []Trigger{}
	one.Normalize()
	b, _ = json.Marshal(one.Rules[0].When)
	if string(b) != `{"subject":"document","in":[{"id":"f1","kind":"folder"}],"trigger":{"type":"finished_reading","minPages":3,"settleHours":6}}` {
		t.Errorf("single trigger json changed: %s", b)
	}
	if one.Rules[0].Kind() != KindPdfEpub {
		t.Error("a lone page-based trigger still adds kind_is pdf_epub")
	}
	var w When
	if err := json.Unmarshal(b, &w); err != nil || w.OrTriggers != nil {
		t.Errorf("round trip: %v %+v", err, w)
	}
}
