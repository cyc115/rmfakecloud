package autoorganize

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestMergeEvalANDFirstSkip(t *testing.T) {
	yes := evaluation{match: true, inputs: map[string]string{"a": "1"}, detail: "a"}
	no := evaluation{inputs: map[string]string{"b": "2"}, detail: "b"}
	if !mergeEval(yes, yes).match || mergeEval(yes, no).match || mergeEval(no, yes).match {
		t.Error("match must be the AND of all evaluations")
	}
	first := evaluation{skip: SkipTimeUnknown, skipDetail: "first", inputs: map[string]string{"s": "1"}}
	second := evaluation{skip: SkipInvalidRule, skipDetail: "second", inputs: map[string]string{"t": "1"}, detail: "later"}
	got := mergeEval(yes, first, second, yes)
	if got.match || got.skip != SkipTimeUnknown || got.skipDetail != "first" {
		t.Errorf("first skip must win: %+v", got)
	}
	if _, ok := got.inputs["t"]; ok || got.detail != "a" {
		t.Errorf("evaluations after the skip must not be merged: %+v", got)
	}
	// ruling a: a non-match stops the merge, so a later skip cannot outrank it
	got = mergeEval(yes, no, first)
	if got.match || got.skip != "" || got.detail != "a · b" {
		t.Errorf("a skip after a non-match must not be merged: %+v", got)
	}
	if _, ok := got.inputs["s"]; ok {
		t.Errorf("inputs after the non-match merged: %+v", got.inputs)
	}
	// one evaluation passes through unchanged (a rule with no conditions)
	for _, ev := range []evaluation{yes, no, first, {skip: SkipOutOfScope, skipDetail: "x"}} {
		if m := mergeEval(ev); !reflect.DeepEqual(m, ev) {
			t.Errorf("mergeEval(%+v) = %+v", ev, m)
		}
	}
}

func TestMergeEvalInputsUnionDetailJoin(t *testing.T) {
	got := mergeEval(
		evaluation{match: true, inputs: map[string]string{"a": "1", "shared": "s"}, detail: "first"},
		evaluation{match: true, inputs: map[string]string{"b": "2", "shared": "s"}, detail: "second"},
		evaluation{match: true, inputs: map[string]string{"c": "3"}, detail: "third"},
	)
	want := map[string]string{"a": "1", "b": "2", "c": "3", "shared": "s"}
	if !reflect.DeepEqual(got.inputs, want) || got.detail != "first · second · third" || !got.match {
		t.Errorf("merged = %+v", got)
	}
}

// notOpenedRule not_opened_for_days 30, with a min_age_days condition when
// minAge (the v1 minAgeEnabled, as MigrateV1 maps it)
func notOpenedRule(minAge bool, minAgeDays int) automation.Rule {
	r := moveRule("r", "src", "dst", trig(TriggerNotOpened, P{Days: 30}))
	if minAge {
		r.Conditions = append(r.Conditions, cond(automation.CondMinAgeDays, P{Days: minAgeDays}))
	}
	return r
}

func TestNotOpenedInputsKeepAgeAnchor(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	d := &DocSnap{ID: "d", LastOpened: ms(now.Add(-40 * 24 * time.Hour)), CreatedTime: ms(now.Add(-50 * 24 * time.Hour))}
	ci := &ContentInfo{FileType: "pdf"}

	off := notOpenedRule(false, 7)
	on := notOpenedRule(true, 7)
	evOff := evalRule(&off, d, ci, nil, now)
	evOn := evalRule(&on, d, ci, nil, now)
	for name, ev := range map[string]evaluation{"without": evOff, "with": evOn} {
		if k := keys(ev.inputs); !reflect.DeepEqual(k, []string{"ageAnchor", "lastOpened"}) {
			t.Errorf("%s min age: input keys %v", name, k)
		}
		if !ev.match {
			t.Errorf("%s min age: no match", name)
		}
	}
	if !reflect.DeepEqual(evOff.inputs, evOn.inputs) {
		t.Errorf("inputs differ with min age: %v vs %v", evOff.inputs, evOn.inputs)
	}
	if evOff.detail != "last opened 40 days ago" {
		t.Errorf("detail without min age = %q", evOff.detail)
	}
	if evOn.detail != "last opened 40 days ago · 50 days old (createdTime)" {
		t.Errorf("detail with min age = %q", evOn.detail)
	}
	old := notOpenedRule(true, 60)
	if ev := evalRule(&old, d, ci, nil, now); ev.match || ev.detail != evOn.detail {
		t.Errorf("a min age of 60 days must not match a 50 day old document: %+v", ev)
	}
	never := &DocSnap{ID: "n", CreatedTime: d.CreatedTime}
	if ev := evalRule(&on, never, ci, nil, now); !ev.match || ev.detail != "never opened · 50 days old (createdTime)" {
		t.Errorf("never opened: %+v", ev)
	}
}

