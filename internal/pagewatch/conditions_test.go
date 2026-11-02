package pagewatch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

// readGolden the pinned engines-golden.json
func readGolden(t *testing.T) enginesGolden {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "engines-golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g enginesGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestStateSurvivesMigration (never miss a note): the page watch state the v1
// server wrote after sweep 3 of the golden scenario, read with the migrated
// config, gives the golden sweeps 4 and 5: no re-send of what was delivered,
// and the next edit under the golden trace id.
func TestStateSurvivesMigration(t *testing.T) {
	e := goldenTree(t)
	for step := 1; step <= 3; step++ {
		pageWatchStep(e, step)
		e.now = e.now.Add(time.Minute)
	}
	e.cfg = goldenConfig(t)
	b, err := os.ReadFile(filepath.Join("testdata", "pagewatch-state-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	e.state = NewState()
	if err := json.Unmarshal(b, e.state); err != nil {
		t.Fatal(err)
	}
	golden := readGolden(t)
	for step := 4; step <= 5; step++ {
		pageWatchStep(e, step)
		p := e.sweep()
		want := golden.Sweeps[step-1]
		if got := (goldenSweep{Pipeline: p.Pipeline, State: e.state}); jsonOf(t, got) != jsonOf(t, want) {
			t.Fatalf("sweep %d differs from the golden:\n got %s\nwant %s", step, jsonOf(t, got), jsonOf(t, want))
		}
	}
	if s4 := golden.Sweeps[3].Pipeline; len(s4) != 0 {
		t.Fatalf("golden sweep 4 re-sends %+v", s4)
	}
	if s5 := golden.Sweeps[4].Pipeline; len(s5) != 1 || s5[0].Kind != KindEdit || s5[0].TraceID == "" {
		t.Fatalf("golden sweep 5 = %+v, want one edit", s5)
	}
}

// TestSweepStateSurvivesMigration (never miss a note): the sweeper state the
// v1 server wrote (sweep-state-v1.json: an undo hold and the created Done
// folder), read with the migrated config, gives the golden plan2: the hold
// still applies (same source, same inputs) and the created folder resolves.
func TestSweepStateSurvivesMigration(t *testing.T) {
	e := goldenTree(t)
	fixed(e.ft.Folder(gDoneFolder, "Done", gReading))
	b, err := os.ReadFile(filepath.Join("testdata", "sweep-state-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := autoorganize.NewState()
	if err := json.Unmarshal(b, st); err != nil {
		t.Fatal(err)
	}
	snap, content := e.snapshot()
	p2 := autoorganize.BuildPlan(autoorganize.PlanInput{Snap: snap, Config: goldenConfig(t), State: st, Content: content, Now: goldenNow})
	if want := readGolden(t).Plan2; jsonOf(t, p2) != jsonOf(t, want) {
		t.Fatalf("plan differs from golden plan2:\n got %s\nwant %s", jsonOf(t, p2), jsonOf(t, want))
	}
	if len(p2.ClearHolds) != 0 || p2.Rules[0].Held != 1 || p2.Rules[0].DestID != gDoneFolder {
		t.Fatalf("hold or created folder lost: %+v %+v", p2.ClearHolds, p2.Rules[0])
	}
}

// TestPageRuleMultiConditionAND page_changed in the root AND kind_is notebooks
// AND name_matches "Journal*": only Journal's pages produce events. Renamed
// away it leaves with no events; renamed back it rejoins (R12, R38): pages
// written before the rule's last look are transcribe_only, newer ones edits.
func TestPageRuleMultiConditionAND(t *testing.T) {
	e := newEnv(t)
	r := e.rule("r1", folderWatch(autoorganize.RootID))
	r.Conditions = []automation.Condition{
		cond(automation.CondKindIs, automation.Params{Kind: automation.KindNotebooks}),
		cond(automation.CondNameMatches, automation.Params{Pattern: "Journal*"}),
	}
	e.ft.Notebook("j", "Journal 2026", "")
	e.page("j", "p1", "strokes_a.rm")
	e.ft.Notebook("n", "Notes", "")
	e.page("n", "p1", "strokes_a.rm")
	e.ft.PDF("pdf", "Journal paper", "", 3, 0, e.now)
	e.page("pdf", "p1", "strokes_a.rm")
	wantEvents(t, "first", e.sweep().Pipeline, "transcribe_only j/p1 r1")

	e.page("j", "p1", "strokes_ab.rm")
	e.page("n", "p1", "strokes_ab.rm")
	e.page("pdf", "p1", "strokes_ab.rm")
	wantEvents(t, "edits", e.sweep().Pipeline, "edit j/p1 r1")

	if err := e.ft.Rename("j", "Diary"); err != nil {
		t.Fatal(err)
	}
	e.page("j", "p1", "text_note.rm")
	p := e.sweep()
	wantEvents(t, "renamed away", p.Pipeline)
	if rs := ruleState(e.state, "r1"); rs.Members["j"] || len(rs.Baselines) != 0 {
		t.Fatalf("the renamed doc did not leave: %+v", rs)
	}

	lastLook := e.now
	if err := e.ft.Rename("j", "Journal 2026"); err != nil {
		t.Fatal(err)
	}
	e.page("j", "p2", "text_todo.rm")
	// written after the rule's last look, and older than MinAge at the sweep
	e.at[e.pageHash("j", "p2")] = lastLook.Add(5 * time.Minute)
	e.now = e.now.Add(20 * time.Minute)
	p = e.sweep()
	wantEvents(t, "renamed back", p.Pipeline, "transcribe_only j/p1 r1", "edit j/p2 r1")
	if rs := ruleState(e.state, "r1"); !rs.Members["j"] {
		t.Fatalf("rejoin not complete: %+v", rs)
	}
}

// pageTaggedScenario the Task 1 page_tagged scenario (TestPageTaggedTraceIDsGolden)
// with rule r; it returns every sweep's pipeline events and rule state
func pageTaggedScenario(t *testing.T, r automation.Rule) ([][]Event, []string) {
	t.Helper()
	e := newEnv(t)
	e.cfg.Rules = append(e.cfg.Rules, r)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	var evs [][]Event
	var states []string
	record := func(p Plan) {
		evs = append(evs, p.Pipeline)
		states = append(states, jsonOf(t, e.state.Rules[r.ID]))
	}
	e.tag("nb", "p1", "To-Do", e.now.Add(-24*time.Hour))
	record(e.sweep())
	e.tag("nb", "p2", "To-Do", e.now)
	record(e.sweep())
	e.page("nb", "p1", "strokes_ab.rm")
	record(e.sweep())
	e.untag("nb", "p2", "To-Do")
	record(e.sweep())
	return evs, states
}

// TestPageTagIsConditionEqualsPageTagged (P4): page_changed AND page_tag_is X
// runs exactly like page_tagged X: the same events (trace ids included) and
// the same rule state, RuleState.Trigger "page_tagged:x" included
func TestPageTagIsConditionEqualsPageTagged(t *testing.T) {
	tagged := pageRule("t1")
	tagged.When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: "To-Do"}}
	viaCond := pageRule("t1", folderWatch(autoorganize.RootID))
	viaCond.Conditions = []automation.Condition{cond(automation.CondPageTagIs, automation.Params{Tag: "to_do"})}

	wantEvs, wantStates := pageTaggedScenario(t, tagged)
	gotEvs, gotStates := pageTaggedScenario(t, viaCond)
	kinds := [][]string{{"transcribe_only nb/p1 t1"}, {"edit nb/p2 t1"}, {"edit nb/p1 t1"}, {}}
	for i := range kinds {
		wantEvents(t, "page_tagged", wantEvs[i], kinds[i]...)
		if !reflect.DeepEqual(gotEvs[i], wantEvs[i]) {
			t.Fatalf("sweep %d events differ:\n got %+v\nwant %+v", i+1, gotEvs[i], wantEvs[i])
		}
		if gotStates[i] != wantStates[i] {
			t.Fatalf("sweep %d rule state differs:\n got %s\nwant %s", i+1, gotStates[i], wantStates[i])
		}
	}
	var rs RuleState
	if err := json.Unmarshal([]byte(gotStates[3]), &rs); err != nil || rs.Trigger != "page_tagged:to do" {
		t.Fatalf("state trigger = %q (%v)", rs.Trigger, err)
	}
}

func TestStateTriggerKeyTwoTags(t *testing.T) {
	two := pageRule("r")
	two.When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: "B"}}
	two.Conditions = []automation.Condition{{Type: automation.CondPageTagIs, Params: automation.Params{Tag: " a "}}}
	if got := stateTrigger(&two); got != "page_tagged:a\x1fb" {
		t.Errorf("two tags: %q", got)
	}
	one := pageRule("r")
	one.When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: "a"}}
	if got := stateTrigger(&one); got != "page_tagged:a" {
		t.Errorf("one tag: %q", got)
	}
	viaCond := pageRule("r", folderWatch(autoorganize.RootID))
	viaCond.Conditions = []automation.Condition{{Type: automation.CondPageTagIs, Params: automation.Params{Tag: "A"}}}
	if got := stateTrigger(&viaCond); got != "page_tagged:a" {
		t.Errorf("page_tag_is: %q", got)
	}
	changed := pageRule("r", folderWatch(autoorganize.RootID))
	if got := stateTrigger(&changed); got != "" {
		t.Errorf("page_changed: %q", got)
	}
	// a blank tag keeps the v1 key and the tagged path, where no page counts
	blank := pageRule("r")
	blank.When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: " "}}
	if got := stateTrigger(&blank); got != "page_tagged:" {
		t.Errorf("blank tag: %q", got)
	}
}

