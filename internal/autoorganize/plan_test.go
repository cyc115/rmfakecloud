package autoorganize

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// memTree builds an in-memory tree; content is looked up by doc id
type memTree struct {
	tree    *models.HashTree
	content map[string]ContentInfo
}

func newMemTree() *memTree {
	return &memTree{tree: &models.HashTree{}, content: map[string]ContentInfo{}}
}

func (m *memTree) add(id, name, parent string, typ common.EntryType, meta func(md *models.MetadataFile)) *models.HashDoc {
	md := models.MetadataFile{DocumentName: name, CollectionType: typ, Parent: parent, Version: 1}
	if meta != nil {
		meta(&md)
	}
	d := models.NewHashDocWithMeta(id, md)
	d.Files = []*models.HashEntry{
		{EntryName: id + ".metadata", Hash: "m-" + id},
		{EntryName: id + ".content", Hash: "c-" + id},
	}
	d.Hash = "h-" + id
	m.tree.Docs = append(m.tree.Docs, d)
	return d
}

func (m *memTree) folder(id, name, parent string) {
	m.add(id, name, parent, common.CollectionType, nil)
	m.content["c-"+id] = ContentInfo{}
}

func (m *memTree) doc(id, name, parent string, ci ContentInfo, meta func(md *models.MetadataFile)) {
	m.add(id, name, parent, common.DocumentType, meta)
	m.content["c-"+id] = ci
}

func pdfInfo(pages int) ContentInfo {
	return ContentInfo{FileType: "pdf", PageCount: pages}
}

func (m *memTree) plan(t *testing.T, now time.Time, st *State, rules ...automation.Rule) *Plan {
	t.Helper()
	if st == nil {
		st = NewState()
	}
	snap := NewSnapshot(m.tree)
	cfg := &automation.Config{Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: 15}, Rules: rules}
	cfg.Normalize()
	return BuildPlan(PlanInput{Snap: snap, Config: cfg, State: st, Now: now, Content: func(d *DocSnap) ContentInfo {
		ci, ok := m.content[d.ContentHash]
		if !ok {
			return ContentInfo{Error: "no blob"}
		}
		return ci
	}})
}

func movedIDs(p *Plan) []string {
	var ids []string
	for _, m := range p.Moves {
		ids = append(ids, m.DocID)
	}
	return ids
}

func skipOf(p *Plan, docID string) *Skip {
	for i := range p.Skipped {
		if p.Skipped[i].DocID == docID {
			return &p.Skipped[i]
		}
	}
	return nil
}

func ruleResult(p *Plan, id string) *RuleResult {
	for i := range p.Rules {
		if p.Rules[i].RuleID == id {
			return &p.Rules[i]
		}
	}
	return nil
}

func hoursAgo(now time.Time, h float64) string {
	return ms(now.Add(-time.Duration(h * float64(time.Hour))))
}

func daysAgo(now time.Time, d float64) string {
	return hoursAgo(now, d*24)
}

// moveRule an enabled document rule: documents directly in from, trigger tr,
// then conds, move to the folder to. A page-based trigger gets kind_is
// pdf_epub first (the v1 scope it was locked to); the others cover every kind.
func moveRule(id, from, to string, tr automation.Trigger, conds ...automation.Condition) automation.Rule {
	all := []automation.Condition{}
	if s := automation.Triggers[tr.Type]; s != nil && s.PageBased {
		all = append(all, kindIs(ScopePdfEpub))
	}
	return automation.Rule{ID: id, Enabled: true,
		When:       automation.When{Subject: automation.SubjectDocument, In: []automation.Place{{ID: from, Kind: automation.PlaceFolder}}, Trigger: tr},
		Conditions: append(all, conds...),
		Actions:    []automation.Action{{Type: automation.ActionMove, To: &automation.Destination{FolderRef: FolderRef{ID: to}}}}}
}

// pageRule an enabled page_changed rule over in that extracts tasks
func pageRule(id string, in ...automation.Place) automation.Rule {
	return automation.Rule{ID: id, Enabled: true,
		When:       automation.When{Subject: automation.SubjectPage, In: in, Trigger: trig(TriggerPageChanged, P{})},
		Conditions: []automation.Condition{},
		Actions:    []automation.Action{{Type: automation.ActionExtractTasks}}}
}

func readRule() automation.Rule {
	return moveRule("r1", "rr", "read", trig(TriggerFinishedReading, P{MinPages: 3, SettleHours: 6}))
}

func baseTree() *memTree {
	m := newMemTree()
	m.folder("rr", "Research Reports", "")
	m.folder("read", "Read", "rr")
	return m
}

