package pagewatch

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

// tagRule a page_tagged pipeline rule; no watch entries = the whole tree
func (e *env) tagRule(id, tag string, watch ...automation.Place) *automation.Rule {
	r := e.rule(id, watch...)
	r.When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: tag}}
	return r
}

func (e *env) tag(doc, page, tag string, at time.Time) {
	e.t.Helper()
	if err := e.ft.TagPage(doc, page, tag, at); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) untag(doc, page, tag string) {
	e.t.Helper()
	if err := e.ft.UntagPage(doc, page, tag); err != nil {
		e.t.Fatal(err)
	}
}

func TestPageTaggedFirstRunPreexistingIsTranscribeOnly(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.tag("nb", "p1", "ToDo", e.now.Add(-24*time.Hour))
	e.tag("nb", "p2", "other", e.now.Add(-24*time.Hour))
	e.ft.Notebook("nb2", "Other", "")
	e.page("nb2", "p1", "strokes_a.rm")

	p := e.detect()
	// tagged before the rule first ran: into the note, no tasks
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p1 r1")
	ev := p.Pipeline[0]
	if ev.BeforeHash != "" || ev.AfterHash != e.pageHash("nb", "p1") || !ev.Watched || ev.PageIndex != 0 ||
		!reflect.DeepEqual(ev.PageOrder, []string{"p1", "p2"}) {
		t.Fatalf("event = %+v", ev)
	}
	e.state = p.Advance(all(p))
	if rs := ruleState(e.state, "r1"); rs.Baselines["nb/p1"] != ev.AfterHash || len(rs.Baselines) != 1 {
		t.Fatalf("baselines = %v", rs.Baselines)
	}
	wantEvents(t, "next pipeline", e.detect().Pipeline)
}

func TestPageTaggedNewlyTaggedIsEditWithEmptyBefore(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	wantEvents(t, "untagged pipeline", e.sweep().Pipeline)

	e.tag("nb", "p2", " TODO ", e.now)
	p := e.detect()
	// the tag is in .content: the page blob is unchanged, so the feed is quiet
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p2 r1")
	ev := p.Pipeline[0]
	if ev.BeforeHash != "" || ev.AfterHash != e.pageHash("nb", "p2") || ev.PageIndex != 1 || !ev.Watched || ev.RuleID != "r1" {
		t.Fatalf("event = %+v", ev)
	}
	e.state = p.Advance(all(p))
	wantEvents(t, "next pipeline", e.detect().Pipeline)
}

func TestPageTaggedEditWhileTaggedIsDelta(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()
	e.tag("nb", "p1", "todo", e.now)
	e.sweep()
	before := e.pageHash("nb", "p1")

	e.page("nb", "p1", "strokes_ab.rm")
	e.page("nb", "p2", "strokes_a.rm") // untagged: feed only
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1", "edit nb/p2")
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	if ev := p.Pipeline[0]; ev.BeforeHash != before || ev.AfterHash != e.pageHash("nb", "p1") {
		t.Fatalf("event = %+v, want before %q", ev, before)
	}
}

func TestPageTaggedUntaggedLeavesWithoutEvents(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.tag("nb", "p1", "todo", e.now)
	e.sweep()

	e.untag("nb", "p1", "todo")
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.sweep()
	wantEvents(t, "untagged pipeline", p.Pipeline)
	if rs := ruleState(e.state, "r1"); len(rs.Baselines) != 0 {
		t.Fatalf("baseline kept after the tag went: %v", rs.Baselines)
	}
	e.page("nb", "p1", "text_todo.rm")
	wantEvents(t, "still untagged", e.sweep().Pipeline)

	// tagged again: the whole page, as a newly tagged page
	e.tag("nb", "p1", "todo", e.now)
	p = e.detect()
	wantEvents(t, "re-tagged pipeline", p.Pipeline, "edit nb/p1 r1")
	if p.Pipeline[0].BeforeHash != "" {
		t.Fatalf("re-tagged page is not whole: %+v", p.Pipeline[0])
	}
}

func TestPageTaggedAfterLastActiveIsEdit(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	// tagged while the rule was off: after its LastActive, so new (R15)
	e.cfg.Rules[0].Enabled = false
	e.tag("nb", "p1", "todo", e.now)
	wantEvents(t, "disabled rule", e.sweep().Pipeline)
	e.cfg.Rules[0].Enabled = true
	wantEvents(t, "re-enabled rule", e.detect().Pipeline, "edit nb/p1 r1")

	// a doc created after the rule last looked: its tagged pages are new too
	e.ft.Notebook("nb2", "New", "")
	e.page("nb2", "p1", "strokes_ab.rm")
	e.tag("nb2", "p1", "todo", e.now)
	wantEvents(t, "new doc", e.detect().Pipeline, "edit nb/p1 r1", "edit nb2/p1 r1")
}

