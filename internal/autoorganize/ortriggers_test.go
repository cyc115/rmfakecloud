package autoorganize

import (
	"reflect"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// readOrTagRule seedRule ("finished reading" in Research Reports -> Read) that also
// fires on the doc tag "read"
func readOrTagRule() automation.Rule {
	r := seedRule()
	r.When.OrTriggers = []automation.Trigger{trig(TriggerTagIs, P{Tag: "read"})}
	return r
}

func TestOrTriggersAnyOneMatches(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	opened := ms(now.Add(-24 * time.Hour))
	r := readOrTagRule()
	cases := []struct {
		name     string
		lastPage int
		tags     []string
		want     bool
	}{
		{"finished, untagged", 9, nil, true},
		{"tagged read, half way", 4, []string{"read"}, true},
		{"both", 9, []string{"Read"}, true},
		{"neither", 4, []string{"other"}, false},
	}
	var inputs map[string]string
	for _, c := range cases {
		d := &DocSnap{ID: "d", LastOpened: opened, LastOpenedPage: c.lastPage}
		ci := &ContentInfo{FileType: "pdf", PageCount: 10, LastOpenedPage: c.lastPage, Tags: c.tags}
		ev := evalRule(&r, d, ci, nil, now)
		if ev.match != c.want || ev.skip != "" {
			t.Errorf("%s: match=%v skip=%q, want match=%v (%s)", c.name, ev.match, ev.skip, c.want, ev.detail)
		}
		// every trigger is evaluated, so the hold key has the same fields whichever matched
		if inputs != nil && !reflect.DeepEqual(keys(inputs), keys(ev.inputs)) {
			t.Errorf("%s: input keys %v, want %v", c.name, keys(ev.inputs), keys(inputs))
		}
		inputs = ev.inputs
	}
}

func TestOrTriggersMatchBeatsSkip(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r := readOrTagRule()
	// pageCount 0: finished_reading skips (not synced yet)
	d := &DocSnap{ID: "d", LastOpened: ms(now.Add(-24 * time.Hour))}
	ev := evalRule(&r, d, &ContentInfo{FileType: "pdf", Tags: []string{"read"}}, nil, now)
	if !ev.match || ev.skip != "" {
		t.Errorf("the tag must match although the reading trigger skips: %+v", ev)
	}
	ev = evalRule(&r, d, &ContentInfo{FileType: "pdf"}, nil, now)
	if ev.match || ev.skip != SkipPageCountUnknown {
		t.Errorf("no trigger matches: the skip must be reported, not a plain no-match: %+v", ev)
	}
}

func TestOrTriggersPageBasedSkipsNotebooksQuietly(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r := readOrTagRule()
	r.Conditions = nil // any kind: notebooks too
	d := &DocSnap{ID: "d", LastOpened: ms(now.Add(-24 * time.Hour))}
	ev := evalRule(&r, d, &ContentInfo{FileType: "notebook", PageCount: 0}, nil, now)
	if ev.match || ev.skip != "" {
		t.Errorf("an untagged notebook is a plain no-match, not a page-count skip: %+v", ev)
	}
	ev = evalRule(&r, d, &ContentInfo{FileType: "notebook", PageCount: 0, Tags: []string{"read"}}, nil, now)
	if !ev.match {
		t.Errorf("a notebook tagged read must match: %+v", ev)
	}
}

func TestOrTriggersConditionsStillAND(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r := readOrTagRule()
	r.Conditions = append(r.Conditions, cond(automation.CondTagIsNot, P{Tag: "keep"}))
	d := &DocSnap{ID: "d", LastOpened: ms(now.Add(-24 * time.Hour))}
	ev := evalRule(&r, d, &ContentInfo{FileType: "pdf", PageCount: 10, Tags: []string{"read", "keep"}}, nil, now)
	if ev.match {
		t.Errorf("a failing condition must stop the rule whichever trigger matched: %+v", ev)
	}
}

func TestOrTriggersCheckDocPinnedAndKind(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r := readOrTagRule()
	locate := func(string) Location { return LocAtRoot }
	d := &DocSnap{ID: "d", Parent: "rr", LastOpened: ms(now.Add(-24 * time.Hour))}
	got := checkDoc(&r, "dst", d, &ContentInfo{FileType: "pdf", PageCount: 10, Tags: []string{"read"}}, nil, locate, now)
	if got.skip != "" || !got.ev.match {
		t.Errorf("tagged PDF in the source: want a match, got %+v", got)
	}
	// the rule keeps its kind_is pdf_epub condition: a tagged notebook is out of scope
	got = checkDoc(&r, "dst", d, &ContentInfo{FileType: "notebook", Tags: []string{"read"}}, nil, locate, now)
	if got.skip != SkipOutOfScope {
		t.Errorf("kind_is still guards: %+v", got)
	}
	// a pinned_is trigger among the or-triggers lifts the pinned guard
	r.When.OrTriggers = append(r.When.OrTriggers, trig(TriggerPinnedIs, P{Pinned: true}))
	if r.SkipsPinned() {
		t.Error("a pinned_is or-trigger must lift the pinned guard")
	}
}

func TestOrTriggersHaveNoV1Form(t *testing.T) {
	r := readOrTagRule()
	if _, reason := ruleToV1(&r); reason == "" {
		t.Error("rollback must refuse a rule with or-triggers")
	}
}

func TestOrTriggerTagIsKnown(t *testing.T) {
	cfg := &automation.Config{Rules: []automation.Rule{readOrTagRule()}}
	if got := KnownTags(cfg, nil, nil); !reflect.DeepEqual(got, []string{NormalizeTag("read")}) {
		t.Errorf("KnownTags = %v", got)
	}
}