func TestFinishedReading(t *testing.T) {
	now := time.Now()
	m := baseTree()
	opened := func(page int, h float64) func(md *models.MetadataFile) {
		return func(md *models.MetadataFile) { md.LastOpenedPage = page; md.LastOpened = hoursAgo(now, h) }
	}
	m.doc("done", "Done", "rr", pdfInfo(14), opened(13, 9))
	m.doc("exact3", "Exactly three pages", "rr", pdfInfo(3), opened(2, 9))
	m.doc("two", "Two pages", "rr", pdfInfo(2), opened(1, 9))
	m.doc("midway", "Midway", "rr", pdfInfo(14), opened(6, 9))
	m.doc("fresh", "Just finished", "rr", pdfInfo(14), opened(13, 2))
	m.doc("zero", "Web upload", "rr", pdfInfo(0), opened(0, 9))
	m.doc("never", "Never opened single page", "rr", pdfInfo(3), func(md *models.MetadataFile) { md.LastOpenedPage = 2; md.LastOpened = "" })
	m.doc("never0", "Never opened zero", "rr", pdfInfo(3), func(md *models.MetadataFile) { md.LastOpenedPage = 2; md.LastOpened = "0" })
	m.doc("epub", "Book", "rr", ContentInfo{FileType: "epub", PageCount: 200}, opened(199, 30))
	m.doc("nb", "Sketchbook", "rr", ContentInfo{FileType: "notebook", PageCount: 5}, opened(4, 30))
	m.doc("nb-empty", "Old notebook", "rr", ContentInfo{FileType: "", PageCount: 5}, opened(4, 30))

	p := m.plan(t, now, nil, readRule())
	got := strings.Join(movedIDs(p), ",")
	if got != "epub,done,exact3" {
		t.Fatalf("moves = %s, want epub,done,exact3 (sorted by name)", got)
	}
	for id, reason := range map[string]string{"zero": SkipPageCountUnknown, "nb": SkipOutOfScope, "nb-empty": SkipOutOfScope} {
		s := skipOf(p, id)
		if s == nil || s.Reason != reason {
			t.Errorf("%s: skip = %+v, want %s", id, s, reason)
		}
	}
	for _, id := range []string{"two", "midway", "fresh", "never", "never0"} {
		if s := skipOf(p, id); s != nil {
			t.Errorf("%s should simply not match, got skip %+v", id, s)
		}
	}
	rr := ruleResult(p, "r1")
	if rr.Checked != 11 || rr.Matches != 3 || rr.NotMatching != 5 {
		t.Errorf("counts: %+v", rr)
	}
	if mv := p.Moves[1]; mv.Inputs["pageCount"] != "14" || mv.Inputs["lastOpenedPage"] != "13" || mv.Inputs["lastOpened"] == "" {
		t.Errorf("inputs: %+v", mv.Inputs)
	}
	if !strings.Contains(p.Moves[1].Detail, "last page 14 of 14") {
		t.Errorf("detail: %s", p.Moves[1].Detail)
	}

	// settle guard 0: the just-finished doc qualifies
	r := readRule()
	r.When.Trigger.SettleHours = 0
	p = m.plan(t, now, nil, r)
	if !strings.Contains(strings.Join(movedIDs(p), ","), "fresh") {
		t.Errorf("settleHours 0 should move the fresh doc: %v", movedIDs(p))
	}
}

func TestReadProgress(t *testing.T) {
	now := time.Now()
	m := baseTree()
	at := func(page int) func(md *models.MetadataFile) {
		return func(md *models.MetadataFile) { md.LastOpenedPage = page; md.LastOpened = hoursAgo(now, 10) }
	}
	m.doc("p80", "Eight of ten", "rr", pdfInfo(10), at(7))
	m.doc("p70", "Seven of ten", "rr", pdfInfo(10), at(6))
	m.doc("p0", "No pages", "rr", pdfInfo(0), at(0))
	r := moveRule("r1", "rr", "read", trig(TriggerReadProgress, P{Percent: 80, SettleHours: 6}))
	p := m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "p80" {
		t.Fatalf("moves = %s", got)
	}
	if s := skipOf(p, "p0"); s == nil || s.Reason != SkipPageCountUnknown {
		t.Errorf("pageCount 0: %+v", s)
	}
}

func TestNotOpenedForDays(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("archive", "Archive", "")
	m.doc("old", "Opened 40 days ago", "inbox", pdfInfo(5), func(md *models.MetadataFile) { md.LastOpened = daysAgo(now, 40); md.CreatedTime = daysAgo(now, 100) })
	m.doc("recent", "Opened 10 days ago", "inbox", pdfInfo(5), func(md *models.MetadataFile) { md.LastOpened = daysAgo(now, 10); md.CreatedTime = daysAgo(now, 100) })
	m.doc("never-old", "Never opened, 10 days old", "inbox", pdfInfo(5), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 10) })
	m.doc("never-new", "Never opened, 3 days old", "inbox", pdfInfo(5), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 3) })
	m.doc("never-lm", "Never opened, no createdTime, lastModified 9 days", "inbox", pdfInfo(5), func(md *models.MetadataFile) { md.LastModified = daysAgo(now, 9) })
	m.doc("never-none", "Never opened, no clocks", "inbox", pdfInfo(5), nil)

	st := NewState()
	st.FirstSeen["never-none"] = &FirstSeen{At: now.Add(-8 * 24 * time.Hour).UnixMilli()}
	r := moveRule("r1", "inbox", "archive", trig(TriggerNotOpened, P{Days: 30}), cond(automation.CondMinAgeDays, P{Days: 7}))
	p := m.plan(t, now, st, r)
	if got := strings.Join(movedIDs(p), ","); got != "never-old,never-none,never-lm,old" {
		t.Fatalf("moves = %s", got)
	}
	var lm *PlannedMove
	for i := range p.Moves {
		if p.Moves[i].DocID == "never-lm" {
			lm = &p.Moves[i]
		}
	}
	if !strings.Contains(lm.Detail, "lastModified") || lm.Inputs["lastOpened"] != "" {
		t.Errorf("age anchor fallback: %+v", lm)
	}

	// without the age guard a brand new never-opened doc matches at once
	r.Conditions = nil
	p = m.plan(t, now, st, r)
	if !strings.Contains(strings.Join(movedIDs(p), ","), "never-new") {
		t.Errorf("age guard off: %v", movedIDs(p))
	}
}