func TestPageTaggedRetryKeepsKindAndTraceID(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))

	first := e.detect()
	e.state = first.Advance(nil) // nothing accepted; LastActive moves on
	second := e.detect()
	wantEvents(t, "retry", second.Pipeline, "transcribe_only nb/p1 r1")
	if first.Pipeline[0].TraceID != second.Pipeline[0].TraceID {
		t.Fatalf("trace id changed on retry: %q → %q", first.Pipeline[0].TraceID, second.Pipeline[0].TraceID)
	}
	e.state = second.Advance(all(second))

	e.tag("nb", "p2", "todo", e.now)
	first = e.detect()
	e.state = first.Advance(nil)
	second = e.detect()
	wantEvents(t, "edit retry", second.Pipeline, "edit nb/p2 r1")
	if first.Pipeline[0].TraceID != second.Pipeline[0].TraceID {
		t.Fatalf("edit trace id changed on retry")
	}
}

func TestPageTaggedRecentPreexistingPageStaysTranscribeOnly(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))
	e.recent[e.pageHash("nb", "p1")] = true
	p := e.sweep()
	wantEvents(t, "recent", p.Pipeline)
	if !hasSkip(p, SkipNote{DocID: "nb", PageID: "p1", Reason: SkipRecent}) {
		t.Fatalf("skips = %v", p.Skipped)
	}
	delete(e.recent, e.pageHash("nb", "p1"))
	e.at[e.pageHash("nb", "p1")] = e.now.Add(-30 * time.Minute)
	wantEvents(t, "settled", e.detect().Pipeline, "transcribe_only nb/p1 r1")
}

func TestPageTaggedDocMovedIn(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "o")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))
	e.tagRule("r1", "todo", folderWatch("w"))
	e.sweep()

	// p1 was tagged before the rule last looked (pre-existing); p2 after
	e.tag("nb", "p2", "todo", e.now.Add(30*time.Second))
	e.ft.Move("nb", "w")
	p := e.detect()
	wantEvents(t, "moved in", p.Pipeline, "edit nb/p2 r1", "transcribe_only nb/p1 r1")
	e.state = p.Advance(nil)
	wantEvents(t, "retry", e.detect().Pipeline, "edit nb/p2 r1", "transcribe_only nb/p1 r1")

	// moved out: no events, and nothing is kept for the doc
	e.ft.Move("nb", "o")
	wantEvents(t, "moved out", e.sweep().Pipeline)
	if rs := ruleState(e.state, "r1"); len(rs.Baselines)+len(rs.TagWatched)+len(rs.TagJoinAt) != 0 {
		t.Fatalf("rule state kept the doc: %+v", rs)
	}
}

func TestPageTaggedDeletedPageEmitsPageDeleted(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "todo")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()
	e.tag("nb", "p1", "todo", e.now)
	e.sweep()

	if err := e.ft.DeletePage("nb", "p1"); err != nil {
		t.Fatal(err)
	}
	p := e.sweep()
	wantEvents(t, "deleted", p.Pipeline, "page_deleted nb/p1 r1")
	if rs := ruleState(e.state, "r1"); len(rs.Baselines) != 0 {
		t.Fatalf("baselines = %v", rs.Baselines)
	}
	wantEvents(t, "after", e.detect().Pipeline)
}

func TestPageTaggedWholeTreeRespectsTrashScopeAndOptOut(t *testing.T) {
	e := newEnv(t)
	r := e.tagRule("r1", "todo")
	r.Conditions = append(r.Conditions, cond(automation.CondKindIs, automation.Params{Kind: automation.KindNotebooks}))
	e.ft.Folder("f", "Deep", "")
	e.ft.Notebook("nb", "Notes", "f")
	e.page("nb", "p1", "strokes_a.rm")
	e.ft.Notebook("bin", "Binned", "trash")
	e.page("bin", "p1", "strokes_a.rm")
	e.ft.Notebook("out", "Opted out", "")
	e.page("out", "p1", "strokes_a.rm")
	e.ft.Docs["out"].Content["tags"] = []any{"no automation"}
	e.ft.PDF("pdf", "Paper", "", 3, 0, e.now)
	e.page("pdf", "p1", "strokes_a.rm")
	e.sweep()
	for _, doc := range []string{"nb", "bin", "out", "pdf"} {
		e.tag(doc, "p1", "todo", e.now)
	}
	wantEvents(t, "pipeline", e.detect().Pipeline, "edit nb/p1 r1")
}

func TestPageTaggedTriggerChangeStartsAfresh(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))
	e.tag("nb", "p2", "later", e.now.Add(-time.Hour))
	e.sweep() // page_changed: joined, both pages delivered

	// the same rule now waits for a tag: what was tagged before is pre-existing
	e.cfg.Rules[0].When.Trigger = automation.Trigger{Type: automation.TriggerPageTagged, Params: automation.Params{Tag: "todo"}}
	wantEvents(t, "switched", e.sweep().Pipeline, "transcribe_only nb/p1 r1")
	if rs := ruleState(e.state, "r1"); len(rs.Members)+len(rs.JoinStartedAt) != 0 || len(rs.Baselines) != 1 {
		t.Fatalf("page_changed state kept: %+v", rs)
	}
	// another tag: also a fresh start, not a flood of edits
	e.cfg.Rules[0].When.Trigger.Tag = "Later"
	wantEvents(t, "retagged rule", e.sweep().Pipeline, "transcribe_only nb/p2 r1")
	// back to page_changed: a fresh join, never edits of old pages
	e.cfg.Rules[0].When.Trigger = automation.Trigger{Type: automation.TriggerPageChanged}
	wantEvents(t, "back", e.sweep().Pipeline, "transcribe_only nb/p1 r1", "transcribe_only nb/p2 r1")
}