// TestBlankPageTagMatchesNoPage a page_tagged rule whose tag normalises to
// nothing (only a hand edit or an unvalidated v1 rule) sends nothing; it never
// widens to every page of the tree
func TestBlankPageTagMatchesNoPage(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "  ")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))
	wantEvents(t, "first", e.sweep().Pipeline)
	e.page("nb", "p1", "strokes_ab.rm")
	wantEvents(t, "edit", e.sweep().Pipeline)
}

func TestSaveNotesVsExtractTasksEventFields(t *testing.T) {
	e := newEnv(t)
	notes := e.rule("notes", folderWatch(autoorganize.RootID))
	notes.Actions[0].Type = automation.ActionSaveNotes
	e.rule("tasks", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	p := e.sweep()
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p1 notes", "transcribe_only nb/p1 tasks")
	for _, ev := range p.Pipeline {
		if ev.Prompt != "prompt-"+ev.RuleID || ev.Model != "model-"+ev.RuleID {
			t.Errorf("%s: prompt %q model %q", ev.RuleID, ev.Prompt, ev.Model)
		}
		switch ev.RuleID {
		case "notes":
			if ev.Tasks == nil || *ev.Tasks {
				t.Errorf("save_notes: tasks %v, want false", ev.Tasks)
			}
		case "tasks":
			if ev.Tasks != nil {
				t.Errorf("extract_tasks: tasks %v, want absent", *ev.Tasks)
			}
		}
	}
}

// TestBothEnginesIgnoreOtherSubject a page rule never appears in the move
// plan; a document rule never gets page watch state
func TestBothEnginesIgnoreOtherSubject(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("f", "Inbox", "")
	e.ft.Folder("g", "Out", "")
	e.ft.Notebook("nb", "Notes", "f")
	e.page("nb", "p1", "strokes_a.rm")
	doc := automation.Rule{ID: "doc", Enabled: true,
		When: automation.When{Subject: automation.SubjectDocument, In: []automation.Place{folderWatch("f")},
			Trigger: automation.Trigger{Type: automation.TriggerNameMatches, Params: automation.Params{Pattern: "*"}}},
		Conditions: []automation.Condition{},
		Actions:    []automation.Action{{Type: automation.ActionMove, To: &automation.Destination{FolderRef: automation.FolderRef{ID: "g"}}}}}
	e.cfg.Rules = append(e.cfg.Rules, doc)
	e.rule("page", folderWatch("f"))
	if err := e.cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	p := e.sweep()
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p1 page")
	if _, ok := e.state.Rules["doc"]; ok {
		t.Fatalf("document rule has page watch state: %+v", e.state.Rules)
	}
	snap, content := e.snapshot()
	plan := autoorganize.BuildPlan(autoorganize.PlanInput{Snap: snap, Config: e.cfg, State: autoorganize.NewState(), Content: content, Now: e.now})
	if len(plan.Rules) != 1 || plan.Rules[0].RuleID != "doc" || plan.Rules[0].Number != 1 {
		t.Fatalf("plan rules = %+v, want the document rule only", plan.Rules)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].DocID != "nb" || plan.Moves[0].RuleID != "doc" {
		t.Fatalf("plan moves = %+v", plan.Moves)
	}
}

// TestMigratedPageTaggedWholeTreeKeepsState (never miss a note): a v1
// page_tagged rule with no watch entries and the state the v1 page watch
// wrote for it. After migration the rule keeps its state key, so nothing is
// reset: unchanged pages send nothing, and the next edit has the stored
// baseline as its before.
func TestMigratedPageTaggedWholeTreeKeepsState(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("f", "Deep", "")
	e.ft.Notebook("nb", "Notes", "f")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_note.rm")
	e.tag("nb", "p1", "To-Do", e.now.Add(-48*time.Hour))
	v1 := &autoorganize.V1Config{Revision: 3, Settings: autoorganize.Settings{Enabled: true, Mode: automation.ModeLive, IntervalMinutes: 15}}
	if err := json.Unmarshal([]byte(`[{"id": "t1", "enabled": true, "scope": "all",
		"trigger": {"type": "page_tagged", "tag": "To-Do", "settleHours": 6, "pinned": true},
		"source": {"id": ""}, "destination": {"id": ""},
		"action": {"type": "pipeline", "prompt": "P", "model": "M"}}]`), &v1.Rules); err != nil {
		t.Fatal(err)
	}
	e.cfg = migrated(t, v1)
	if in := e.cfg.Rules[0].When.In; len(in) != 0 {
		t.Fatalf("whole tree migrated to places %+v", in)
	}

	// what the v1 page watch held after delivering p1
	last := e.now.Add(-15 * time.Minute)
	p1 := e.pageHash("nb", "p1")
	rs := newRuleState()
	rs.Trigger = "page_tagged:to do"
	rs.LastActive = last
	rs.TagWatched["nb"] = true
	rs.Baselines["nb/p1"] = p1
	e.state.Rules["t1"] = rs
	e.state.Seen["nb"] = last.Add(-time.Hour)
	before := stateJSON(t, e.state)

	p := e.sweep()
	wantEvents(t, "after migration", p.Pipeline)
	got := ruleState(e.state, "t1")
	if got.Trigger != rs.Trigger || !reflect.DeepEqual(got.Baselines, rs.Baselines) || !got.TagWatched["nb"] {
		t.Fatalf("rule state reset by the migration:\nbefore %s\nafter  %s", before, stateJSON(t, e.state))
	}
	e.page("nb", "p1", "strokes_ab.rm")
	p = e.sweep()
	wantEvents(t, "edit", p.Pipeline, "edit nb/p1 t1")
	if p.Pipeline[0].BeforeHash != p1 || p.Pipeline[0].Prompt != "P" || p.Pipeline[0].Model != "M" {
		t.Fatalf("edit = %+v, want before %s", p.Pipeline[0], p1)
	}
}

// TestPageConditionSkipLeavesDocAlone a condition the page watch cannot
// evaluate (a bad pattern) leaves the doc alone: no events, no leave, its
// baselines kept, and a skip note
func TestPageConditionSkipLeavesDocAlone(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	wantEvents(t, "first", e.sweep().Pipeline, "transcribe_only nb/p1 r1")
	before := jsonOf(t, ruleState(e.state, "r1").Baselines)

	r := &e.cfg.Rules[0]
	r.Conditions = []automation.Condition{cond(automation.CondNameMatches, automation.Params{Pattern: "/[/"})}
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.sweep()
	wantEvents(t, "bad pattern", p.Pipeline)
	if !hasSkip(p, SkipNote{DocID: "nb", Reason: SkipCondition}) || p.SkipRules[SkipNote{DocID: "nb", Reason: SkipCondition}] != "r1" {
		t.Fatalf("skips = %+v", p.Skipped)
	}
	rs := ruleState(e.state, "r1")
	if !rs.Members["nb"] || jsonOf(t, rs.Baselines) != before {
		t.Fatalf("the doc left the rule: %+v", rs)
	}
	// fixed: the edit made while skipped is delivered, nothing is missed
	r.Conditions = nil
	wantEvents(t, "fixed", e.sweep().Pipeline, "edit nb/p1 r1")
}

// TestPageWatchFailsClosedOnBadConfig (Review Focus 3): a corrupt
// automation.json, or a corrupt auto-organize.json with no automation.json,
// fails the run and leaves the page watch state byte-unchanged
func TestPageWatchFailsClosedOnBadConfig(t *testing.T) {
	for _, c := range []struct{ name, file string }{
		{"automation.json", autoorganize.ConfigFileName},
		{"auto-organize.json", autoorganize.V1ConfigFileName},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newSvcEnv(t)
			e.rule("r1")
			e.ft.Notebook("nb", "Notes", "")
			e.page("nb", "p1", "strokes_a.rm")
			e.run()
			dir := e.st.UserDataPath(uid)
			statePath := filepath.Join(dir, StateFileName)
			before, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if c.file == autoorganize.V1ConfigFileName {
				if err := os.Remove(filepath.Join(dir, autoorganize.ConfigFileName)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, c.file), []byte(`{"rules": [`), 0o600); err != nil {
				t.Fatal(err)
			}
			e.page("nb", "p1", "strokes_ab.rm")
			calls := e.sender.count()
			run, err := e.svc.RunNow(uid)
			if err == nil || run == nil || run.Error == "" {
				t.Fatalf("run with a corrupt %s: %+v, %v", c.file, run, err)
			}
			if e.sender.count() != calls {
				t.Fatal("events sent with a corrupt config")
			}
			after, err := os.ReadFile(statePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("state changed (%v)", err)
			}
			if _, err := os.Stat(filepath.Join(dir, autoorganize.ConfigFileName)); c.file == autoorganize.V1ConfigFileName && !os.IsNotExist(err) {
				t.Fatalf("a failed migration wrote %s: %v", autoorganize.ConfigFileName, err)
			}
		})
	}
}