func TestNotModifiedForDays(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("papers", "Papers", "")
	m.folder("cold", "Cold storage", "")
	m.doc("stale", "Stale", "papers", ContentInfo{FileType: "notebook"}, func(md *models.MetadataFile) { md.LastModified = daysAgo(now, 100) })
	m.doc("active", "Active", "papers", pdfInfo(2), func(md *models.MetadataFile) { md.LastModified = daysAgo(now, 10) })
	m.doc("unknown", "No clock", "papers", pdfInfo(2), nil)
	r := moveRule("r1", "papers", "cold", trig(TriggerNotModified, P{Days: 90}))
	p := m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "stale" {
		t.Fatalf("moves = %s", got)
	}
	if s := skipOf(p, "unknown"); s == nil || s.Reason != SkipTimeUnknown {
		t.Errorf("missing lastModified: %+v", s)
	}
	if p.Moves[0].Inputs["lastModified"] == "" {
		t.Errorf("inputs: %+v", p.Moves[0].Inputs)
	}
}

func TestNameMatches(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("news", "News", "")
	m.doc("wsj", "WSJ Weekend 2026-10-18", "inbox", pdfInfo(0), nil)
	m.doc("ft", "FT Weekend", "inbox", pdfInfo(10), nil)
	m.doc("lower", "wsj daily", "inbox", ContentInfo{FileType: "epub"}, nil)
	r := moveRule("r1", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "WSJ *"}))
	p := m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "lower,wsj" {
		t.Fatalf("moves = %s (pageCount 0 does not block name rules)", got)
	}
}

func TestTagIs(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("done", "Done", "")
	m.doc("strtag", "String tags", "inbox", ContentInfo{FileType: "pdf", Tags: []string{"Done"}}, nil)
	m.doc("objtag", "Object tags", "inbox", ContentInfo{FileType: "notebook", Tags: []string{"other", "done"}}, nil)
	m.doc("untagged", "Untagged", "inbox", pdfInfo(3), nil)
	r := moveRule("r1", "inbox", "done", trig(TriggerTagIs, P{Tag: "done"}))
	p := m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "objtag,strtag" {
		t.Fatalf("moves = %s", got)
	}
}

