package pagewatch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

// DefaultMinAge a page blob younger than this is skipped (Input.MinAge 0)
const DefaultMinAge = 10 * time.Minute

// Input everything Detect reads. Snap must have had AttachPages called.
type Input struct {
	Snap    *autoorganize.Snapshot
	Config  *autoorganize.Config
	State   *State
	Content func(*autoorganize.DocSnap) autoorganize.ContentInfo
	// ModTime the blob's write time; an error skips the page (never "old")
	ModTime func(hash string) (time.Time, error)
	Now     time.Time
	// MinAge 0 means DefaultMinAge
	MinAge time.Duration
	User   string
}

// Plan the events of one sweep. Advance returns the next state given the
// trace ids rm-pages accepted; it never changes Input.State and can be
// called more than once.
type Plan struct {
	Feed     []Event    `json:"feed"`
	Pipeline []Event    `json:"pipeline"`
	Skipped  []SkipNote `json:"skipped"`
	// SkipRules the first pipeline rule a skipped page was evaluated for
	// (absent when only the feed saw it)
	SkipRules map[SkipNote]string                   `json:"-"`
	Advance   func(accepted map[string]bool) *State `json:"-"`
}

// Detect compares the snapshot with the baselines and plans the events.
// It is pure: no I/O beyond the Content and ModTime callbacks.
func Detect(in Input) Plan {
	if in.MinAge == 0 {
		in.MinAge = DefaultMinAge
	}
	d := &detector{
		in:       in,
		base:     in.State.Clone(),
		mtimes:    map[string]mtime{},
		seenSkip:  map[SkipNote]bool{},
		skipRules: map[SkipNote]string{},
	}
	if in.Snap == nil || !in.Snap.PagesAttached {
		// no page lists: any comparison would read as "every page deleted"
		d.skip(SkipNote{Reason: SkipNotAttached})
		return d.plan()
	}
	d.collectDocs()
	d.recordSeen()
	d.feed()
	d.pipeline()
	return d.plan()
}

// TraceID the deterministic trace id of a change: 32 lowercase hex, the first
// 16 bytes of sha256 over the parts joined by 0x1f. The same unaccepted
// change gets the same id every sweep, so a re-send (failed save, timeout,
// shutdown mid-request) is recognised by rm-pages and never duplicated.
func TraceID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

// docView a document Detect can compare: its content was readable (or it is
// in the trash, which needs no content)
type docView struct {
	snap    *autoorganize.DocSnap
	ci      autoorganize.ContentInfo
	trashed bool
	path    string
	docType string
	// order the page ids in order; each pipeline event gets its own copy
	order []string
}

type mtime struct {
	at  time.Time
	err error
}

// effect a change Advance applies: always, or when trace is accepted
type effect struct {
	always bool
	trace  string
	apply  func(*State)
}

// join a doc becoming a rule member once every trace is accepted
type join struct {
	ruleID, docID string
	traces        []string
}

// detector the working set of one Detect call
type detector struct {
	in   Input
	base *State
	// docs every non-folder doc that can be compared; docs left alone this
	// sweep (content/location skips) are absent, so nothing touches them
	docs []*docView
	// leftAlone docs skipped this sweep for content or location
	leftAlone map[string]bool
	mtimes    map[string]mtime
	feedEvs   []Event
	pipeEvs   []Event
	skips     []SkipNote
	seenSkip  map[SkipNote]bool
	skipRules map[SkipNote]string
	effects   []effect
	joins     []join
}

func (d *detector) skip(n SkipNote) { d.skipFor(n, "") }

// skipFor a skip seen while evaluating ruleID ("" = the feed)
func (d *detector) skipFor(n SkipNote, ruleID string) {
	if !d.seenSkip[n] {
		d.seenSkip[n] = true
		d.skips = append(d.skips, n)
	}
	if ruleID != "" && d.skipRules[n] == "" {
		d.skipRules[n] = ruleID
	}
}

func (d *detector) always(f func(*State)) {
	d.effects = append(d.effects, effect{always: true, apply: f})
}

func (d *detector) onAccept(trace string, f func(*State)) {
	d.effects = append(d.effects, effect{trace: trace, apply: f})
}

