package pagewatch

import (
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/google/uuid"
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
	// NewID mints trace ids; nil means UUID4 hex
	NewID func() string
}

// Plan the events of one sweep. Advance returns the next state given the
// trace ids rm-pages accepted; it never changes Input.State and can be
// called more than once.
type Plan struct {
	Feed     []Event                               `json:"feed"`
	Pipeline []Event                               `json:"pipeline"`
	Skipped  []SkipNote                            `json:"skipped"`
	Advance  func(accepted map[string]bool) *State `json:"-"`
}

// Detect compares the snapshot with the baselines and plans the events.
// It is pure: no I/O beyond the Content and ModTime callbacks.
func Detect(in Input) Plan {
	if in.MinAge == 0 {
		in.MinAge = DefaultMinAge
	}
	if in.NewID == nil {
		in.NewID = newTraceID
	}
	d := &detector{
		in:       in,
		base:     in.State.Clone(),
		mtimes:   map[string]mtime{},
		seenSkip: map[SkipNote]bool{},
	}
	if in.Snap == nil || !in.Snap.PagesAttached {
		// no page lists: any comparison would read as "every page deleted"
		d.skip(SkipNote{Reason: SkipNotAttached})
		return d.plan()
	}
	d.collectDocs()
	d.feed()
	d.pipeline()
	return d.plan()
}

func newTraceID() string {
	u := uuid.New()
	return hex.EncodeToString(u[:])
}

// docView a document Detect can compare: its content was readable (or it is
// in the trash, which needs no content)
type docView struct {
	snap    *autoorganize.DocSnap
	ci      autoorganize.ContentInfo
	trashed bool
	// fresh created since the last sweep: the initialised feed has no
	// baseline for it, so its pages are new writing (R12)
	fresh   bool
	path    string
	docType string
	// order the page ids in order, shared by the doc's pipeline events
	order []string
}

type mtime struct {
	at  time.Time
	err error
}

// effect a change Advance applies; trace "" means always
type effect struct {
	trace string
	apply func(*State)
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
	docs     []*docView
	mtimes   map[string]mtime
	feedEvs  []Event
	pipeEvs  []Event
	skips    []SkipNote
	seenSkip map[SkipNote]bool
	effects  []effect
	joins    []join
}

func (d *detector) skip(n SkipNote) {
	if !d.seenSkip[n] {
		d.seenSkip[n] = true
		d.skips = append(d.skips, n)
	}
}

func (d *detector) always(f func(*State)) {
	d.effects = append(d.effects, effect{apply: f})
}

func (d *detector) onAccept(trace string, f func(*State)) {
	d.effects = append(d.effects, effect{trace: trace, apply: f})
}