func TestTasksFalseOnPipelineEventsOnly(t *testing.T) {
	e := newEnv(t)
	notes := e.tagRule("notes", "todo")
	notes.Actions[0].Type = automation.ActionSaveNotes
	e.rule("tasks", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.tag("nb", "p1", "todo", e.now.Add(-time.Hour))
	e.sweep()
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 notes", "edit nb/p1 tasks")
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	for _, ev := range append(append([]Event{}, p.Pipeline...), p.Feed...) {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		v, has := m["tasks"]
		if want := ev.RuleID == "notes"; has != want || (has && v != false) {
			t.Errorf("rule %q event json: %s", ev.RuleID, b)
		}
	}
}

// docTag adds a document tag to .content tags, as the device writes it
func (e *env) docTag(doc, tag string, at time.Time) {
	e.t.Helper()
	d := e.ft.Docs[doc]
	tags, _ := d.Content["tags"].([]any)
	d.Content["tags"] = append(tags, map[string]any{"name": tag, "timestamp": at.UnixMilli()})
}

func (e *env) undocTag(doc string) {
	e.ft.Docs[doc].Content["tags"] = []any{}
}

// a document tag is a tag on every page (Mike, 2026-11-03): tagged after the
// rule's join start, older writing is an edit with no before (tasks)
func TestDocTagIsTagOnEveryPage(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "extract")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	wantEvents(t, "untagged", e.sweep().Pipeline)

	e.docTag("nb", "Extract", e.now)
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "edit nb/p2 r1")
	for _, ev := range p.Pipeline {
		if ev.BeforeHash != "" {
			t.Fatalf("before = %q", ev.BeforeHash)
		}
	}
	e.state = p.Advance(all(p))
	wantEvents(t, "next", e.detect().Pipeline)
}

func TestDocTagNewWritingIsEdit(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "extract")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	tagAt := e.now
	e.docTag("nb", "extract", tagAt)
	e.sweep()
	first := e.pageHash("nb", "p1")

	e.now = e.now.Add(time.Hour) // past the minimum page age
	e.page("nb", "p1", "strokes_ab.rm")
	e.at[e.pageHash("nb", "p1")] = tagAt.Add(time.Minute)
	e.page("nb", "p2", "text_todo.rm")
	e.at[e.pageHash("nb", "p2")] = tagAt.Add(time.Minute)
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "edit nb/p2 r1")
	if p.Pipeline[0].BeforeHash != first || p.Pipeline[1].BeforeHash != "" {
		t.Fatalf("befores: %q %q", p.Pipeline[0].BeforeHash, p.Pipeline[1].BeforeHash)
	}
}

func TestDocTagCoversWatchedDoc(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "extract")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()
	e.tag("nb", "p1", "extract", e.now)
	wantEvents(t, "page tag", e.sweep().Pipeline, "edit nb/p1 r1")

	e.docTag("nb", "extract", e.now)
	wantEvents(t, "doc tag", e.sweep().Pipeline, "edit nb/p2 r1")
}

func TestDocTagRemovedStopsEvents(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "extract")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.docTag("nb", "extract", e.now)
	e.sweep()

	e.undocTag("nb")
	e.page("nb", "p1", "strokes_ab.rm")
	wantEvents(t, "untagged", e.sweep().Pipeline)
	if rs := ruleState(e.state, "r1"); len(rs.Baselines) != 0 {
		t.Fatalf("baselines kept: %v", rs.Baselines)
	}
}

func TestDocTagIsNotPageTag(t *testing.T) {
	e := newEnv(t)
	r := e.tagRule("r1", "extract")
	r.Conditions = append(r.Conditions, automation.Condition{Type: automation.CondPageTagIs, Params: automation.Params{Tag: "q3"}})
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()
	e.docTag("nb", "extract", e.now)
	e.tag("nb", "p1", "q3", e.now)
	wantEvents(t, "pipeline", e.sweep().Pipeline, "edit nb/p1 r1")
}

// as with page tags: a document tagged before the rule first ran goes into
// the note without tasks
func TestDocTagBeforeFirstRunIsTranscribeOnly(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.docTag("nb", "extract", e.now.Add(-24*time.Hour))
	e.tagRule("r1", "extract")
	wantEvents(t, "pipeline", e.detect().Pipeline, "transcribe_only nb/p1 r1")
}