func (d *detector) plan() Plan {
	base, effects, joins := d.base, d.effects, d.joins
	return Plan{
		Feed:      d.feedEvs,
		Pipeline:  d.pipeEvs,
		Skipped:   d.skips,
		SkipRules: d.skipRules,
		Advance: func(accepted map[string]bool) *State {
			s := base.Clone()
			for _, e := range effects {
				if e.always || accepted[e.trace] {
					e.apply(s)
				}
			}
			for _, j := range joins {
				if allAccepted(j.traces, accepted) {
					rs := s.rule(j.ruleID)
					rs.Members[j.docID] = true
					delete(rs.JoinStartedAt, j.docID)
				}
			}
			return s
		},
	}
}

func allAccepted(traces []string, accepted map[string]bool) bool {
	for _, t := range traces {
		if !accepted[t] {
			return false
		}
	}
	return true
}

// collectDocs every document, by id; folders dropped, unreadable or
// unlocatable docs reported and left alone
func (d *detector) collectDocs() {
	snap := d.in.Snap
	ids := make([]string, 0, len(snap.Docs))
	for id := range snap.Docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	d.leftAlone = map[string]bool{}
	for _, id := range ids {
		s := snap.Docs[id]
		if s.IsFolder() {
			continue
		}
		switch snap.Locate(s.Parent) {
		case autoorganize.LocInTrash:
			d.docs = append(d.docs, &docView{snap: s, trashed: true})
			continue
		case autoorganize.LocUnknown:
			d.leftAlone[id] = true
			d.skip(SkipNote{DocID: id, Reason: SkipLocation})
			continue
		}
		if s.PagesErr != "" || s.ContentHash == "" {
			d.leftAlone[id] = true
			d.skip(SkipNote{DocID: id, Reason: SkipContent})
			continue
		}
		ci := d.in.Content(s)
		if !ci.Readable() {
			d.leftAlone[id] = true
			d.skip(SkipNote{DocID: id, Reason: SkipContent})
			continue
		}
		v := &docView{snap: s, ci: ci, path: snap.PathName(id), docType: docType(ci.FileType)}
		for _, p := range s.Pages {
			v.order = append(v.order, p.ID)
		}
		d.docs = append(d.docs, v)
	}
}

func docType(fileType string) string {
	switch strings.ToLower(fileType) {
	case "pdf":
		return DocPDF
	case "epub":
		return DocEPUB
	}
	return DocNotebook
}

// age the page blob's write time; ok false (with a skip note) when the page
// is too recent or its mtime cannot be read. ruleID "" is the feed.
func (d *detector) age(docID, ruleID string, p autoorganize.PageSnap) (time.Time, bool) {
	m, done := d.mtimes[p.Hash]
	if !done {
		if d.in.ModTime == nil {
			m.err = errNoModTime
		} else {
			m.at, m.err = d.in.ModTime(p.Hash)
		}
		d.mtimes[p.Hash] = m
	}
	switch {
	case m.err != nil:
		d.skipFor(SkipNote{DocID: docID, PageID: p.ID, Reason: SkipMtime}, ruleID)
		return time.Time{}, false
	case d.in.Now.Sub(m.at) < d.in.MinAge:
		d.skipFor(SkipNote{DocID: docID, PageID: p.ID, Reason: SkipRecent}, ruleID)
		return time.Time{}, false
	}
	return m.at, true
}

var errNoModTime = errors.New("no ModTime func")

// event a new event for a page of v; the caller sets the trace id once the
// hashes are known
func (d *detector) event(kind string, v *docView, pageID string, changedAt time.Time) Event {
	return Event{
		Kind:      kind,
		User:      d.in.User,
		DocID:     v.snap.ID,
		DocName:   v.snap.Name,
		DocPath:   v.path,
		DocType:   v.docType,
		PageID:    pageID,
		PDFHash:   v.snap.PDFHash,
		ChangedAt: changedAt,
	}
}

