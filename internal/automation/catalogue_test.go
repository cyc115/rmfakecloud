package automation

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// paramsTags the JSON names of the Params fields
func paramsTags() map[string]bool {
	out := map[string]bool{}
	typ := reflect.TypeOf(Params{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		out[name] = true
	}
	return out
}

// TestCatalogueComplete every Fields entry is a Params JSON tag and every
// entry names a subject. Evaluator coverage is checked in autoorganize
// (TestCatalogueComplete there), because the evaluators are package-private.
func TestCatalogueComplete(t *testing.T) {
	tags := paramsTags()
	want := []string{"days", "kind", "minPages", "pattern", "percent", "pinned", "settleHours", "tag"}
	var got []string
	for k := range tags {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Params JSON tags = %v, want %v", got, want)
	}
	for name, specs := range map[string]map[string]*Spec{"trigger": Triggers, "condition": Conditions} {
		for typ, s := range specs {
			for _, f := range s.Fields {
				if !tags[f] {
					t.Errorf("%s %s: field %q is not a Params JSON tag", name, typ, f)
				}
			}
			if len(s.Subjects) == 0 {
				t.Errorf("%s %s has no subject", name, typ)
			}
			for _, sub := range s.Subjects {
				if sub != SubjectDocument && sub != SubjectPage {
					t.Errorf("%s %s: unknown subject %q", name, typ, sub)
				}
			}
		}
	}
}

func TestCatalogueValues(t *testing.T) {
	fields := map[string][]string{
		TriggerFinishedReading: {"minPages", "settleHours"},
		TriggerReadProgress:    {"percent", "settleHours"},
		TriggerNotOpened:       {"days"},
		TriggerNotModified:     {"days"},
		TriggerAgeSinceCreated: {"days"},
		TriggerNameMatches:     {"pattern"},
		TriggerTagIs:           {"tag"},
		TriggerPinnedIs:        {"pinned"},
		TriggerPageChanged:     {},
		TriggerPageTagged:      {"tag"},
	}
	if len(Triggers) != len(fields) {
		t.Errorf("%d triggers, want %d", len(Triggers), len(fields))
	}
	for typ, f := range fields {
		s, ok := Triggers[typ]
		if !ok {
			t.Errorf("trigger %s missing", typ)
			continue
		}
		if len(s.Fields) != len(f) || (len(f) > 0 && !reflect.DeepEqual(s.Fields, f)) {
			t.Errorf("trigger %s fields = %v, want %v", typ, s.Fields, f)
		}
		subj := []string{SubjectDocument}
		if typ == TriggerPageChanged || typ == TriggerPageTagged {
			subj = []string{SubjectPage}
		}
		if !reflect.DeepEqual(s.Subjects, subj) {
			t.Errorf("trigger %s subjects = %v, want %v", typ, s.Subjects, subj)
		}
		pageBased := typ == TriggerFinishedReading || typ == TriggerReadProgress
		if s.PageBased != pageBased || s.BeforeTrigger || s.Repeatable {
			t.Errorf("trigger %s flags = %+v", typ, s)
		}
	}

	both := []string{SubjectDocument, SubjectPage}
	doc := []string{SubjectDocument}
	page := []string{SubjectPage}
	conds := map[string]struct {
		fields   []string
		subjects []string
	}{
		CondKindIs:      {[]string{"kind"}, both},
		CondTagIs:       {[]string{"tag"}, both},
		CondTagIsNot:    {[]string{"tag"}, both},
		CondNameMatches: {[]string{"pattern"}, both},
		CondNotOpened:   {[]string{"days"}, doc},
		CondMinAgeDays:  {[]string{"days"}, doc},
		CondPinnedIs:    {[]string{"pinned"}, doc},
		CondPageTagIs:   {[]string{"tag"}, page},
	}
	if len(Conditions) != len(conds) {
		t.Errorf("%d conditions, want %d", len(Conditions), len(conds))
	}
	for typ, w := range conds {
		s, ok := Conditions[typ]
		if !ok {
			t.Errorf("condition %s missing", typ)
			continue
		}
		if !reflect.DeepEqual(s.Fields, w.fields) || !reflect.DeepEqual(s.Subjects, w.subjects) {
			t.Errorf("condition %s = fields %v subjects %v, want %v %v", typ, s.Fields, s.Subjects, w.fields, w.subjects)
		}
		before := typ == CondKindIs
		repeat := typ == CondTagIs || typ == CondTagIsNot || typ == CondPageTagIs
		if s.BeforeTrigger != before || s.Repeatable != repeat || s.PageBased {
			t.Errorf("condition %s flags = %+v", typ, s)
		}
	}
}

// run Defaults then Validate the way a save does
func check(s *Spec, p Params) (Params, error) {
	if s.Defaults != nil {
		s.Defaults(&p)
	}
	if s.Validate != nil {
		return p, s.Validate(&p)
	}
	return p, nil
}

func TestCatalogueDefaultsAndValidation(t *testing.T) {
	cases := []struct {
		name string
		spec *Spec
		in   Params
		want Params
		err  string
	}{
		{"finished defaults", Triggers[TriggerFinishedReading], Params{}, Params{MinPages: 3}, ""},
		{"finished pages", Triggers[TriggerFinishedReading], Params{MinPages: -1}, Params{MinPages: -1}, "minimum pages must be at least 1"},
		{"finished settle", Triggers[TriggerFinishedReading], Params{MinPages: 3, SettleHours: -1}, Params{MinPages: 3, SettleHours: -1}, "settle hours cannot be negative"},
		{"progress defaults", Triggers[TriggerReadProgress], Params{}, Params{Percent: 80}, ""},
		{"progress range", Triggers[TriggerReadProgress], Params{Percent: 101}, Params{Percent: 101}, "percent must be between 1 and 100"},
		{"progress settle", Triggers[TriggerReadProgress], Params{Percent: 50, SettleHours: -2}, Params{Percent: 50, SettleHours: -2}, "settle hours cannot be negative"},
		{"not opened default", Triggers[TriggerNotOpened], Params{}, Params{Days: 30}, ""},
		{"not opened days", Triggers[TriggerNotOpened], Params{Days: -1}, Params{Days: -1}, "days must be at least 1"},
		{"not modified default", Triggers[TriggerNotModified], Params{}, Params{Days: 90}, ""},
		{"created default", Triggers[TriggerAgeSinceCreated], Params{}, Params{Days: 14}, ""},
		{"pattern empty", Triggers[TriggerNameMatches], Params{}, Params{}, "pattern cannot be empty"},
		{"tag empty", Triggers[TriggerTagIs], Params{Tag: " - "}, Params{Tag: " - "}, "tag cannot be empty"},
		{"tag opt-out", Triggers[TriggerTagIs], Params{Tag: "No_Automation"}, Params{Tag: "No_Automation"}, "documents tagged “no automation” are always skipped; pick another tag"},
		{"page tag trims", Triggers[TriggerPageTagged], Params{Tag: " todo "}, Params{Tag: "todo"}, ""},
		{"page tag empty", Triggers[TriggerPageTagged], Params{Tag: "  "}, Params{}, "tag cannot be empty"},
		{"kind ok", Conditions[CondKindIs], Params{Kind: KindNotebooks}, Params{Kind: KindNotebooks}, ""},
		{"kind all", Conditions[CondKindIs], Params{Kind: KindAll}, Params{Kind: KindAll}, `unknown kind "all"`},
		{"kind empty", Conditions[CondKindIs], Params{}, Params{}, `unknown kind ""`},
		{"min age zero", Conditions[CondMinAgeDays], Params{}, Params{}, ""},
		{"min age negative", Conditions[CondMinAgeDays], Params{Days: -1}, Params{Days: -1}, "minimum age cannot be negative"},
		{"cond not opened default", Conditions[CondNotOpened], Params{}, Params{Days: 30}, ""},
		{"cond not opened days", Conditions[CondNotOpened], Params{Days: -3}, Params{Days: -3}, "days must be at least 1"},
		{"tag_is_not opt-out", Conditions[CondTagIsNot], Params{Tag: "no automation"}, Params{Tag: "no automation"}, "documents tagged “no automation” are always skipped; pick another tag"},
		{"page_tag_is trims", Conditions[CondPageTagIs], Params{Tag: " todo "}, Params{Tag: "todo"}, ""},
		{"page_tag_is empty", Conditions[CondPageTagIs], Params{}, Params{}, "tag cannot be empty"},
	}
	for _, c := range cases {
		got, err := check(c.spec, c.in)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != c.err || got != c.want {
			t.Errorf("%s: got %+v %q, want %+v %q", c.name, got, msg, c.want, c.err)
		}
	}
}
