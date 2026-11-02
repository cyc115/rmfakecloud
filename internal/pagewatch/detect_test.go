package pagewatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

const uid = "u"

// env one fake tablet, its server storage and the page watch state
type env struct {
	t     *testing.T
	ft    *tablet.Tablet
	st    *fs.FileSystemStorage
	cache map[string]autoorganize.ContentInfo
	state *State
	cfg   *automation.Config
	now   time.Time
	// recent blob hashes whose mtime is inside MinAge
	recent map[string]bool
	// mtimeErr blob hashes whose mtime cannot be read
	mtimeErr map[string]bool
	// at blob hashes with a fixed mtime (others: an hour before now)
	at map[string]time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := tablet.NewStorage(t.TempDir())
	if err := tablet.RegisterUser(st, uid, true); err != nil {
		t.Fatal(err)
	}
	return &env{
		t:     t,
		ft:    tablet.New(t, st, uid),
		st:    st,
		cache: map[string]autoorganize.ContentInfo{},
		state: NewState(),
		cfg: &automation.Config{Version: automation.Version, Settings: automation.Settings{
			Enabled: true, Mode: automation.ModeLive, IntervalMinutes: 15,
		}},
		now:      time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		recent:   map[string]bool{},
		mtimeErr: map[string]bool{},
		at:       map[string]time.Time{},
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../testkit/tablet/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// page stores a fixture as the page blob
func (e *env) page(doc, page, fix string) {
	e.t.Helper()
	if err := e.ft.SetPage(doc, page, fixture(e.t, fix)); err != nil {
		e.t.Fatal(err)
	}
}

// blankPage a page in cPages without an .rm entry
func (e *env) blankPage(doc, page string) {
	e.t.Helper()
	if err := e.ft.SetPage(doc, page, nil); err != nil {
		e.t.Fatal(err)
	}
	delete(e.ft.Docs[doc].RM, page)
}

// pageRule an enabled page_changed rule over in that extracts tasks, with no
// conditions (kind all)
func pageRule(id string, in ...automation.Place) automation.Rule {
	return automation.Rule{
		ID:         id,
		Enabled:    true,
		When:       automation.When{Subject: automation.SubjectPage, In: in, Trigger: automation.Trigger{Type: automation.TriggerPageChanged}},
		Conditions: []automation.Condition{},
		Actions:    []automation.Action{{Type: automation.ActionExtractTasks, Prompt: "prompt-" + id, Model: "model-" + id}},
	}
}

func (e *env) rule(id string, watch ...automation.Place) *automation.Rule {
	e.cfg.Rules = append(e.cfg.Rules, pageRule(id, watch...))
	return &e.cfg.Rules[len(e.cfg.Rules)-1]
}

func folderWatch(id string) automation.Place {
	return automation.Place{ID: id, Kind: automation.PlaceFolder}
}

func docWatch(id string) automation.Place {
	return automation.Place{ID: id, Kind: automation.PlaceDocument}
}

// cond a condition of type typ with params p
func cond(typ string, p automation.Params) automation.Condition {
	return automation.Condition{Type: typ, Params: p}
}

func (e *env) modTime(hash string) (time.Time, error) {
	if e.mtimeErr[hash] {
		return time.Time{}, errors.New("stat failed")
	}
	if e.recent[hash] {
		return e.now.Add(-time.Minute), nil
	}
	if t, ok := e.at[hash]; ok {
		return t, nil
	}
	return e.now.Add(-time.Hour), nil
}

// snapshot syncs the tablet and snapshots the server tree with pages attached
func (e *env) snapshot() (*autoorganize.Snapshot, func(*autoorganize.DocSnap) autoorganize.ContentInfo) {
	e.t.Helper()
	if err := e.ft.Sync(); err != nil {
		e.t.Fatal(err)
	}
	tree, err := e.st.GetCachedTree(uid)
	if err != nil {
		e.t.Fatal(err)
	}
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(e.cache, e.st.BlobStorage(uid).GetReader)
	snap.AttachPages(cc.Get)
	return snap, cc.Get
}

func (e *env) input(snap *autoorganize.Snapshot, content func(*autoorganize.DocSnap) autoorganize.ContentInfo) Input {
	return Input{
		Snap: snap, Config: e.cfg, State: e.state, Content: content,
		ModTime: e.modTime, Now: e.now, MinAge: DefaultMinAge, User: uid,
	}
}

func (e *env) detect() Plan {
	e.t.Helper()
	// each sweep happens later than the one before
	e.now = e.now.Add(time.Minute)
	snap, content := e.snapshot()
	return Detect(e.input(snap, content))
}

// sweep detects and accepts every event
func (e *env) sweep() Plan {
	e.t.Helper()
	p := e.detect()
	e.state = p.Advance(all(p))
	return p
}

func (e *env) pageHash(doc, page string) string {
	e.t.Helper()
	snap, _ := e.snapshot()
	d := snap.Docs[doc]
	if d == nil {
		e.t.Fatalf("no doc %s", doc)
	}
	for _, p := range d.Pages {
		if p.ID == page {
			return p.Hash
		}
	}
	e.t.Fatalf("doc %s has no page %s", doc, page)
	return ""
}

func all(p Plan) map[string]bool {
	out := map[string]bool{}
	for _, ev := range append(append([]Event{}, p.Feed...), p.Pipeline...) {
		out[ev.TraceID] = true
	}
	return out
}

func ids(evs ...Event) map[string]bool {
	out := map[string]bool{}
	for _, ev := range evs {
		out[ev.TraceID] = true
	}
	return out
}

// summary "kind doc/page rule" per event, sorted
func summary(evs []Event) []string {
	out := []string{}
	for _, ev := range evs {
		s := ev.Kind + " " + ev.DocID + "/" + ev.PageID
		if ev.RuleID != "" {
			s += " " + ev.RuleID
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func wantEvents(t *testing.T, what string, evs []Event, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if got := summary(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s events = %q, want %q", what, got, want)
	}
}

func hasSkip(p Plan, want SkipNote) bool {
	for _, s := range p.Skipped {
		if s == want {
			return true
		}
	}
	return false
}

func stateJSON(t *testing.T, s *State) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFirstFeedRunEmitsNothingAndSetsBaselines(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	at := e.now.Add(-2 * time.Hour)
	e.ft.Open("nb", 1, at)

	p := e.detect()
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline)

	st := p.Advance(nil)
	if !st.FeedInitialised {
		t.Fatal("FeedInitialised not set")
	}
	if st.Feed["nb/p1"] != e.pageHash("nb", "p1") || st.Feed["nb/p2"] != e.pageHash("nb", "p2") {
		t.Fatalf("feed baselines = %v", st.Feed)
	}
	if st.Opened["nb"] != tablet.Ms(at) {
		t.Fatalf("opened = %v", st.Opened)
	}
	e.state = st
	wantEvents(t, "second run feed", e.detect().Feed)
}

func TestEditedPageEmitsEditWithBeforeAndAfter(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("f", "Work", "")
	e.ft.Notebook("nb", "Notes", "f")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	before := e.pageHash("nb", "p1")

	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	wantEvents(t, "pipeline", p.Pipeline)
	ev := p.Feed[0]
	if ev.BeforeHash != before || ev.AfterHash != e.pageHash("nb", "p1") || ev.AfterHash == before {
		t.Fatalf("hashes before=%q after=%q, want before %q", ev.BeforeHash, ev.AfterHash, before)
	}
	if ev.PageIndex != 0 || ev.User != uid || ev.DocName != "Notes" || ev.DocPath != "Work / Notes" || ev.DocType != DocNotebook {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Watched || ev.RuleID != "" || ev.Prompt != "" || ev.OpenedAt != nil || ev.TraceID == "" {
		t.Fatalf("feed event = %+v", ev)
	}
	if !ev.ChangedAt.Equal(e.now.Add(-time.Hour)) {
		t.Fatalf("changed_at = %v, want the blob mtime", ev.ChangedAt)
	}
}

func TestNewPageEmitsEditWithEmptyBefore(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	e.page("nb", "p2", "text_todo.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p2")
	if ev := p.Feed[0]; ev.BeforeHash != "" || ev.AfterHash != e.pageHash("nb", "p2") || ev.PageIndex != 1 {
		t.Fatalf("event = %+v", ev)
	}
}

func TestRecentPageIsSkippedThenEmittedNextSweep(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	before := e.pageHash("nb", "p1")

	e.page("nb", "p1", "strokes_ab.rm")
	e.recent[e.pageHash("nb", "p1")] = true
	p := e.detect()
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline)
	if !hasSkip(p, SkipNote{DocID: "nb", PageID: "p1", Reason: SkipRecent}) {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
	e.state = p.Advance(all(p))

	e.recent = map[string]bool{}
	p = e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	if p.Feed[0].BeforeHash != before || p.Pipeline[0].BeforeHash != before {
		t.Fatalf("before = %q / %q, want %q", p.Feed[0].BeforeHash, p.Pipeline[0].BeforeHash, before)
	}
}

func TestModTimeErrorSkipsPage(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	e.page("nb", "p1", "strokes_ab.rm")
	e.mtimeErr[e.pageHash("nb", "p1")] = true
	for i := 0; i < 2; i++ {
		p := e.detect()
		wantEvents(t, "feed", p.Feed)
		wantEvents(t, "pipeline", p.Pipeline)
		if !hasSkip(p, SkipNote{DocID: "nb", PageID: "p1", Reason: SkipMtime}) {
			t.Fatalf("skipped = %+v", p.Skipped)
		}
		e.state = p.Advance(all(p))
	}
	e.mtimeErr = map[string]bool{}
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
}

func TestOpenEmitsLastOpenedPage(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()

	at := e.now.Add(-30 * time.Minute)
	e.ft.Open("nb", 1, at)
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "open nb/p2")
	ev := p.Feed[0]
	if ev.PageIndex != 1 || ev.AfterHash != e.pageHash("nb", "p2") || ev.BeforeHash != "" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.OpenedAt == nil || !ev.OpenedAt.Equal(at) || !ev.ChangedAt.Equal(at) {
		t.Fatalf("opened_at = %v changed_at = %v, want %v", ev.OpenedAt, ev.ChangedAt, at)
	}
	e.state = p.Advance(all(p))
	wantEvents(t, "next feed", e.detect().Feed)
}

func TestEditAndOpenSameSweepEmitBoth(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	e.page("nb", "p1", "strokes_ab.rm")
	e.ft.Open("nb", 0, e.now.Add(-20*time.Minute))
	wantEvents(t, "feed", e.detect().Feed, "edit nb/p1", "open nb/p1")
}

func TestDeletedPageFeedNothingPipelinePageDeleted(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()

	if err := e.ft.DeletePage("nb", "p1"); err != nil {
		t.Fatal(err)
	}
	p := e.detect()
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline, "page_deleted nb/p1 r1")
	ev := p.Pipeline[0]
	if ev.BeforeHash != "" || ev.AfterHash != "" || !reflect.DeepEqual(ev.PageOrder, []string{"p2"}) || !ev.Watched || ev.PageIndex != -1 {
		t.Fatalf("event = %+v", ev)
	}

	// not accepted: the page stays in the baseline and is reported again
	e.state = p.Advance(nil)
	p = e.detect()
	wantEvents(t, "pipeline again", p.Pipeline, "page_deleted nb/p1 r1")

	e.state = p.Advance(all(p))
	if _, ok := ruleState(e.state, "r1").Baselines["nb/p1"]; ok {
		t.Fatal("deleted page still in the rule baseline")
	}
	p = e.detect()
	wantEvents(t, "feed after", p.Feed)
	wantEvents(t, "pipeline after", p.Pipeline)
}

func TestPipelineEventsCarryPageOrder(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.page("nb", "p3", "strokes_ab.rm")
	p := e.sweep()
	for _, ev := range p.Pipeline {
		if !reflect.DeepEqual(ev.PageOrder, []string{"p1", "p2", "p3"}) {
			t.Fatalf("join page order = %v", ev.PageOrder)
		}
	}

	if err := e.ft.MovePageBefore("nb", "p3", "p1"); err != nil {
		t.Fatal(err)
	}
	p = e.detect()
	wantEvents(t, "reorder feed", p.Feed)
	wantEvents(t, "reorder pipeline", p.Pipeline)
	e.state = p.Advance(all(p))

	e.page("nb", "p1", "strokes_ab.rm")
	p = e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	ev := p.Pipeline[0]
	if !reflect.DeepEqual(ev.PageOrder, []string{"p3", "p1", "p2"}) || ev.PageIndex != 1 {
		t.Fatalf("order = %v index = %d", ev.PageOrder, ev.PageIndex)
	}
}

func TestPipelineNeedsEnabled(t *testing.T) {
	e := newEnv(t)
	e.cfg.Settings.Enabled = false
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	wantEvents(t, "disabled join", e.sweep().Pipeline)

	e.page("nb", "p1", "strokes_ab.rm")
	p := e.sweep()
	wantEvents(t, "disabled feed", p.Feed, "edit nb/p1")
	wantEvents(t, "disabled pipeline", p.Pipeline)

	// Dry mode is the loop's business: Detect still plans pipeline events
	e.cfg.Settings.Enabled = true
	e.cfg.Settings.Mode = automation.ModeDry
	wantEvents(t, "enabled pipeline", e.detect().Pipeline, "transcribe_only nb/p1 r1")
}

func TestPipelineWatchesSubfolders(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("a", "A", "")
	e.ft.Folder("b", "B", "a")
	e.ft.Notebook("deep", "Deep", "b")
	e.page("deep", "p1", "strokes_a.rm")
	e.ft.Notebook("other", "Other", "")
	e.page("other", "p1", "strokes_a.rm")
	e.rule("r1", folderWatch("a"))

	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only deep/p1 r1")
	if p.Pipeline[0].DocPath != "A / B / Deep" {
		t.Fatalf("doc path = %q", p.Pipeline[0].DocPath)
	}
}

func TestPipelineWatchedDocument(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("f", "F", "")
	e.ft.Notebook("nb1", "One", "f")
	e.page("nb1", "p1", "strokes_a.rm")
	e.ft.Notebook("nb2", "Two", "f")
	e.page("nb2", "p1", "strokes_a.rm")
	e.rule("r1", docWatch("nb1"))

	wantEvents(t, "pipeline", e.detect().Pipeline, "transcribe_only nb1/p1 r1")
}

func TestNoAutomationTagExcludedFromPipelineNotFeed(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("tagged", "Tagged", "")
	e.ft.Docs["tagged"].Content["tags"] = []any{map[string]any{"name": "No-Automation"}}
	e.page("tagged", "p1", "strokes_a.rm")
	e.ft.Notebook("plain", "Plain", "")
	e.page("plain", "p1", "strokes_a.rm")
	wantEvents(t, "join", e.sweep().Pipeline, "transcribe_only plain/p1 r1")

	e.page("tagged", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit tagged/p1")
	wantEvents(t, "pipeline", p.Pipeline)
}

func TestScopeMismatchExcludedFromPipeline(t *testing.T) {
	e := newEnv(t)
	r := e.rule("r1", folderWatch(autoorganize.RootID))
	r.Conditions = append(r.Conditions, cond(automation.CondKindIs, automation.Params{Kind: automation.KindPdfEpub}))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	wantEvents(t, "pipeline", e.detect().Pipeline)
}

func TestTrashedDocNotOnFeed(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "trash")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p2", "strokes_ab.rm")
	e.ft.Open("nb", 1, e.now.Add(-20*time.Minute))
	wantEvents(t, "feed", e.detect().Feed)
}

func TestJoiningDocEmitsTranscribeOnlyForEachPage(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.blankPage("nb", "p2")
	e.page("nb", "p3", "text_todo.rm")

	p := e.detect()
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p1 r1", "transcribe_only nb/p3 r1")
	for _, ev := range p.Pipeline {
		if ev.BeforeHash != "" || ev.AfterHash != e.pageHash("nb", ev.PageID) || !ev.Watched ||
			!reflect.DeepEqual(ev.PageOrder, []string{"p1", "p2", "p3"}) {
			t.Fatalf("event = %+v", ev)
		}
		if (ev.PageID == "p1" && ev.PageIndex != 0) || (ev.PageID == "p3" && ev.PageIndex != 2) {
			t.Fatalf("page index = %+v", ev)
		}
	}
	e.state = p.Advance(all(p))
	rs := ruleState(e.state, "r1")
	if !rs.Members["nb"] || rs.Baselines["nb/p1"] == "" || rs.Baselines["nb/p3"] == "" {
		t.Fatalf("rule state = %+v", rs)
	}
	wantEvents(t, "next pipeline", e.detect().Pipeline)
}

func TestJoinCompletesOnlyWhenAllAccepted(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")

	p := e.detect()
	var first Event
	for _, ev := range p.Pipeline {
		if ev.PageID == "p1" {
			first = ev
		}
	}
	e.state = p.Advance(ids(first))
	if ruleState(e.state, "r1").Members["nb"] {
		t.Fatal("member before every join event was accepted")
	}
	// the rest of the join stays transcribe_only, never an edit
	p = e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p2 r1")
	e.state = p.Advance(all(p))
	if !ruleState(e.state, "r1").Members["nb"] {
		t.Fatal("not a member after the join completed")
	}
}

func TestAdvanceOnlyMovesAcceptedBaselines(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "strokes_a.rm")
	e.sweep()

	e.page("nb", "p1", "strokes_ab.rm")
	e.page("nb", "p2", "text_todo.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1", "edit nb/p2")
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "edit nb/p2 r1")
	pick := func(evs []Event, page string) Event {
		for _, ev := range evs {
			if ev.PageID == page {
				return ev
			}
		}
		t.Fatalf("no event for %s", page)
		return Event{}
	}
	// feed accepts p1, pipeline accepts p2: the baselines are independent
	e.state = p.Advance(ids(pick(p.Feed, "p1"), pick(p.Pipeline, "p2")))

	p = e.detect()
	wantEvents(t, "feed again", p.Feed, "edit nb/p2")
	wantEvents(t, "pipeline again", p.Pipeline, "edit nb/p1 r1")
	if p.Feed[0].AfterHash != e.pageHash("nb", "p2") || p.Pipeline[0].AfterHash != e.pageHash("nb", "p1") {
		t.Fatal("re-emitted events have the wrong hashes")
	}
}

func TestTwoRulesOnOneDocBothEmit(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.rule("r2", docWatch("nb"))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	wantEvents(t, "join", e.sweep().Pipeline, "transcribe_only nb/p1 r1", "transcribe_only nb/p1 r2")

	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "edit nb/p1 r2")
	if p.Pipeline[0].TraceID == p.Pipeline[1].TraceID {
		t.Fatal("two rules share a trace id")
	}
	for _, ev := range p.Pipeline {
		if ev.RuleID == "r1" {
			e.state = p.Advance(ids(ev))
		}
	}
	wantEvents(t, "next pipeline", e.detect().Pipeline, "edit nb/p1 r2")
}