// recordSeen the first sweep that saw each doc (R15); docs gone from the
// tree are forgotten. A doc left alone (content/location) is recorded only
// on the very first run: later, a new doc skipped on its first sweep must
// still be new when it becomes processable (R16).
func (d *detector) recordSeen() {
	now := d.in.Now
	var add, drop []string
	for _, id := range sortedKeys(d.in.Snap.Docs) {
		if _, ok := d.base.Seen[id]; ok || d.in.Snap.Docs[id].IsFolder() {
			continue
		}
		if d.leftAlone[id] && d.base.FeedInitialised {
			continue
		}
		add = append(add, id)
	}
	for _, id := range sortedKeys(d.base.Seen) {
		if _, ok := d.in.Snap.Docs[id]; !ok {
			drop = append(drop, id)
		}
	}
	if len(add)+len(drop) == 0 {
		return
	}
	d.always(func(s *State) {
		for _, id := range add {
			s.Seen[id] = now
		}
		for _, id := range drop {
			delete(s.Seen, id)
		}
	})
}

// seenAt when the doc was first seen; this sweep if never before
func (d *detector) seenAt(docID string) time.Time {
	if t, ok := d.base.Seen[docID]; ok {
		return t
	}
	return d.in.Now
}

// pagesOf baseline page ids by doc, sorted
func pagesOf(baselines map[string]string) map[string][]string {
	out := map[string][]string{}
	for key := range baselines {
		if i := strings.LastIndex(key, "/"); i > 0 {
			out[key[:i]] = append(out[key[:i]], key[i+1:])
		}
	}
	for _, ps := range out {
		sort.Strings(ps)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- timeline feed: every non-trash document, ignores Enabled/Mode ----

func (d *detector) feed() {
	d.pruneFeed()
	if !d.base.FeedInitialised {
		d.initFeed()
		return
	}
	for _, v := range d.docs {
		if v.trashed {
			continue
		}
		d.feedOpen(v)
		d.feedEdits(v)
	}
}

// initFeed the first run records baselines and emits nothing
func (d *detector) initFeed() {
	feed, opened := map[string]string{}, map[string]string{}
	for _, v := range d.docs {
		if v.trashed {
			continue
		}
		for _, p := range v.snap.Pages {
			if p.Hash != "" {
				feed[pageKey(v.snap.ID, p.ID)] = p.Hash
			}
		}
		if v.snap.LastOpened != "" {
			opened[v.snap.ID] = v.snap.LastOpened
		}
	}
	d.always(func(s *State) {
		for k, h := range feed {
			s.Feed[k] = h
		}
		for k, o := range opened {
			s.Opened[k] = o
		}
		s.FeedInitialised = true
	})
}

// pruneFeed drops baselines of docs gone from the tree and of pages gone
// from a readable doc; trashed and left-alone docs keep theirs
func (d *detector) pruneFeed() {
	live := map[string]map[string]bool{}
	for _, v := range d.docs {
		if v.trashed {
			continue
		}
		pages := map[string]bool{}
		for _, p := range v.snap.Pages {
			pages[p.ID] = true
		}
		live[v.snap.ID] = pages
	}
	var feedKeys, openedKeys []string
	for doc, pages := range pagesOf(d.base.Feed) {
		_, inTree := d.in.Snap.Docs[doc]
		for _, p := range pages {
			if lp, ok := live[doc]; !inTree || (ok && !lp[p]) {
				feedKeys = append(feedKeys, pageKey(doc, p))
			}
		}
	}
	for _, doc := range sortedKeys(d.base.Opened) {
		if _, inTree := d.in.Snap.Docs[doc]; !inTree {
			openedKeys = append(openedKeys, doc)
		}
	}
	if len(feedKeys)+len(openedKeys) == 0 {
		return
	}
	d.always(func(s *State) {
		for _, k := range feedKeys {
			delete(s.Feed, k)
		}
		for _, k := range openedKeys {
			delete(s.Opened, k)
		}
	})
}

// feedOpen an open when lastOpened changed: the last-opened page
func (d *detector) feedOpen(v *docView) {
	cur := v.snap.LastOpened
	if cur == "" || cur == d.base.Opened[v.snap.ID] {
		return
	}
	ms, err := strconv.ParseInt(cur, 10, 64)
	if err != nil || ms <= 0 {
		return
	}
	at := time.UnixMilli(ms).UTC()
	idx := v.snap.LastOpenedPage
	var page autoorganize.PageSnap
	if idx >= 0 && idx < len(v.snap.Pages) {
		page = v.snap.Pages[idx]
	}
	ev := d.event(KindOpen, v, page.ID, at)
	ev.TraceID = TraceID("feed", "open", v.snap.ID, at.Format(time.RFC3339Nano))
	ev.OpenedAt = &at
	ev.PageIndex, ev.AfterHash = idx, page.Hash
	d.feedEvs = append(d.feedEvs, ev)
	id := v.snap.ID
	d.onAccept(ev.TraceID, func(s *State) { s.Opened[id] = cur })
}

// feedEdits an edit per page whose hash differs from the feed baseline
func (d *detector) feedEdits(v *docView) {
	for _, p := range v.snap.Pages {
		key := pageKey(v.snap.ID, p.ID)
		prev := d.base.Feed[key]
		if p.Hash == "" || p.Hash == prev {
			continue
		}
		at, ok := d.age(v.snap.ID, "", p)
		if !ok {
			continue
		}
		ev := d.event(KindEdit, v, p.ID, at)
		ev.PageIndex, ev.BeforeHash, ev.AfterHash = p.Index, prev, p.Hash
		ev.TraceID = TraceID("feed", "edit", v.snap.ID, p.ID, prev, p.Hash)
		d.feedEvs = append(d.feedEvs, ev)
		hash := p.Hash
		d.onAccept(ev.TraceID, func(s *State) { s.Feed[key] = hash })
	}
}

// ---- pipeline: enabled pipeline rules, their watched documents ----

func (d *detector) pipeline() {
	cfg := d.in.Config
	if cfg == nil || !cfg.Settings.Enabled {
		return
	}
	for i := range cfg.Rules {
		if r := &cfg.Rules[i]; r.Enabled && r.IsPipeline() {
			d.rule(r)
		}
	}
}

func (d *detector) rule(r *autoorganize.Rule) {
	base := d.base.Rules[r.ID]
	if base == nil {
		base = newRuleState()
	}
	held := pagesOf(base.Baselines)
	known := func(doc string) bool {
		_, joining := base.JoinStartedAt[doc]
		return base.Members[doc] || len(held[doc]) > 0 || joining
	}
	for _, v := range d.docs {
		if d.inRule(r, v) {
			d.ruleDoc(r, base, held[v.snap.ID], v)
		} else if known(v.snap.ID) {
			d.leave(r.ID, v.snap.ID)
		}
	}
	// docs gone from the tree leave too; left-alone docs are still in it
	gone := map[string]bool{}
	for doc := range base.Members {
		gone[doc] = true
	}
	for doc := range held {
		gone[doc] = true
	}
	for doc := range base.JoinStartedAt {
		gone[doc] = true
	}
	for _, doc := range sortedKeys(gone) {
		if _, inTree := d.in.Snap.Docs[doc]; !inTree {
			d.leave(r.ID, doc)
		}
	}
	ruleID, now := r.ID, d.in.Now
	d.always(func(s *State) { s.rule(ruleID).LastActive = now })
}

// leave a doc no longer in the rule: no events; it drops its membership and
// baselines, so a later re-join sends every page as transcribe_only again
func (d *detector) leave(ruleID, docID string) {
	d.always(func(s *State) {
		rs := s.rule(ruleID)
		delete(rs.Members, docID)
		delete(rs.JoinStartedAt, docID)
		for k := range rs.Baselines {
			if strings.HasPrefix(k, docID+"/") {
				delete(rs.Baselines, k)
			}
		}
	})
}

// inRule the doc is watched by the rule, in scope, and not opted out
func (d *detector) inRule(r *autoorganize.Rule, v *docView) bool {
	if v.trashed || v.ci.HasNoAutomationTag() {
		return false
	}
	if r.Scope != "" && r.Scope != autoorganize.ScopeAll && v.ci.Kind() != r.Scope {
		return false
	}
	for _, w := range r.Watch {
		switch w.Kind {
		case autoorganize.WatchDocument:
			if w.ID == v.snap.ID {
				return true
			}
		case autoorganize.WatchFolder:
			if d.under(v.snap.Parent, w.ID) {
				return true
			}
		}
	}
	return false
}

// under folder is parent or one of its ancestors (the root holds everything
// outside the trash)
func (d *detector) under(parent, folder string) bool {
	if folder == autoorganize.RootID {
		return true
	}
	seen := map[string]bool{}
	for id := parent; id != autoorganize.RootID && !seen[id]; {
		if id == folder {
			return true
		}
		seen[id] = true
		f := d.in.Snap.Docs[id]
		if f == nil {
			return false
		}
		id = f.Parent
	}
	return false
}

// ruleDoc a member gets an edit per changed page. A joining doc (content that
// existed before the rule watched it) gets a transcribe_only per page not yet
// delivered whose blob is no newer than the join's start, and an edit for any
// other changed page (delivered earlier in the join and changed since, or
// written after the join started); it becomes a member once all of them are
// accepted with none skipped. A doc first seen after the rule last looked is
// new content and a member at once (R12, R15). Both get a page_deleted per
// page the baseline holds that is gone.
func (d *detector) ruleDoc(r *autoorganize.Rule, base *RuleState, held []string, v *docView) {
	docID, ruleID, now := v.snap.ID, r.ID, d.in.Now
	member := base.Members[docID]
	if !member && !base.LastActive.IsZero() && d.seenAt(docID).After(base.LastActive) {
		// new content: its pages are edits with an empty before, and a page
		// skipped now is still an edit next sweep
		member = true
		d.always(func(s *State) { s.rule(ruleID).Members[docID] = true })
	}
	joinStart, started := base.JoinStartedAt[docID]
	switch {
	case !member && !started:
		// the first sweep of the join: a page written after now is new
		// writing even while an old page holds the join open (R12)
		joinStart = now
		d.always(func(s *State) { s.rule(ruleID).JoinStartedAt[docID] = now })
	case member && started:
		d.always(func(s *State) { delete(s.rule(ruleID).JoinStartedAt, docID) })
	}
	var traces []string
	complete := true
	current := map[string]bool{}
	for _, p := range v.snap.Pages {
		current[p.ID] = true
		key := pageKey(docID, p.ID)
		prev := base.Baselines[key]
		if p.Hash == "" || p.Hash == prev {
			continue
		}
		at, ok := d.age(docID, ruleID, p)
		if !ok {
			complete = false
			continue
		}
		// prev != "" on a non-member: delivered earlier in this join (leaving
		// clears baselines), so the change is new writing; so is a blob
		// written after the join started. Over-capture is the safe side.
		kind := KindTranscribeOnly
		if member || prev != "" || at.After(joinStart) {
			kind = KindEdit
		}
		ev := d.pipelineEvent(kind, r, v, p.ID, at)
		ev.PageIndex, ev.AfterHash = p.Index, p.Hash
		if kind == KindEdit {
			ev.BeforeHash = prev
		}
		ev.TraceID = TraceID("rule", ruleID, kind, docID, p.ID, ev.BeforeHash, ev.AfterHash)
		d.pipeEvs = append(d.pipeEvs, ev)
		hash := p.Hash
		d.onAccept(ev.TraceID, func(s *State) { s.rule(ruleID).Baselines[key] = hash })
		traces = append(traces, ev.TraceID)
	}
	for _, pageID := range held {
		if current[pageID] {
			continue
		}
		ev := d.pipelineEvent(KindPageDeleted, r, v, pageID, now)
		ev.PageIndex = -1
		// the deleted page's baseline stands in for before_hash, so a page
		// that is re-created and deleted again gets a new id
		ev.TraceID = TraceID("rule", ruleID, KindPageDeleted, docID, pageID, base.Baselines[pageKey(docID, pageID)], "")
		d.pipeEvs = append(d.pipeEvs, ev)
		key := pageKey(docID, pageID)
		d.onAccept(ev.TraceID, func(s *State) { delete(s.rule(ruleID).Baselines, key) })
		traces = append(traces, ev.TraceID)
	}
	if !member && complete {
		d.joins = append(d.joins, join{ruleID: ruleID, docID: docID, traces: traces})
	}
}

// pipelineEvent a rule's event; index and hashes are left to the caller
func (d *detector) pipelineEvent(kind string, r *autoorganize.Rule, v *docView, pageID string, at time.Time) Event {
	ev := d.event(kind, v, pageID, at)
	ev.Watched = true
	ev.RuleID = r.ID
	ev.Prompt = r.Action.Prompt
	if ev.Prompt == "" {
		ev.Prompt = autoorganize.DefaultPipelinePrompt
	}
	ev.Model = r.Action.Model
	if ev.Model == "" {
		ev.Model = autoorganize.DefaultPipelineModel
	}
	ev.PageOrder = append([]string(nil), v.order...)
	return ev
}
