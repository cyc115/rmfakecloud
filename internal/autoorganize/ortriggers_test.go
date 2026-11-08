package autoorganize

import (
	"context"
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

// a page tag fires the tag_is trigger (a handwritten #read on one page moves the
// document); the tag_is condition stays document-level
func TestTagIsTriggerMatchesPageTag(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	r := readOrTagRule()
	d := &DocSnap{ID: "d", LastOpened: ms(now.Add(-24 * time.Hour)), LastOpenedPage: 1}
	pageTagged := &ContentInfo{FileType: "pdf", PageCount: 10, LastOpenedPage: 1,
		PageTags: map[string]map[string]int64{"p2": {"read": 5}, "p1": {"other": 1}}}
	ev := evalRule(&r, d, pageTagged, nil, now)
	if !ev.match || ev.skip != "" {
		t.Fatalf("page tag read must fire the rule: %+v", ev)
	}
	if ev.inputs["page_tags"] != "p2" {
		t.Errorf("page_tags input = %q, want p2", ev.inputs["page_tags"])
	}
	// no page carries the tag: inputs keep their old keys (holds stay valid)
	plain := &ContentInfo{FileType: "pdf", PageCount: 10, LastOpenedPage: 1, Tags: []string{"read"}}
	ev = evalRule(&r, d, plain, nil, now)
	if _, ok := ev.inputs["page_tags"]; ok || !ev.match {
		t.Errorf("doc tag only: match=%v inputs=%v", ev.match, ev.inputs)
	}
	// the condition does not look at pages
	p := automation.Params{Tag: "read"}
	if conditionEvaluators[automation.CondTagIs](&p, d, pageTagged, nil, now).match {
		t.Error("tag_is condition must stay document-level")
	}
	if !conditionEvaluators[automation.CondTagIsNot](&p, d, pageTagged, nil, now).match {
		t.Error("tag_is_not condition must stay document-level")
	}
}

// end to end: rm-pages writes the page tag "read" on a half-read PDF; the write
// kicks a sweep (no waiting for the timer) and the read-or-tag rule moves it
func TestApplyPageTagKicksSweepThatMoves(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.PDF("d", "GlitchTip Errors", "rr", 14, 1, now.Add(-time.Hour))
	if err := ft.SetPage("d", "p1", []byte("rm-p1")); err != nil {
		t.Fatal(err)
	}
	ft.Sync()
	svc, _, _ := newService(t, storage)
	svc.Debounce = 10 * time.Millisecond
	svc.IntervalUnit = time.Hour // only the kick can sweep during the test
	saveConfig(t, svc, uid, ModeLive, readOrTagRule())
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.Wait() }()
	svc.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	time.Sleep(30 * time.Millisecond) // the loop is up and waiting on its (hour-long) timer
	if p := ft.ServerMetaRaw("d")["parent"]; p != "rr" {
		t.Fatalf("moved before the tag: parent %v", p)
	}
	added, err := svc.ApplyPageTags(uid, "d", "p1", []string{"read"})
	if err != nil || len(added) != 1 {
		t.Fatalf("apply: %v %v", added, err)
	}
	// the run entry is saved when the sweep ends; reading the tablet while the
	// sweep holds its lock would fail, so wait for the entry first
	var st *State
	for {
		if cur, err := svc.Store().LoadState(uid); err == nil && len(cur.Runs) > 0 {
			st = cur
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no sweep after the page tag")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := ft.ServerMetaRaw("d")["parent"]; p != "read" {
		t.Fatalf("not moved after the page tag: parent %v, runs %+v", p, st.Runs)
	}
	if last := st.Runs[len(st.Runs)-1]; last.Trigger != TriggerTabletSync {
		t.Errorf("moving run trigger %q, want %q", last.Trigger, TriggerTabletSync)
	}
}