func TestTagIsNotNegatesTagIs(t *testing.T) {
	now := time.Now()
	p := automation.Params{Tag: "Done"}
	for _, tags := range [][]string{{"later", "done"}, {"later"}, nil} {
		ci := &ContentInfo{Tags: tags}
		is := conditionEvaluators[automation.CondTagIs](&p, &DocSnap{}, ci, nil, now)
		not := conditionEvaluators[automation.CondTagIsNot](&p, &DocSnap{}, ci, nil, now)
		if !reflect.DeepEqual(is.inputs, not.inputs) || is.match == not.match || is.skip != "" || not.skip != "" {
			t.Errorf("tags %v: tag_is %+v, tag_is_not %+v", tags, is, not)
		}
	}
}

func TestConditionSharesTriggerEvaluator(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	d := &DocSnap{ID: "d", Name: "WSJ Weekend", Pinned: true,
		LastOpened: ms(now.Add(-40 * 24 * time.Hour)), CreatedTime: ms(now.Add(-50 * 24 * time.Hour))}
	ci := &ContentInfo{FileType: "pdf", Tags: []string{"Done"}}
	cases := map[string]automation.Params{
		automation.CondTagIs:       {Tag: "done"},
		automation.CondNameMatches: {Pattern: "wsj *"},
		automation.CondPinnedIs:    {Pinned: true},
		automation.CondNotOpened:   {Days: 30},
	}
	for typ, p := range cases {
		trig := evaluators[typ](&p, d, ci, nil, now)
		cond := conditionEvaluators[typ](&p, d, ci, nil, now)
		if !reflect.DeepEqual(trig, cond) {
			t.Errorf("%s: trigger %+v, condition %+v", typ, trig, cond)
		}
	}
}

// TestCatalogueComplete the evaluator half: every catalogue trigger and every
// document condition except kind_is (checkDoc runs evalKind) has an evaluator,
// and there is no evaluator the catalogue does not know
func TestCatalogueComplete(t *testing.T) {
	for typ := range automation.Triggers {
		if evaluators[typ] == nil {
			t.Errorf("trigger %s has no evaluator", typ)
		}
	}
	for typ := range evaluators {
		if automation.Triggers[typ] == nil {
			t.Errorf("evaluator %s is not in the trigger catalogue", typ)
		}
	}
	for typ, s := range automation.Conditions {
		doc := false
		for _, sub := range s.Subjects {
			doc = doc || sub == automation.SubjectDocument
		}
		if doc && typ != automation.CondKindIs && conditionEvaluators[typ] == nil {
			t.Errorf("document condition %s has no evaluator", typ)
		}
	}
	for typ := range conditionEvaluators {
		if automation.Conditions[typ] == nil {
			t.Errorf("condition evaluator %s is not in the catalogue", typ)
		}
	}
}

// TestTriggerNonMatchShortCircuits (ruling a): a trigger that does not match
// gives the trigger's own evaluation; its conditions are not evaluated, so a
// condition that would skip (a bad pattern) cannot outrank the non-match
func TestTriggerNonMatchShortCircuits(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	d := &DocSnap{ID: "d", Name: "Paper", LastOpened: ms(now.Add(-2 * 24 * time.Hour)), CreatedTime: ms(now.Add(-50 * 24 * time.Hour))}
	ci := &ContentInfo{FileType: "pdf"}
	r := notOpenedRule(false, 0)
	r.Conditions = append(r.Conditions, cond(automation.CondNameMatches, P{Pattern: "/[/"}))
	alone := notOpenedRule(false, 0)
	got, want := evalRule(&r, d, ci, nil, now), evalRule(&alone, d, ci, nil, now)
	if got.match || got.skip != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("non-matching trigger: got %+v, want the trigger's evaluation %+v", got, want)
	}
	// a condition non-match stops too: the bad pattern after it is not reached
	r.Conditions = append([]automation.Condition{cond(automation.CondTagIs, P{Tag: "keep"})}, r.Conditions...)
	d.LastOpened = ms(now.Add(-40 * 24 * time.Hour))
	if ev := evalRule(&r, d, ci, nil, now); ev.match || ev.skip != "" || ev.detail != "last opened 40 days ago · tagged “keep”" {
		t.Fatalf("condition non-match then skip: %+v", ev)
	}
	// with every earlier test matching, the bad pattern is a skip, never a guess
	ci.Tags = []string{"keep"}
	if ev := evalRule(&r, d, ci, nil, now); ev.match || ev.skip != SkipInvalidRule {
		t.Fatalf("bad pattern reached: %+v", ev)
	}
}

func TestEvalKindDetailV1(t *testing.T) {
	cases := []struct {
		kind, fileType string
		skip           bool
		detail         string
	}{
		{ScopeAll, "png", false, ""},
		{ScopePdfEpub, "EPUB", false, ""},
		{ScopeNotebooks, "", false, ""},
		{ScopePdfEpub, "notebook", true, "notebook, rule applies to PDF & EPUB"},
		{ScopeNotebooks, "PDF", true, "pdf, rule applies to notebooks"},
		{ScopePdfEpub, "png", true, "file type “png”, rule applies to PDF & EPUB"},
	}
	for _, c := range cases {
		skip, detail := evalKind(c.kind, &ContentInfo{FileType: c.fileType})
		if skip != c.skip || detail != c.detail {
			t.Errorf("evalKind(%s, %s) = %v %q, want %v %q", c.kind, c.fileType, skip, detail, c.skip, c.detail)
		}
	}
}