func TestMovedOutOfWatchedFolderStopsPipeline(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "w")
	e.page("nb", "p1", "strokes_a.rm")
	e.rule("r1", folderWatch("w"))
	e.sweep()

	e.ft.Move("nb", "o")
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	wantEvents(t, "pipeline", p.Pipeline)
	e.state = p.Advance(all(p))
	rs := ruleState(e.state, "r1")
	if rs.Members["nb"] || rs.Baselines["nb/p1"] != "" {
		t.Fatalf("rule state after leaving = %+v", rs)
	}
}

func TestRejoinGetsTranscribeOnlyAgain(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "w")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.rule("r1", folderWatch("w"))
	e.sweep()

	e.ft.Move("nb", "o")
	wantEvents(t, "out", e.sweep().Pipeline)
	e.ft.Move("nb", "w")
	wantEvents(t, "rejoin", e.detect().Pipeline, "transcribe_only nb/p1 r1", "transcribe_only nb/p2 r1")
}

func TestEventHasTraceIDAndPrompt(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	ev := p.Pipeline[0]
	if ev.Prompt != "prompt-r1" || ev.Model != "model-r1" || ev.RuleID != "r1" || !ev.Watched {
		t.Fatalf("event = %+v", ev)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(ev.TraceID) || ev.TraceID == p.Feed[0].TraceID {
		t.Fatalf("trace ids feed=%q pipeline=%q", p.Feed[0].TraceID, ev.TraceID)
	}
}

func TestDetectSkipsWhenPagesNotAttached(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p2", "strokes_ab.rm")
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	tree, err := e.st.GetCachedTree(uid)
	if err != nil {
		t.Fatal(err)
	}
	snap := autoorganize.NewSnapshot(tree) // no AttachPages: every Pages is nil
	before := stateJSON(t, e.state)
	p := Detect(e.input(snap, func(*autoorganize.DocSnap) autoorganize.ContentInfo {
		t.Fatal("content read for an unattached snapshot")
		return autoorganize.ContentInfo{}
	}))
	wantEvents(t, "feed", p.Feed)
	wantEvents(t, "pipeline", p.Pipeline)
	if len(p.Skipped) != 1 || p.Skipped[0].Reason != SkipNotAttached {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
	if got := stateJSON(t, p.Advance(all(p))); got != before {
		t.Fatalf("state changed:\n%s\nwant\n%s", got, before)
	}
}

func TestDetectSkipsDocWithPagesErr(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("bad", "Bad", "")
	e.page("bad", "p1", "strokes_a.rm")
	e.ft.Notebook("good", "Good", "")
	e.page("good", "p1", "strokes_a.rm")
	e.sweep()
	old := e.state.Clone()

	e.page("bad", "p1", "strokes_ab.rm")
	e.ft.Open("bad", 0, e.now.Add(-20*time.Minute))
	e.ft.Docs["bad"].RawContent = []byte("{not json")
	e.page("good", "p1", "strokes_ab.rm")
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit good/p1")
	wantEvents(t, "pipeline", p.Pipeline, "edit good/p1 r1")
	if !hasSkip(p, SkipNote{DocID: "bad", Reason: SkipContent}) {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
	st := p.Advance(all(p))
	rs, oldRS := ruleState(st, "r1"), ruleState(old, "r1")
	if st.Feed["bad/p1"] != old.Feed["bad/p1"] || st.Opened["bad"] != old.Opened["bad"] ||
		rs.Baselines["bad/p1"] != oldRS.Baselines["bad/p1"] || !rs.Members["bad"] {
		t.Fatalf("bad doc state moved: feed=%q opened=%q rule=%+v", st.Feed["bad/p1"], st.Opened["bad"], rs)
	}
}

func TestAdvanceDoesNotMutateInput(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.sweep()

	e.page("nb", "p1", "strokes_ab.rm")
	if err := e.ft.DeletePage("nb", "p2"); err != nil {
		t.Fatal(err)
	}
	e.ft.Open("nb", 0, e.now.Add(-20*time.Minute))
	before := stateJSON(t, e.state)
	p := e.detect()
	if got := stateJSON(t, e.state); got != before {
		t.Fatal("Detect mutated the input state")
	}
	next := p.Advance(all(p))
	if got := stateJSON(t, e.state); got != before {
		t.Fatal("Advance mutated the input state")
	}
	if stateJSON(t, next) == before {
		t.Fatal("Advance moved nothing")
	}
	if stateJSON(t, p.Advance(all(p))) != stateJSON(t, next) {
		t.Fatal("Advance is not repeatable")
	}
}

func TestEventJSONFieldNames(t *testing.T) {
	at := time.Unix(0, 0).UTC()
	b, err := json.Marshal(Event{TraceID: "t", Kind: KindOpen, PageOrder: []string{"p"}, BeforeHash: "b", AfterHash: "a", PDFHash: "x", PageID: "p", OpenedAt: &at, RuleID: "r", Prompt: "q", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	json.Unmarshal(b, &m)
	for _, k := range []string{"trace_id", "kind", "page_order", "user", "doc_id", "doc_name", "doc_path", "doc_type", "page_id", "page_index", "before_hash", "after_hash", "pdf_hash", "changed_at", "opened_at", "watched", "rule_id", "prompt", "model"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing json key %q in %s", k, b)
		}
	}
	b, _ = json.Marshal(Event{Kind: KindPageDeleted})
	m = map[string]any{}
	json.Unmarshal(b, &m)
	for _, k := range []string{"before_hash", "after_hash", "opened_at", "page_order", "rule_id"} {
		if _, ok := m[k]; ok {
			t.Errorf("optional key %q not omitted: %s", k, b)
		}
	}
}

// ruleState never nil, so a missing rule fails an assertion instead of panicking
func ruleState(s *State, id string) *RuleState {
	if rs := s.Rules[id]; rs != nil {
		return rs
	}
	return newRuleState()
}

func TestNewDocInWatchedFolderEmitsEditsNotTranscribeOnly(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("old", "Old", "")
	e.page("old", "p1", "strokes_a.rm")
	e.sweep()

	// created since the last sweep: its pages are new writing, so tasks get extracted
	e.ft.Notebook("nb", "New", "")
	e.page("nb", "p1", "strokes_ab.rm")
	e.page("nb", "p2", "text_todo.rm")
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "edit nb/p2 r1")
	wantEvents(t, "feed", p.Feed, "edit nb/p1", "edit nb/p2")
	for _, ev := range p.Pipeline {
		if ev.BeforeHash != "" || ev.AfterHash != e.pageHash("nb", ev.PageID) || !ev.Watched {
			t.Fatalf("event = %+v", ev)
		}
	}
	if st := p.Advance(nil); !ruleState(st, "r1").Members["nb"] {
		t.Fatal("new doc is not a member at once")
	}
}

func TestKnownDocMovedInEmitsTranscribeOnly(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "o")
	e.page("nb", "p1", "strokes_a.rm")
	e.rule("r1", folderWatch("w"))
	e.sweep()

	e.ft.Move("nb", "w")
	// a rule created after the feed knew the doc: pre-existing content too
	e.rule("r2", docWatch("nb"))
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "transcribe_only nb/p1 r1", "transcribe_only nb/p1 r2")
	wantEvents(t, "feed", p.Feed)
}

func TestNewDocRecentPageEmittedAsEditNextSweep(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("old", "Old", "")
	e.page("old", "p1", "strokes_a.rm")
	e.sweep()

	e.ft.Notebook("nb", "New", "")
	e.page("nb", "p1", "strokes_ab.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.recent[e.pageHash("nb", "p1")] = true
	p := e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p2 r1")
	if !hasSkip(p, SkipNote{DocID: "nb", PageID: "p1", Reason: SkipRecent}) {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
	e.state = p.Advance(all(p))
	if !ruleState(e.state, "r1").Members["nb"] {
		t.Fatal("new doc is not a member")
	}

	// the feed now knows the doc; the waiting page is still an edit
	e.recent = map[string]bool{}
	p = e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	if p.Pipeline[0].BeforeHash != "" {
		t.Fatalf("before = %q", p.Pipeline[0].BeforeHash)
	}
}

func TestPartialJoinEditedPageIsEdit(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	h1 := e.pageHash("nb", "p1")
	p := e.detect()
	for _, ev := range p.Pipeline {
		if ev.PageID == "p1" {
			e.state = p.Advance(ids(ev))
		}
	}

	// p1 was delivered during the join: writing on it is new content
	e.page("nb", "p1", "strokes_ab.rm")
	p = e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1", "transcribe_only nb/p2 r1")
	for _, ev := range p.Pipeline {
		if ev.PageID == "p1" && ev.BeforeHash != h1 {
			t.Fatalf("before = %q, want %q", ev.BeforeHash, h1)
		}
	}
	e.state = p.Advance(all(p))
	if !ruleState(e.state, "r1").Members["nb"] {
		t.Fatal("join did not complete")
	}
}

func TestDocSkippedOnFirstRunIsNotNew(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.ft.Docs["nb"].RawContent = []byte("{not json")
	p := e.sweep()
	if !hasSkip(p, SkipNote{DocID: "nb", Reason: SkipContent}) {
		t.Fatalf("skipped = %+v", p.Skipped)
	}

	e.ft.Docs["nb"].RawContent = nil
	wantEvents(t, "pipeline", e.detect().Pipeline, "transcribe_only nb/p1 r1")
}

func TestDocRestoredFromTrashIsNotNew(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "trash")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.sweep()

	e.ft.Move("nb", "")
	wantEvents(t, "pipeline", e.detect().Pipeline, "transcribe_only nb/p1 r1")
}

func TestDocCreatedWhilePipelineDisabledIsNewOnReenable(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("old", "Old", "")
	e.page("old", "p1", "strokes_a.rm")
	e.sweep()

	e.cfg.Settings.Enabled = false
	e.ft.Notebook("nb", "New", "")
	e.page("nb", "p1", "strokes_ab.rm")
	wantEvents(t, "disabled pipeline", e.sweep().Pipeline)

	e.cfg.Settings.Enabled = true
	wantEvents(t, "re-enabled pipeline", e.detect().Pipeline, "edit nb/p1 r1")
}

func TestDocCreatedWhileRuleDisabledIsNewOnReenable(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("old", "Old", "")
	e.page("old", "p1", "strokes_a.rm")
	e.sweep()

	e.cfg.Rules[0].Enabled = false
	e.ft.Notebook("nb", "New", "")
	e.page("nb", "p1", "strokes_ab.rm")
	wantEvents(t, "disabled rule", e.sweep().Pipeline)

	e.cfg.Rules[0].Enabled = true
	wantEvents(t, "re-enabled rule", e.detect().Pipeline, "edit nb/p1 r1")
}

func TestTraceIDStableAcrossSweeps(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p1", "strokes_ab.rm")

	first := e.detect() // not accepted: the state stays
	second := e.detect()
	if len(first.Feed) != 1 || len(first.Pipeline) != 1 {
		t.Fatalf("plan %+v", first)
	}
	if first.Feed[0].TraceID != second.Feed[0].TraceID || first.Pipeline[0].TraceID != second.Pipeline[0].TraceID {
		t.Fatalf("ids changed: %q/%q %q/%q", first.Feed[0].TraceID, second.Feed[0].TraceID, first.Pipeline[0].TraceID, second.Pipeline[0].TraceID)
	}
}

func TestTraceIDDiffersForDifferentTransitions(t *testing.T) {
	ab := TraceID("feed", "edit", "d", "p", "A", "B")
	ba := TraceID("feed", "edit", "d", "p", "B", "A")
	rule1 := TraceID("rule", "r1", "edit", "d", "p", "A", "B")
	rule2 := TraceID("rule", "r2", "edit", "d", "p", "A", "B")
	seen := map[string]bool{}
	for _, id := range []string{ab, ba, rule1, rule2} {
		if len(id) != 32 || seen[id] {
			t.Fatalf("id %q empty-ish or repeated among %v", id, []string{ab, ba, rule1, rule2})
		}
		seen[id] = true
	}
	if TraceID("a", "bc") == TraceID("ab", "c") {
		t.Fatal("parts are not separated")
	}

	// two rules watching one page get different ids for the same change
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.rule("r2", docWatch("nb"))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	if len(p.Pipeline) != 2 || p.Pipeline[0].TraceID == p.Pipeline[1].TraceID || p.Pipeline[0].TraceID == p.Feed[0].TraceID {
		t.Fatalf("ids %+v", p)
	}
}

func TestPageOrderNotShared(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.rule("r2", docWatch("nb"))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	p := e.detect()
	if len(p.Pipeline) != 4 {
		t.Fatalf("pipeline = %q", summary(p.Pipeline))
	}
	p.Pipeline[0].PageOrder[0] = "changed"
	for _, ev := range p.Pipeline[1:] {
		if ev.PageOrder[0] != "p1" {
			t.Fatalf("page order shared: %v", ev.PageOrder)
		}
	}
}

func TestNewDocSkippedOnItsFirstSweepIsStillNew(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("old", "Old", "")
	e.page("old", "p1", "strokes_a.rm")
	e.sweep()

	e.ft.Notebook("nb", "New", "")
	e.page("nb", "p1", "strokes_ab.rm")
	e.ft.Docs["nb"].RawContent = []byte("{not json")
	p := e.sweep()
	if !hasSkip(p, SkipNote{DocID: "nb", Reason: SkipContent}) {
		t.Fatalf("skipped = %+v", p.Skipped)
	}

	e.ft.Docs["nb"].RawContent = nil
	p = e.detect()
	wantEvents(t, "pipeline", p.Pipeline, "edit nb/p1 r1")
	if p.Pipeline[0].BeforeHash != "" {
		t.Fatalf("before = %q", p.Pipeline[0].BeforeHash)
	}
}

func pipelineEv(t *testing.T, evs []Event, page string) Event {
	t.Helper()
	for _, ev := range evs {
		if ev.PageID == page {
			return ev
		}
	}
	t.Fatalf("no pipeline event for %s in %q", page, summary(evs))
	return Event{}
}

func TestPageAddedMidJoinIsEdit(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "o")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.rule("r1", folderWatch("w"))
	e.sweep()
	last := ruleState(e.state, "r1").LastActive

	// moved in; p1 is rewritten after the rule's last sweep and is still recent
	e.ft.Move("nb", "w")
	e.page("nb", "p1", "strokes_ab.rm")
	h1 := e.pageHash("nb", "p1")
	e.recent[h1] = true
	p := e.sweep() // the join starts; p1 holds it open
	wantEvents(t, "join", p.Pipeline, "transcribe_only nb/p2 r1")
	start := ruleState(e.state, "r1").JoinStartedAt["nb"]
	if !start.Equal(last) || ruleState(e.state, "r1").Members["nb"] {
		t.Fatalf("join start %v, want the rule's last sweep %v; state %+v", start, last, ruleState(e.state, "r1"))
	}

	// p3 is written after the join started: new writing, tasks extracted
	e.page("nb", "p3", "text_note.rm")
	e.at[e.pageHash("nb", "p3")] = start.Add(time.Minute)
	e.now = e.now.Add(time.Hour) // p3 is old enough to send
	p = e.sweep()
	wantEvents(t, "mid-join", p.Pipeline, "edit nb/p3 r1")
	if ev := pipelineEv(t, p.Pipeline, "p3"); ev.BeforeHash != "" {
		t.Fatalf("before = %q", ev.BeforeHash)
	}
	if got := ruleState(e.state, "r1").JoinStartedAt["nb"]; !got.Equal(start) {
		t.Fatalf("join start moved: %v, want %v", got, start)
	}

	// p1 settles: written after the rule's last sweep, so an edit; the join completes
	e.recent = map[string]bool{}
	e.at[h1] = last.Add(30 * time.Second)
	p = e.sweep()
	wantEvents(t, "rest of join", p.Pipeline, "edit nb/p1 r1")
	if ev := pipelineEv(t, p.Pipeline, "p1"); ev.BeforeHash != "" {
		t.Fatalf("before = %q", ev.BeforeHash)
	}
	rs := ruleState(e.state, "r1")
	if !rs.Members["nb"] || len(rs.JoinStartedAt) != 0 {
		t.Fatalf("rule state after join = %+v", rs)
	}
}

func TestPageWrittenBetweenMoveAndJoinSweepIsEdit(t *testing.T) {
	setup := func(t *testing.T) (*env, time.Time) {
		e := newEnv(t)
		e.ft.Folder("w", "Watched", "")
		e.ft.Folder("o", "Other", "")
		e.ft.Notebook("nb", "Notes", "o")
		e.page("nb", "p1", "strokes_a.rm")
		e.rule("r1", folderWatch("w"))
		e.sweep()
		last := e.now
		e.ft.Move("nb", "w")
		e.page("nb", "p2", "text_todo.rm")
		return e, last
	}
	t.Run("old enough at the join sweep", func(t *testing.T) {
		e, last := setup(t)
		e.at[e.pageHash("nb", "p2")] = last.Add(2 * time.Minute) // written after the move
		e.now = last.Add(14 * time.Minute)
		p := e.sweep()
		wantEvents(t, "join sweep", p.Pipeline, "edit nb/p2 r1", "transcribe_only nb/p1 r1")
	})
	t.Run("recent at the join sweep", func(t *testing.T) {
		e, last := setup(t)
		e.at[e.pageHash("nb", "p2")] = last.Add(13 * time.Minute)
		e.now = last.Add(13 * time.Minute) // the sweep at +14m sees p2 1m old: skipped
		wantEvents(t, "join sweep", e.sweep().Pipeline, "transcribe_only nb/p1 r1")
		e.now = e.now.Add(15 * time.Minute)
		p := e.sweep()
		wantEvents(t, "next sweep", p.Pipeline, "edit nb/p2 r1")
		if !ruleState(e.state, "r1").Members["nb"] {
			t.Fatal("join did not complete")
		}
	})
}

func TestJoinWithUnreadableMtimeStillExtractsNewPages(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	e.mtimeErr[e.pageHash("nb", "p1")] = true // never readable: the join never completes
	wantEvents(t, "join", e.sweep().Pipeline, "transcribe_only nb/p2 r1")
	start := ruleState(e.state, "r1").JoinStartedAt["nb"]

	for i := 0; i < 3; i++ {
		e.sweep()
	}
	e.page("nb", "p3", "strokes_ab.rm")
	e.at[e.pageHash("nb", "p3")] = e.now // written after the join started
	e.now = e.now.Add(time.Hour)
	p := e.sweep()
	wantEvents(t, "new page", p.Pipeline, "edit nb/p3 r1")
	if ruleState(e.state, "r1").Members["nb"] || !ruleState(e.state, "r1").JoinStartedAt["nb"].Equal(start) {
		t.Fatalf("rule state = %+v", ruleState(e.state, "r1"))
	}
}

func TestLeavingClearsJoinStart(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("o", "Other", "")
	e.ft.Notebook("nb", "Notes", "w")
	e.page("nb", "p1", "strokes_a.rm")
	e.mtimeErr[e.pageHash("nb", "p1")] = true
	e.rule("r1", folderWatch("w"))
	e.sweep()
	if _, ok := ruleState(e.state, "r1").JoinStartedAt["nb"]; !ok {
		t.Fatal("no join start")
	}
	e.ft.Move("nb", "o")
	e.sweep()
	if rs := ruleState(e.state, "r1"); len(rs.JoinStartedAt) != 0 {
		t.Fatalf("join start kept after leaving: %+v", rs)
	}
}

func TestMissingWatchEntriesReported(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Notebook("nb", "Notes", "w")
	e.page("nb", "p1", "strokes_a.rm")
	e.rule("r1", folderWatch(autoorganize.RootID), folderWatch("w"), docWatch("nb"),
		automation.Place{ID: "gone-f", Kind: automation.PlaceFolder, Name: "Old"}, docWatch("gone-d"))
	p := e.detect()
	want := []MissingWatch{
		{RuleID: "r1", WatchID: "gone-f", Kind: automation.PlaceFolder, Name: "Old"},
		{RuleID: "r1", WatchID: "gone-d", Kind: automation.PlaceDocument},
	}
	if !reflect.DeepEqual(p.MissingWatches, want) {
		t.Fatalf("missing = %+v", p.MissingWatches)
	}
	// a disabled rule reports nothing
	e.cfg.Rules[0].Enabled = false
	if p := e.detect(); len(p.MissingWatches) != 0 {
		t.Fatalf("disabled rule reported %+v", p.MissingWatches)
	}
}

func TestEmptyTreeWithManyKnownDocsAborts(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	for i := 0; i <= EmptyTreeGuard; i++ {
		id := fmt.Sprintf("nb%02d", i)
		e.ft.Notebook(id, id, "")
		e.page(id, "p1", "strokes_a.rm")
	}
	e.sweep()
	e.sweep()
	before := stateJSON(t, e.state)
	for id := range e.ft.Docs {
		delete(e.ft.Docs, id)
	}
	p := e.detect()
	if p.Abort == "" || len(p.Feed)+len(p.Pipeline) != 0 {
		t.Fatalf("plan abort=%q feed=%d pipeline=%d", p.Abort, len(p.Feed), len(p.Pipeline))
	}
	if got := stateJSON(t, p.Advance(all(p))); got != before {
		t.Fatalf("aborted plan changed the state:\n%s\n%s", got, before)
	}
}

func TestEmptyTreeWithFewKnownDocsPrunes(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < EmptyTreeGuard; i++ {
		id := fmt.Sprintf("nb%02d", i)
		e.ft.Notebook(id, id, "")
		e.page(id, "p1", "strokes_a.rm")
	}
	e.sweep()
	for id := range e.ft.Docs {
		delete(e.ft.Docs, id)
	}
	p := e.detect()
	if p.Abort != "" {
		t.Fatalf("aborted with %d known docs", EmptyTreeGuard)
	}
	if st := p.Advance(all(p)); len(st.Feed) != 0 || len(st.Seen) != 0 {
		t.Fatalf("not pruned: %+v", st)
	}
}

func TestZeroMinAgeHasNoSkipWindow(t *testing.T) {
	e := newEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p1", "strokes_ab.rm")
	e.recent[e.pageHash("nb", "p1")] = true
	e.now = e.now.Add(time.Minute)
	snap, content := e.snapshot()
	in := e.input(snap, content)
	in.MinAge = 0
	if p := Detect(in); len(p.Feed) != 1 || len(p.Skipped) != 0 {
		t.Fatalf("feed %d skipped %+v", len(p.Feed), p.Skipped)
	}
}

func TestTrashedWatchEntryIsMissing(t *testing.T) {
	e := newEnv(t)
	e.ft.Folder("w", "Watched", "")
	e.ft.Folder("sub", "Sub", "w")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.rule("r1", folderWatch("sub"), docWatch("nb"))
	if p := e.detect(); len(p.MissingWatches) != 0 {
		t.Fatalf("missing %+v", p.MissingWatches)
	}
	e.ft.Move("w", "trash") // sub's chain now ends in the trash
	e.ft.Move("nb", "trash")
	want := []MissingWatch{
		{RuleID: "r1", WatchID: "sub", Kind: automation.PlaceFolder, InTrash: true},
		{RuleID: "r1", WatchID: "nb", Kind: automation.PlaceDocument, InTrash: true},
	}
	if p := e.detect(); !reflect.DeepEqual(p.MissingWatches, want) {
		t.Fatalf("missing %+v", p.MissingWatches)
	}
}