func TestParseContentTagsBothEncodings(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"pdf","pageCount":12,"lastOpenedPage":3,"textScale":0.8,"tags":["No Automation","x"],"pageTags":[{"name":"no automation","pageId":"p1"}]}`))
	if !ci.Readable() || ci.PageCount != 12 || ci.LastOpenedPage != 3 || ci.FileType != "pdf" || !ci.HasNoAutomationTag() {
		t.Fatalf("string tags: %+v", ci)
	}
	ci = ParseContent([]byte(`{"fileType":"epub","pageCount":3,"tags":[{"name":"no-automation","timestamp":1700000000000}]}`))
	if !ci.HasNoAutomationTag() {
		t.Fatalf("object tags: %+v", ci)
	}
	ci = ParseContent([]byte(`{"fileType":"pdf","pageCount":3,"tags":[],"pageTags":[{"name":"no automation","pageId":"p1","timestamp":1}]}`))
	if ci.HasNoAutomationTag() {
		t.Fatal("page tags must not opt the document out")
	}
	ci = ParseContent([]byte(`{"fileType":`))
	if ci.Readable() {
		t.Fatal("corrupt content must be unreadable")
	}
}

func TestAgeSinceCreated(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("old", "Old", "")
	m.doc("c20", "Created 20 days ago", "inbox", pdfInfo(1), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 20); md.LastModified = daysAgo(now, 1) })
	m.doc("c5", "Created 5 days ago", "inbox", pdfInfo(1), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 5); md.LastModified = daysAgo(now, 30) })
	m.doc("lm", "No createdTime, modified 15 days ago", "inbox", pdfInfo(1), func(md *models.MetadataFile) { md.LastModified = daysAgo(now, 15) })
	m.doc("seen", "No clocks, first seen 16 days ago", "inbox", pdfInfo(1), nil)
	m.doc("unseen", "No clocks, never seen", "inbox", pdfInfo(1), nil)
	st := NewState()
	st.FirstSeen["seen"] = &FirstSeen{At: now.Add(-16 * 24 * time.Hour).UnixMilli()}
	r := moveRule("r1", "inbox", "old", trig(TriggerAgeSinceCreated, P{Days: 14}))
	p := m.plan(t, now, st, r)
	if got := strings.Join(movedIDs(p), ","); got != "c20,seen,lm" {
		t.Fatalf("moves = %s", got)
	}
}

func TestPinnedGuardAndPinnedIs(t *testing.T) {
	now := time.Now()
	m := baseTree()
	pin := func(md *models.MetadataFile) {
		md.Pinned = true
		md.LastOpenedPage = 4
		md.LastOpened = hoursAgo(now, 10)
	}
	m.doc("pinned", "Pinned finished", "rr", pdfInfo(5), pin)
	p := m.plan(t, now, nil, readRule())
	if s := skipOf(p, "pinned"); s == nil || s.Reason != SkipPinned || len(p.Moves) != 0 {
		t.Fatalf("pinned must be skipped: %+v", p.Skipped)
	}

	r := moveRule("r2", "rr", "read", trig(TriggerPinnedIs, P{Pinned: true}))
	p = m.plan(t, now, nil, r)
	if len(p.Moves) != 1 {
		t.Fatalf("pinned_is ignores the pinned guard: %+v", p.Skipped)
	}
	r.When.Trigger.Pinned = false
	p = m.plan(t, now, nil, r)
	if len(p.Moves) != 0 {
		t.Fatal("is not pinned must not match a pinned doc")
	}
}

func TestNoAutomationTagSkipsEveryTrigger(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("out", "Out", "")
	meta := func(md *models.MetadataFile) {
		md.LastOpenedPage = 4
		md.LastOpened = daysAgo(now, 200)
		md.LastModified = daysAgo(now, 200)
		md.CreatedTime = daysAgo(now, 200)
		md.Pinned = true
	}
	m.doc("tagged", "Staff Guide", "inbox", ContentInfo{FileType: "pdf", PageCount: 5, Tags: []string{"No-Automation", "done"}}, meta)
	triggers := []automation.Trigger{
		trig(TriggerFinishedReading, P{MinPages: 1}),
		trig(TriggerReadProgress, P{Percent: 10}),
		trig(TriggerNotOpened, P{Days: 1}),
		trig(TriggerNotModified, P{Days: 1}),
		trig(TriggerNameMatches, P{Pattern: "*"}),
		trig(TriggerTagIs, P{Tag: "done"}),
		trig(TriggerAgeSinceCreated, P{Days: 1}),
		trig(TriggerPinnedIs, P{Pinned: true}),
	}
	for _, tr := range triggers {
		p := m.plan(t, now, nil, moveRule("r", "inbox", "out", tr))
		if len(p.Moves) != 0 {
			t.Errorf("%s moved a no-automation doc", tr.Type)
		}
		if s := skipOf(p, "tagged"); s == nil || s.Reason != SkipNoAutomation {
			t.Errorf("%s: skip = %+v", tr.Type, s)
		}
		if p.Rules[0].OptedOut != 1 {
			t.Errorf("%s: opted out count %d", tr.Type, p.Rules[0].OptedOut)
		}
	}
}

func TestUnreadableContentIsSkipped(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("out", "Out", "")
	m.doc("broken", "Broken", "inbox", ContentInfo{Error: "cannot parse .content"}, nil)
	p := m.plan(t, now, nil, moveRule("r", "inbox", "out", trig(TriggerNameMatches, P{Pattern: "*"})))
	if s := skipOf(p, "broken"); s == nil || s.Reason != SkipContentUnreadable || len(p.Moves) != 0 {
		t.Fatalf("unreadable content: %+v", p.Skipped)
	}
}

func TestTrashByAncestorWalkAndUUIDResolution(t *testing.T) {
	now := time.Now()
	finished := func(md *models.MetadataFile) { md.LastOpenedPage = 4; md.LastOpened = hoursAgo(now, 10) }
	m := newMemTree()
	m.folder("rr", "Research Reports (renamed)", "")
	m.folder("read", "Read", "rr")
	m.folder("binned", "Binned", "trash")
	m.folder("child", "Child of binned", "binned")
	m.folder("grandchild", "Grandchild", "child")
	m.doc("d1", "Finished", "rr", pdfInfo(5), finished)
	m.doc("d2", "In trashed tree", "grandchild", pdfInfo(5), finished)

	// renamed source still resolves by UUID
	r := readRule()
	r.When.In[0].Name = "Research Reports"
	p := m.plan(t, now, nil, r)
	rr := ruleResult(p, "r1")
	if len(p.Moves) != 1 || rr.SourceRenamedFrom != "Research Reports" || rr.SourceName != "Research Reports (renamed)" {
		t.Fatalf("renamed source: moves %v result %+v", movedIDs(p), rr)
	}

	// source in trash by ancestor walk (grandchild of a trashed folder)
	p = m.plan(t, now, nil, moveRule("r2", "grandchild", "read", trig(TriggerFinishedReading, P{MinPages: 1})))
	if len(p.Moves) != 0 || !contains(p.Rules[0].Problems, "Source folder in Trash") {
		t.Fatalf("source in trash: %+v", p.Rules[0])
	}
	// destination in trash by ancestor walk
	p = m.plan(t, now, nil, moveRule("r3", "rr", "child", trig(TriggerFinishedReading, P{MinPages: 1})))
	if len(p.Moves) != 0 || !contains(p.Rules[0].Problems, "Destination in Trash") {
		t.Fatalf("destination in trash: %+v", p.Rules[0])
	}
	// UUID resolution failure: the rule is skipped and flagged, never widened
	p = m.plan(t, now, nil, moveRule("r4", "gone-uuid", "read", trig(TriggerNameMatches, P{Pattern: "*"})))
	if len(p.Moves) != 0 || p.Scanned != 0 || !contains(p.Rules[0].Problems, "Source folder missing") {
		t.Fatalf("missing source: %+v", p.Rules[0])
	}
	p = m.plan(t, now, nil, moveRule("r5", "rr", "gone-uuid", trig(TriggerNameMatches, P{Pattern: "*"})))
	if len(p.Moves) != 0 || !contains(p.Rules[0].Problems, "Destination folder missing") {
		t.Fatalf("missing destination: %+v", p.Rules[0])
	}
	// a document UUID is not a folder
	p = m.plan(t, now, nil, moveRule("r6", "d1", "read", trig(TriggerNameMatches, P{Pattern: "*"})))
	if !contains(p.Rules[0].Problems, "Source folder missing") {
		t.Fatalf("doc as source: %+v", p.Rules[0])
	}
}

func TestUnknownParentIsNeverRoot(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("archive", "Archive", "")
	m.doc("rootdoc", "At root", "", pdfInfo(1), nil)
	m.doc("orphan", "Orphan", "missing-folder", pdfInfo(1), nil)
	m.folder("loop-a", "A", "loop-b")
	m.folder("loop-b", "B", "loop-a")
	r := moveRule("r1", RootID, "archive", trig(TriggerNameMatches, P{Pattern: "*"}))
	p := m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "rootdoc" {
		t.Fatalf("root source moves = %s; an unknown parent is not root", got)
	}
	snap := NewSnapshot(m.tree)
	if snap.Locate("orphan") != LocUnknown || snap.Locate("loop-a") != LocUnknown || snap.Locate("rootdoc") != LocAtRoot {
		t.Fatal("locate: unknown parent and cycles must be LocUnknown")
	}
}

func TestFirstMatchWinsInListOrder(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("news", "News", "")
	m.folder("archive", "Archive", "")
	m.doc("wsj", "WSJ old", "inbox", pdfInfo(3), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 60) })
	m.doc("other", "Other old", "inbox", pdfInfo(3), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 60) })
	a := moveRule("a", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "WSJ *"}))
	b := moveRule("b", "inbox", "archive", trig(TriggerNotOpened, P{Days: 30}), cond(automation.CondMinAgeDays, P{Days: 7}))

	p := m.plan(t, now, nil, a, b)
	if len(p.Moves) != 2 || p.Moves[0].DocID != "wsj" || p.Moves[0].To != "news" || p.Moves[1].DocID != "other" || p.Moves[1].To != "archive" {
		t.Fatalf("order a,b: %+v", p.Moves)
	}
	if s := skipOf(p, "wsj"); s == nil || s.Reason != SkipClaimed || s.RuleNumber != 2 || !strings.Contains(s.Detail, "rule 1") {
		t.Fatalf("claimed skip: %+v", s)
	}
	// reversed order: b claims both, a gets nothing
	p = m.plan(t, now, nil, b, a)
	if len(p.Moves) != 2 || p.Moves[0].To != "archive" || p.Moves[1].To != "archive" {
		t.Fatalf("order b,a: %+v", p.Moves)
	}
	// a document is in at most one planned move
	seen := map[string]bool{}
	for _, mv := range p.Moves {
		if seen[mv.DocID] {
			t.Fatalf("document planned twice: %s", mv.DocID)
		}
		seen[mv.DocID] = true
	}
}

func TestSkippedByEarlierRuleIsNotClaimed(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("archive", "Archive", "")
	m.folder("inbox", "Inbox", "")
	m.doc("d", "Pinned doc", "archive", pdfInfo(3), func(md *models.MetadataFile) { md.Pinned = true })
	a := moveRule("a", "archive", "inbox", trig(TriggerNameMatches, P{Pattern: "*"}))
	b := moveRule("b", "archive", "inbox", trig(TriggerPinnedIs, P{Pinned: true}))
	p := m.plan(t, now, nil, a, b)
	if len(p.Moves) != 1 || p.Moves[0].RuleID != "b" {
		t.Fatalf("rule a skips pinned, rule b may claim: %+v", p.Moves)
	}
}

func TestSnapshotIsolationWithinOnePlan(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("archive", "Archive", "")
	m.folder("deep", "Deep", "")
	m.doc("d", "Doc", "inbox", pdfInfo(3), nil)
	a := moveRule("a", "inbox", "archive", trig(TriggerNameMatches, P{Pattern: "*"}))
	b := moveRule("b", "archive", "deep", trig(TriggerNameMatches, P{Pattern: "*"}))
	p := m.plan(t, now, nil, a, b)
	if len(p.Moves) != 1 || p.Moves[0].To != "archive" {
		t.Fatalf("a move earlier in the sweep must not feed a later rule: %+v", p.Moves)
	}
	if ruleResult(p, "b").Checked != 0 {
		t.Fatal("rule b sees the snapshot, where Archive is empty")
	}
}

func TestHoldKeyedOnInputs(t *testing.T) {
	now := time.Now()
	m := baseTree()
	opened := hoursAgo(now, 10)
	m.doc("d", "Doc", "rr", pdfInfo(5), func(md *models.MetadataFile) { md.LastOpenedPage = 4; md.LastOpened = opened; md.Version = 7 })
	st := NewState()
	h := st.SetHold(Hold{RuleID: "r1", DocID: "d", Source: "rr", MovedTo: "read", CreatedAt: now.UnixMilli(), Reason: "moved",
		Inputs: map[string]string{"pageCount": "5", "lastOpenedPage": "4", "lastOpened": opened}})

	p := m.plan(t, now, st, readRule())
	s := skipOf(p, "d")
	if len(p.Moves) != 0 || s == nil || s.Reason != SkipHeld || s.HoldID != h.ID || !strings.Contains(s.Detail, "inputs unchanged") {
		t.Fatalf("identical inputs must hold (version is not part of the key): %+v", p.Skipped)
	}
	if len(p.ClearHolds) != 0 {
		t.Fatal("matching hold must not be cleared")
	}

	// the doc is re-opened: lastOpened moves, the hold no longer applies and is cleared
	m2 := baseTree()
	m2.doc("d", "Doc", "rr", pdfInfo(5), func(md *models.MetadataFile) { md.LastOpenedPage = 4; md.LastOpened = hoursAgo(now, 8) })
	p = m2.plan(t, now, st, readRule())
	if len(p.Moves) != 1 || len(p.ClearHolds) != 1 {
		t.Fatalf("changed inputs: moves %v clear %v", movedIDs(p), p.ClearHolds)
	}
}

func TestCycleGuardRefusesReturn(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("archive", "Archive", "")
	m.doc("d", "Doc", "archive", pdfInfo(3), func(md *models.MetadataFile) { md.Pinned = true })
	fp := NewSnapshot(m.tree).Docs["d"].Fingerprint
	st := NewState()
	st.History = []HistoryEntry{{DocID: "d", RuleID: "a", RuleNumber: 1, From: "inbox", To: "archive", At: now.Add(-time.Hour).UnixMilli(), Fingerprint: fp}}
	b := moveRule("b", "archive", "inbox", trig(TriggerPinnedIs, P{Pinned: true}))

	p := m.plan(t, now, st, b)
	if s := skipOf(p, "d"); len(p.Moves) != 0 || s == nil || s.Reason != SkipCycleGuard || !strings.Contains(s.Detail, "rule 1") {
		t.Fatalf("cycle guard: %+v", p.Skipped)
	}
	// outside the window
	p = m.plan(t, now.Add(24*time.Hour), st, b)
	if len(p.Moves) != 1 {
		t.Fatal("after 24 h the move is allowed")
	}
	// tablet-side change resets the window
	st.History[0].Fingerprint = "different"
	p = m.plan(t, now, st, b)
	if len(p.Moves) != 1 {
		t.Fatal("a fingerprint change is new information and resets the guard")
	}

	// the root ("My files") is a destination like any other
	st.History = []HistoryEntry{{DocID: "d", RuleID: "a", RuleNumber: 1, From: RootID, To: "archive", At: now.Add(-time.Hour).UnixMilli(), Fingerprint: fp}}
	toRoot := moveRule("b", "archive", RootID, trig(TriggerPinnedIs, P{Pinned: true}))
	p = m.plan(t, now, st, toRoot)
	if s := skipOf(p, "d"); len(p.Moves) != 0 || s == nil || s.Reason != SkipCycleGuard {
		t.Fatalf("cycle guard back to root: moves %v skipped %+v", movedIDs(p), p.Skipped)
	}
}

func TestResolveCreateIfMissing(t *testing.T) {
	now := time.Now()
	finished := func(md *models.MetadataFile) { md.LastOpenedPage = 4; md.LastOpened = hoursAgo(now, 10) }

	// zero matches: will be created
	m := newMemTree()
	m.folder("rr", "Research Reports", "")
	m.doc("d", "Doc", "rr", pdfInfo(5), finished)
	p := m.plan(t, now, nil, seedRule())
	if len(p.Moves) != 1 || p.Moves[0].To != "" || p.Moves[0].CreateFolder == nil || p.Moves[0].ToName != "Research Reports / Read" || !p.Rules[0].DestWillCreate {
		t.Fatalf("0 matches: %+v %+v", p.Moves, p.Rules[0])
	}

	// one match by name (case-insensitive): used
	m.folder("existing", "read", "rr")
	p = m.plan(t, now, nil, seedRule())
	if len(p.Moves) != 1 || p.Moves[0].To != "existing" {
		t.Fatalf("1 match: %+v", p.Moves)
	}

	// two matches: skipped and flagged
	m.folder("existing2", "Read", "rr")
	p = m.plan(t, now, nil, seedRule())
	if len(p.Moves) != 0 || len(p.Rules[0].Problems) != 1 || !strings.Contains(p.Rules[0].Problems[0], "2 folders named") {
		t.Fatalf("2 matches: %+v", p.Rules[0])
	}

	// the created folder UUID in the state wins over name lookup
	st := NewState()
	st.CreatedFolders["seed"] = "existing2"
	p = m.plan(t, now, st, seedRule())
	if len(p.Moves) != 1 || p.Moves[0].To != "existing2" {
		t.Fatalf("created folder from state: %+v %+v", p.Moves, p.Rules[0])
	}
}

func TestDisabledRuleDoesNothing(t *testing.T) {
	now := time.Now()
	m := baseTree()
	m.doc("d", "Doc", "rr", pdfInfo(5), func(md *models.MetadataFile) { md.LastOpenedPage = 4; md.LastOpened = hoursAgo(now, 10) })
	r := readRule()
	r.Enabled = false
	p := m.plan(t, now, nil, r)
	if len(p.Moves) != 0 || p.Scanned != 0 {
		t.Fatal("disabled rule must not plan")
	}
	if p := m.plan(t, now, nil); len(p.Moves) != 0 {
		t.Fatal("empty rule list does nothing")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestPlanIgnoresPipelineRules(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("news", "News", "")
	m.doc("wsj", "WSJ Weekend", "inbox", pdfInfo(0), nil)
	pipe := pageRule("pipe", automation.Place{ID: "inbox", Kind: automation.PlaceFolder})
	mv := moveRule("r2", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "WSJ *"}))
	p := m.plan(t, now, nil, pipe, mv)
	if got := strings.Join(movedIDs(p), ","); got != "wsj" {
		t.Fatalf("moves = %q, want wsj (a pipeline rule must not claim documents)", got)
	}
	if p.Moves[0].RuleNumber != 2 {
		t.Errorf("rule number = %d, want 2 (numbering keeps the config position)", p.Moves[0].RuleNumber)
	}
	if skipOf(p, "wsj") != nil {
		t.Errorf("unexpected skip: %+v", skipOf(p, "wsj"))
	}
	if ruleResult(p, "pipe") != nil {
		t.Errorf("pipeline rule must not appear in the move plan")
	}
}

func TestLoopWarningsIgnorePipelineRules(t *testing.T) {
	a := moveRule("a", "f1", "f2", trig(TriggerPinnedIs, P{Pinned: true}))
	// a page rule "in f2" that would close the loop if it moved anything
	pipe := pageRule("p", automation.Place{ID: "f2", Kind: automation.PlaceFolder})
	cfg := &automation.Config{Rules: []automation.Rule{a, pipe}}
	if got := LoopWarnings(cfg, func(r *automation.Rule) (string, bool) { return "f1", true }); len(got) != 0 {
		t.Fatalf("pipeline rule formed a loop: %v", got)
	}
}

// TestFinishedReadingIgnoresMinAgeAndPinned (R-B3): finished_reading never
// reads pinned or days (v1 carried them with minAge*; MigrateV1 drops them).
// Leftover parameters outside the type's fields change nothing, and a future
// createdTime does not hold a finished document.
func TestFinishedReadingIgnoresMinAgeAndPinned(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	m := baseTree()
	opened := func(page int, h float64) func(md *models.MetadataFile) {
		return func(md *models.MetadataFile) { md.LastOpenedPage = page; md.LastOpened = hoursAgo(now, h) }
	}
	m.doc("done", "Done", "rr", pdfInfo(5), opened(4, 9))
	m.doc("future", "Future", "rr", pdfInfo(5), func(md *models.MetadataFile) {
		opened(4, 9)(md)
		md.CreatedTime = ms(now.Add(48 * time.Hour))
	})
	m.doc("midway", "Midway", "rr", pdfInfo(5), opened(2, 9))
	m.doc("fresh", "Fresh", "rr", pdfInfo(5), opened(4, 1))
	m.doc("pin", "Pinned", "rr", pdfInfo(5), func(md *models.MetadataFile) { opened(4, 9)(md); md.Pinned = true })
	m.doc("nb", "Sketchbook", "rr", ContentInfo{FileType: "notebook", PageCount: 5}, opened(4, 9))

	a, b := readRule(), readRule()
	a.When.Trigger.Days, a.When.Trigger.Pinned = 0, true
	b.When.Trigger.Days, b.When.Trigger.Pinned = 99, false
	pa, pb := m.plan(t, now, nil, a), m.plan(t, now, nil, b)
	if got := movedIDs(pa); !reflect.DeepEqual(got, []string{"done", "future"}) {
		t.Fatalf("moved %v, want done and future", got)
	}
	if rr := ruleResult(pa, "r1"); len(rr.Problems) != 0 {
		t.Fatalf("problems %v", rr.Problems)
	}
	if !reflect.DeepEqual(pa.Moves, pb.Moves) || !reflect.DeepEqual(pa.Skipped, pb.Skipped) || !reflect.DeepEqual(pa.Rules, pb.Rules) {
		t.Fatalf("plans differ:\n%+v\n%+v", pa, pb)
	}
	for i := range pa.Moves {
		if !reflect.DeepEqual(pa.Moves[i].Inputs, pb.Moves[i].Inputs) {
			t.Fatalf("inputs differ: %v, %v", pa.Moves[i].Inputs, pb.Moves[i].Inputs)
		}
	}
	if s := skipOf(pa, "pin"); s == nil || s.Reason != SkipPinned {
		t.Fatalf("pinned skip = %+v", s)
	}
}

// TestNotOpenedMinAgeZeroFutureAnchor pins not_opened_for_days with
// "at least 0 days old" and a createdTime in the future (tablet clock ahead):
// the age is negative, so the guard holds the document; without the guard it
// moves. The hold key (Inputs) is {lastOpened, ageAnchor} either way.
func TestNotOpenedMinAgeZeroFutureAnchor(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(48 * time.Hour)
	m := baseTree()
	m.doc("future", "Future", "rr", pdfInfo(5), func(md *models.MetadataFile) { md.CreatedTime = ms(future) })
	inputs := map[string]string{"lastOpened": "", "ageAnchor": ms(future)}

	rule := moveRule("r1", "rr", "read", trig(TriggerNotOpened, P{Days: 1}), cond(automation.CondMinAgeDays, P{Days: 0}))
	// a hold keyed on exactly these inputs survives the sweep only if the
	// evaluator's inputs are the same map (sameInputs), match or not
	st := NewState()
	st.SetHold(Hold{ID: "h1", RuleID: "r1", DocID: "future", Inputs: inputs, Source: "rr", MovedTo: "read", Reason: "undo"})
	p := m.plan(t, now, st, rule)
	if len(p.Moves) != 0 || ruleResult(p, "r1").NotMatching != 1 {
		t.Fatalf("guard on: moves %+v, rule %+v", p.Moves, ruleResult(p, "r1"))
	}
	if len(p.ClearHolds) != 0 {
		t.Fatalf("guard on: inputs are not %v (hold cleared)", inputs)
	}

	rule.Conditions = nil
	p = m.plan(t, now, nil, rule)
	if got := movedIDs(p); !reflect.DeepEqual(got, []string{"future"}) {
		t.Fatalf("guard off: moved %v, want future", got)
	}
	if got := p.Moves[0].Inputs; !reflect.DeepEqual(got, inputs) {
		t.Fatalf("guard off: inputs %v, want %v", got, inputs)
	}
}

// TestDocumentRuleMultiConditionAND not_opened_for_days 30 AND kind_is
// pdf_epub AND tag_is "keep": only the old, unopened, tagged PDF matches. The
// rest count as v1 counted them: not matching, or skipped out of scope.
func TestDocumentRuleMultiConditionAND(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("archive", "Archive", "")
	keep := func(ci ContentInfo) ContentInfo { ci.Tags = []string{"Keep"}; return ci }
	old := func(md *models.MetadataFile) { md.LastOpened = daysAgo(now, 40); md.CreatedTime = daysAgo(now, 100) }
	m.doc("hit", "Old tagged paper", "inbox", keep(pdfInfo(5)), old)
	m.doc("never", "Never opened tagged book", "inbox", keep(ContentInfo{FileType: "epub", PageCount: 9}), func(md *models.MetadataFile) { md.CreatedTime = daysAgo(now, 100) })
	m.doc("recent", "Recently opened", "inbox", keep(pdfInfo(5)), func(md *models.MetadataFile) { md.LastOpened = daysAgo(now, 2) })
	m.doc("untagged", "Old untagged", "inbox", pdfInfo(5), old)
	m.doc("nb", "Old tagged notebook", "inbox", keep(ContentInfo{FileType: "notebook", PageCount: 5}), old)

	r := moveRule("r1", "inbox", "archive", trig(TriggerNotOpened, P{Days: 30}),
		kindIs(ScopePdfEpub), cond(automation.CondTagIs, P{Tag: "keep"}))
	p := m.plan(t, now, nil, r)
	moved := movedIDs(p)
	sort.Strings(moved)
	if got := strings.Join(moved, ","); got != "hit,never" {
		t.Fatalf("moves = %q, want hit and never", got)
	}
	rr := ruleResult(p, "r1")
	if rr.Checked != 5 || rr.Matches != 2 || rr.NotMatching != 2 || rr.OptedOut != 0 {
		t.Fatalf("rule result %+v", rr)
	}
	if s := skipOf(p, "nb"); s == nil || s.Reason != SkipOutOfScope || s.Detail != "notebook, rule applies to PDF & EPUB" {
		t.Fatalf("notebook skip = %+v", s)
	}
	for _, id := range []string{"recent", "untagged"} {
		if s := skipOf(p, id); s != nil {
			t.Errorf("%s skipped (%+v), want not matching", id, s)
		}
	}
	for _, mv := range p.Moves {
		if k := keys(mv.Inputs); !reflect.DeepEqual(k, []string{"ageAnchor", "lastOpened", "tags"}) {
			t.Errorf("%s inputs %v", mv.DocID, k)
		}
		if !strings.HasSuffix(mv.Detail, " · tagged “keep”") {
			t.Errorf("%s detail %q", mv.DocID, mv.Detail)
		}
	}
}
