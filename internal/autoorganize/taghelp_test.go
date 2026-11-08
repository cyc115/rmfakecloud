package autoorganize

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// the live rule set of 2026-11-07, by shape
func tagHelpConfig() *automation.Config {
	folder := func(id, name string) automation.Place {
		return automation.Place{ID: id, Kind: automation.PlaceFolder, Name: name}
	}
	readTo := &automation.Destination{CreateIfMissing: &automation.CreateIfMissing{Name: "Read", Parent: ParentSource}}
	readRule := func(id, name string) automation.Rule {
		return automation.Rule{ID: "r-" + id, Enabled: true,
			When: automation.When{Subject: automation.SubjectDocument, In: []automation.Place{folder(id, name)},
				Trigger:    trig(TriggerFinishedReading, P{MinPages: 2, SettleHours: 6}),
				OrTriggers: []automation.Trigger{trig(TriggerTagIs, P{Tag: "read"})}},
			Conditions: []automation.Condition{kindIs(ScopePdfEpub)},
			Actions:    []automation.Action{{Type: automation.ActionMove, To: readTo}}}
	}
	return &automation.Config{Rules: []automation.Rule{
		deleteRule(),
		{ID: "x", Enabled: true, When: automation.When{Subject: automation.SubjectPage, In: []automation.Place{folder(RootID, "My files")},
			Trigger: trig(automation.TriggerPageTagged, P{Tag: "extract"})},
			Actions: []automation.Action{{Type: automation.ActionExtractTasks}}},
		{ID: "w", Enabled: true, When: automation.When{Subject: automation.SubjectPage,
			In:      []automation.Place{folder("nb", "Notebooks"), folder("in", "Inbox")},
			Trigger: trig(automation.TriggerPageChanged, P{})},
			Conditions: []automation.Condition{kindIs(automation.KindNotebooks)},
			Actions:    []automation.Action{{Type: automation.ActionExtractTasks}}},
		readRule("rr", "Research Reports"),
		readRule("fr", "Financial Reports"),
		{ID: "n", Enabled: true, When: automation.When{Subject: automation.SubjectDocument, In: []automation.Place{folder(RootID, "My files")},
			Trigger: trig(automation.TriggerCreatedTime, P{Compare: "within", Amount: 24, Unit: "hours"})},
			Actions: []automation.Action{{Type: automation.ActionAddTag, Tag: "new"}}},
		{ID: "off", Enabled: false, When: automation.When{Subject: automation.SubjectDocument, In: []automation.Place{folder(RootID, "My files")},
			Trigger: trig(TriggerTagIs, P{Tag: "disabled"})},
			Actions: []automation.Action{{Type: automation.ActionTrash}}},
	}}
}

func TestTagHelpRows(t *testing.T) {
	rows, auto := TagHelp(tagHelpConfig())
	type got struct{ tag, where, limits string }
	var g []got
	for _, r := range rows {
		g = append(g, got{r.Tag, strings.Join(r.Where, ", "), r.Limits})
	}
	want := []got{
		{"#delete", "anywhere", "not pinned"},
		{"#read", "Research Reports, Financial Reports", "PDFs & books, not pinned"},
		{"#extract", "anywhere", ""},
		{"-inbox", "pages that send to-dos", ""},
		{"no automation", "anywhere", ""},
	}
	if !reflect.DeepEqual(g, want) {
		t.Errorf("rows\n got %+v\nwant %+v", g, want)
	}
	if !strings.Contains(rows[1].What, "<b>Read</b> inside that folder") || !strings.Contains(rows[1].What, "when you finish reading") {
		t.Errorf("#read what: %q", rows[1].What)
	}
	wantAuto := []string{
		"Changed pages in Notebooks, Inbox (notebooks) go to the vault with their to-dos.",
		"<b>new</b> is added when a document arrives (first 24 hours).",
	}
	if !reflect.DeepEqual(auto, wantAuto) {
		t.Errorf("auto\n got %q\nwant %q", auto, wantAuto)
	}
}

func TestTagHelpHTMLIsPlainRichText(t *testing.T) {
	cfg := tagHelpConfig()
	cfg.Rules[0].When.Trigger.Tag = "<b>x</b>"
	h := TagHelpHTML(cfg)
	for _, bad := range []string{"class=", "<style", "<script", "<b>x</b>"} {
		if strings.Contains(h, bad) {
			t.Errorf("html contains %q", bad)
		}
	}
	if !strings.Contains(h, "#&lt;b&gt;x&lt;/b&gt;") || !strings.Contains(h, "MOVE OR DELETE THE DOCUMENT") {
		t.Errorf("html: %s", h)
	}
}