func (d *detector) plan() Plan {
	base, effects, joins := d.base, d.effects, d.joins
	return Plan{
		Feed:     d.feedEvs,
		Pipeline: d.pipeEvs,
		Skipped:  d.skips,
		Advance: func(accepted map[string]bool) *State {
			s := base.Clone()
			for _, e := range effects {
				if e.trace == "" || accepted[e.trace] {
					e.apply(s)
				}
			}
			for _, j := range joins {
				if allAccepted(j.traces, accepted) {
					s.rule(j.ruleID).Members[j.docID] = true
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
	feedDocs := pagesOf(d.base.Feed)
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
			d.skip(SkipNote{DocID: id, Reason: SkipLocation})
			continue
		}
		if s.PagesErr != "" || s.ContentHash == "" {
			d.skip(SkipNote{DocID: id, Reason: SkipContent})
			continue
		}
		ci := d.in.Content(s)
		if !ci.Readable() {
			d.skip(SkipNote{DocID: id, Reason: SkipContent})
			continue
		}
		v := &docView{snap: s, ci: ci, path: snap.PathName(id), docType: docType(ci.FileType)}
		v.fresh = d.base.FeedInitialised && len(feedDocs[id]) == 0
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
// is too recent or its mtime cannot be read
func (d *detector) age(docID string, p autoorganize.PageSnap) (time.Time, bool) {
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
		d.skip(SkipNote{DocID: docID, PageID: p.ID, Reason: SkipMtime})
		return time.Time{}, false
	case d.in.Now.Sub(m.at) < d.in.MinAge:
		d.skip(SkipNote{DocID: docID, PageID: p.ID, Reason: SkipRecent})
		return time.Time{}, false
	}
	return m.at, true
}

var errNoModTime = errors.New("no ModTime func")

func (d *detector) event(kind string, v *docView, changedAt time.Time) Event {
	return Event{
		TraceID:   d.in.NewID(),
		Kind:      kind,
		User:      d.in.User,
		DocID:     v.snap.ID,
		DocName:   v.snap.Name,
		DocPath:   v.path,
		DocType:   v.docType,
		PDFHash:   v.snap.PDFHash,
		ChangedAt: changedAt,
	}
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
	ev := d.event(KindOpen, v, at)
	ev.OpenedAt = &at
	ev.PageIndex = v.snap.LastOpenedPage
	if i := ev.PageIndex; i >= 0 && i < len(v.snap.Pages) {
		ev.PageID = v.snap.Pages[i].ID
		ev.AfterHash = v.snap.Pages[i].Hash
	}
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
		at, ok := d.age(v.snap.ID, p)
		if !ok {
			continue
		}
		ev := d.event(KindEdit, v, at)
		ev.PageID, ev.PageIndex, ev.BeforeHash, ev.AfterHash = p.ID, p.Index, prev, p.Hash
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
	known := func(doc string) bool { return base.Members[doc] || len(held[doc]) > 0 }
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
	for _, doc := range sortedKeys(gone) {
		if _, inTree := d.in.Snap.Docs[doc]; !inTree {
			d.leave(r.ID, doc)
		}
	}
}

// leave a doc no longer in the rule: no events; it drops its membership and
// baselines, so a later re-join sends every page as transcribe_only again
func (d *detector) leave(ruleID, docID string) {
	d.always(func(s *State) {
		rs := s.rule(ruleID)
		delete(rs.Members, docID)
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

// ruleDoc a member gets an edit per changed page; a joining doc (content that
// existed before the rule watched it) gets a transcribe_only per page not yet
// delivered, and becomes a member once all of them are accepted with none
// skipped. A doc created since the last sweep is a member at once (R12). Both get a page_deleted per page
// the baseline holds that is gone.
func (d *detector) ruleDoc(r *autoorganize.Rule, base *RuleState, held []string, v *docView) {
	docID, ruleID := v.snap.ID, r.ID
	member := base.Members[docID]
	if !member && v.fresh {
		// a new doc is a member at once: its pages are edits with an empty
		// before, and a page skipped now is still an edit next sweep
		member = true
		d.always(func(s *State) { s.rule(ruleID).Members[docID] = true })
	}
	kind := KindEdit
	if !member {
		kind = KindTranscribeOnly
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
		at, ok := d.age(docID, p)
		if !ok {
			complete = false
			continue
		}
		ev := d.pipelineEvent(kind, r, v, at)
		ev.PageID, ev.PageIndex, ev.AfterHash = p.ID, p.Index, p.Hash
		if member {
			ev.BeforeHash = prev
		}
		d.pipeEvs = append(d.pipeEvs, ev)
		hash := p.Hash
		d.onAccept(ev.TraceID, func(s *State) { s.rule(ruleID).Baselines[key] = hash })
		traces = append(traces, ev.TraceID)
	}
	for _, pageID := range held {
		if current[pageID] {
			continue
		}
		ev := d.pipelineEvent(KindPageDeleted, r, v, d.in.Now)
		ev.PageID, ev.PageIndex = pageID, -1
		d.pipeEvs = append(d.pipeEvs, ev)
		key := pageKey(docID, pageID)
		d.onAccept(ev.TraceID, func(s *State) { delete(s.rule(ruleID).Baselines, key) })
		traces = append(traces, ev.TraceID)
	}
	if !member && complete {
		d.joins = append(d.joins, join{ruleID: ruleID, docID: docID, traces: traces})
	}
}

// pipelineEvent a rule's event, page fields left to the caller
func (d *detector) pipelineEvent(kind string, r *autoorganize.Rule, v *docView, at time.Time) Event {
	ev := d.event(kind, v, at)
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
	ev.PageOrder = v.order
	return ev
}
