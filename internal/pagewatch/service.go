package pagewatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	log "github.com/sirupsen/logrus"
)

const logPrefix = "[page-watch] "

// Run triggers, shown in RunEntry.Trigger
const (
	TriggerStart  = "start"
	TriggerTimer  = "timer"
	TriggerRunNow = "run now"
)

// Run modes, in RunEntry.Mode
const (
	ModeDry  = "dry"
	ModeLive = "live"
)

// LagRuns a pipeline event emitted but not accepted for more than this many
// runs in a row logs one Error (the baseline lags the page)
const LagRuns = 2

// maxSendErrors per failed send, this many events are logged one by one
const maxSendErrors = 10

// One POST carries at most this many events and about this many bytes of
// JSON, well under rm-pages' 8 MiB body limit and its 10 s timeout; a big
// first batch goes out in several POSTs.
const (
	DefaultChunkEvents = 200
	DefaultChunkBytes  = 2 << 20
)

// Errors returned to callers
var (
	ErrBusy   = errors.New("run in progress")
	ErrSync10 = errors.New("page watch requires sync15")
)

// Service the page watch: one loop per sync15 user. Each run loads the
// snapshot, calls Detect, sends the events to rm-pages and saves the next
// state. The automation config (automation.json, through the sweeper's
// store) is read, never written; the page watch
// keeps its own state file and content cache, so the two services never share
// a file.
type Service struct {
	storage autoorganize.Storage
	cfgs    *autoorganize.Store
	sender  Sender
	store   *Store

	// Now the clock, replaceable in tests
	Now func() time.Time
	// MinAge a page blob younger than this is skipped by timer runs
	// (DefaultMinAge unless set; 0 = no skip window). Run now skips no page.
	MinAge time.Duration
	// UserRescan how often the user list is re-read to start new loops
	UserRescan time.Duration
	// IntervalUnit the unit of Settings.IntervalMinutes (a minute; shorter in tests)
	IntervalUnit time.Duration
	// ChunkEvents, ChunkBytes the most events / JSON bytes per POST
	ChunkEvents int
	ChunkBytes  int

	mu    sync.Mutex
	users map[string]*userLoop
	ctx   context.Context
	wg    sync.WaitGroup
}

type userLoop struct {
	uid string
	// runMu one run at a time; guards cache and lag
	runMu sync.Mutex
	// cache .content infos by blob hash, kept in memory between runs
	cache map[string]autoorganize.ContentInfo
	// lag consecutive runs a (rule, doc, page) event was not accepted
	lag     map[string]int
	alerted map[string]bool
	// skips consecutive runs a (doc, page) was skipped; skipAlerted the
	// streaks that already logged their Error
	skips       map[string]int
	skipAlerted map[string]bool
	// missingAlerted the (rule, watch id) entries already reported missing
	missingAlerted map[string]bool
	// missing the last run's missing watch entries, for Status
	missing atomic.Value // []MissingWatch
	started bool         // guarded by Service.mu
	nextAt  atomic.Int64
	// pending pipeline events not accepted by the last run
	pending atomic.Int64
	lastErr atomic.Value // string
}

// NewService a page watch; call Start to run the loops
func NewService(st autoorganize.Storage, cfgs *autoorganize.Store, sender Sender) *Service {
	return &Service{
		storage:      st,
		cfgs:         cfgs,
		sender:       sender,
		store:        NewStore(st.UserDataPath),
		Now:          time.Now,
		MinAge:       DefaultMinAge,
		UserRescan:   time.Minute,
		IntervalUnit: time.Minute,
		ChunkEvents:  DefaultChunkEvents,
		ChunkBytes:   DefaultChunkBytes,
		users:        map[string]*userLoop{},
	}
}

// Store the state and run-log store
func (s *Service) Store() *Store { return s.store }

func (s *Service) loop(uid string) *userLoop {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loopLocked(uid)
}

func (s *Service) loopLocked(uid string) *userLoop {
	ul, ok := s.users[uid]
	if !ok {
		ul = &userLoop{
			uid: uid, cache: map[string]autoorganize.ContentInfo{},
			lag: map[string]int{}, alerted: map[string]bool{},
			skips: map[string]int{}, skipAlerted: map[string]bool{},
			missingAlerted: map[string]bool{},
		}
		ul.lastErr.Store("")
		ul.missing.Store([]MissingWatch{})
		s.users[uid] = ul
	}
	return ul
}

// Start runs the supervisor until ctx is cancelled. It returns at once.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.wg.Add(1)
	go s.supervise(ctx)
}

// Wait for every loop to exit after the context was cancelled
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) supervise(ctx context.Context) {
	defer s.wg.Done()
	for {
		users, err := s.storage.GetUsers()
		if err != nil {
			log.Warn(logPrefix, "cannot read users: ", err)
		}
		for _, u := range users {
			if u.Sync15 {
				s.startLoop(ctx, u.ID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.UserRescan):
		}
	}
}

func (s *Service) startLoop(ctx context.Context, uid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ul := s.loopLocked(uid)
	if ul.started || ctx.Err() != nil {
		return
	}
	ul.started = true
	s.wg.Add(1)
	go s.runLoop(ctx, ul)
}

// interval the user's period, re-read every iteration so a UI change applies
func (s *Service) interval(uid string) time.Duration {
	m := autoorganize.DefaultInterval
	if cfg, err := s.cfgs.LoadConfig(uid); err == nil && cfg.Settings.IntervalMinutes > 0 {
		m = cfg.Settings.IntervalMinutes
	}
	return time.Duration(m) * s.IntervalUnit
}

func (s *Service) runLoop(ctx context.Context, ul *userLoop) {
	defer s.wg.Done()
	log.Info(logPrefix, "watcher started for ", ul.uid)
	trigger := TriggerStart
	for {
		ul.runMu.Lock()
		run, err := s.runLocked(ctx, ul, trigger)
		ul.runMu.Unlock()
		if err != nil && run == nil && !errors.Is(err, ErrSync10) {
			// runs that started already logged their failure at Error
			log.Warn(logPrefix, ul.uid, ": ", err)
		}
		wait := s.interval(ul.uid)
		ul.nextAt.Store(s.Now().Add(wait).UnixMilli())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		trigger = TriggerTimer
	}
}

// RunNow one run now. It returns ErrBusy while another run is going. A
// failed send is not an error here: the entry's Error holds it. The error
// result is for runs that could not start or finish locally (config, tree,
// state); the entry is recorded then too.
func (s *Service) RunNow(uid string) (*RunEntry, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	if !ul.runMu.TryLock() {
		return nil, ErrBusy
	}
	defer ul.runMu.Unlock()
	ctx := context.Background()
	s.mu.Lock()
	if s.ctx != nil {
		ctx = s.ctx
	}
	s.mu.Unlock()
	return s.runLocked(ctx, ul, TriggerRunNow)
}

func (s *Service) checkSync15(uid string) error {
	u, err := s.storage.GetUser(uid)
	if err != nil {
		return err
	}
	if !u.Sync15 {
		return ErrSync10
	}
	return nil
}

// StatusView what the UI shows about the watcher
type StatusView struct {
	LastRun *RunEntry `json:"lastRun"`
	// NextAt unix milliseconds of the next timed run, 0 when no loop is waiting
	NextAt int64 `json:"nextAt"`
	// PendingPipeline pipeline events of the last run that rm-pages did not
	// take (all of them in dry mode)
	PendingPipeline int    `json:"pendingPipeline"`
	LastError       string `json:"lastError"`
	// MissingWatches watch entries whose folder or doc no longer exists
	MissingWatches []MissingWatch `json:"missingWatches"`
}

// Status of uid's watcher
func (s *Service) Status(uid string) (*StatusView, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	runs, err := s.store.LoadRuns(uid)
	if err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	v := &StatusView{NextAt: ul.nextAt.Load(), PendingPipeline: int(ul.pending.Load())}
	v.LastError, _ = ul.lastErr.Load().(string)
	v.MissingWatches, _ = ul.missing.Load().([]MissingWatch)
	if len(runs) > 0 {
		v.LastRun = &runs[0]
	}
	return v, nil
}

// runLocked one run; the caller holds ul.runMu
func (s *Service) runLocked(ctx context.Context, ul *userLoop, trigger string) (*RunEntry, error) {
	uid := ul.uid
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	start := s.Now()
	run := &RunEntry{At: start.UnixMilli(), Trigger: trigger, Mode: ModeLive}
	fail := func(what string, err error, extra log.Fields) (*RunEntry, error) {
		run.Error = what + ": " + err.Error()
		f := log.Fields{"stage": "detect", "user": uid}
		for k, v := range extra {
			f[k] = v
		}
		log.WithFields(f).Error(logPrefix, run.Error)
		s.finish(ul, run)
		return run, err
	}
	cfg, err := s.cfgs.LoadConfig(uid)
	if err != nil {
		return fail("cannot load config", err, nil)
	}
	dry := cfg.Settings.Mode != autoorganize.ModeLive
	if dry {
		run.Mode = ModeDry
	}
	st, err := s.store.LoadState(uid)
	if err != nil {
		return fail("cannot load state", err, nil)
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return fail("cannot load tree", err, nil)
	}
	blobs := s.storage.BlobStorage(uid)
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(ul.cache, blobs.GetReader)
	snap.AttachPages(cc.Get)
	// Run now is Mike pressing the button right after writing: no settle
	// window, every changed page goes now. The timer keeps MinAge.
	minAge := s.MinAge
	if trigger == TriggerRunNow {
		minAge = 0
	}
	plan := Detect(Input{
		Snap: snap, Config: cfg, State: st, Content: cc.Get, ModTime: blobs.ModTime,
		Now: start, MinAge: minAge, User: uid,
	})
	for h := range ul.cache {
		if !cc.Used()[h] {
			delete(ul.cache, h)
		}
	}
	if plan.Abort != "" {
		return fail("snapshot not trusted", errors.New(plan.Abort), nil)
	}
	run.Feed, run.Pipeline, run.Skipped = len(plan.Feed), len(plan.Pipeline), len(plan.Skipped)
	s.trackSkips(ul, plan)
	s.trackMissing(ul, plan)

	// dry mode sends the feed only; the pipeline events stay planned, so their
	// baselines do not move
	send := append([]Event(nil), plan.Feed...)
	if !dry {
		send = append(send, plan.Pipeline...)
	}
	accepted, sendErr := s.deliver(ctx, uid, send)
	if sendErr != nil {
		run.Error = "send failed: " + sendErr.Error()
	}
	for _, ev := range send {
		if accepted[ev.TraceID] {
			run.Accepted++
			if ev.Watched {
				run.PipelineAccepted++
			}
		}
	}
	// Advance(empty) still applies the "always" effects (Seen, LastActive,
	// first-run baselines), so a failed send persists those and nothing else
	next := plan.Advance(acceptedOnly(accepted, send))
	s.trackLag(ul, plan, accepted, dry)
	var pending int64
	for _, ev := range plan.Pipeline {
		if dry || !accepted[ev.TraceID] {
			pending++
		}
	}
	ul.pending.Store(pending)
	if err := s.store.SaveState(uid, next); err != nil {
		// the events are re-sent next run under the same trace ids
		ids := make([]string, 0, len(accepted))
		for _, ev := range send {
			if accepted[ev.TraceID] {
				ids = append(ids, ev.TraceID)
			}
		}
		first := ids[:min(len(ids), 5)]
		return fail("cannot save state", err, log.Fields{"accepted_count": len(ids), "accepted_first": first})
	}
	s.finish(ul, run)
	return run, nil
}

// chunks splits events into POST-sized groups, in order. No events is one
// empty group: the heartbeat. An event bigger than maxBytes goes alone.
func chunks(events []Event, maxEvents, maxBytes int) [][]Event {
	if len(events) == 0 {
		return [][]Event{{}}
	}
	var out [][]Event
	var cur []Event
	size := 0
	for _, ev := range events {
		n := 1
		if b, err := json.Marshal(ev); err == nil {
			n = len(b) + 1
		}
		if len(cur) > 0 && (len(cur) >= maxEvents || size+n > maxBytes) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, ev)
		size += n
	}
	return append(out, cur)
}

// deliver sends the events in chunks and returns the union of the ids
// rm-pages accepted. A failed chunk loses only its own ids (they go again
// next run under the same trace ids); the error names the failed chunks.
func (s *Service) deliver(ctx context.Context, uid string, send []Event) (map[string]bool, error) {
	parts := chunks(send, max(s.ChunkEvents, 1), max(s.ChunkBytes, 1))
	accepted := map[string]bool{}
	var firstErr error
	failed := 0
	for _, part := range parts {
		acc, err := s.sender.Send(ctx, uid, part)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			s.logSendFailure(ctx, uid, part, err)
			continue
		}
		for _, ev := range part {
			if acc[ev.TraceID] {
				accepted[ev.TraceID] = true
				sendLog(ev).Info(logPrefix, "delivered ", ev.Kind)
			} else {
				sendLog(ev).Warn(logPrefix, "rm-pages did not accept ", ev.Kind)
			}
		}
	}
	if firstErr == nil {
		return accepted, nil
	}
	if len(parts) == 1 {
		return accepted, firstErr
	}
	return accepted, fmt.Errorf("%d of %d chunks failed: %w", failed, len(parts), firstErr)
}

// acceptedOnly the accepted ids among those actually sent
func acceptedOnly(accepted map[string]bool, sent []Event) map[string]bool {
	out := map[string]bool{}
	for _, ev := range sent {
		if accepted[ev.TraceID] {
			out[ev.TraceID] = true
		}
	}
	return out
}

func (s *Service) finish(ul *userLoop, run *RunEntry) {
	ul.lastErr.Store(run.Error)
	if err := s.store.AppendRun(ul.uid, *run); err != nil {
		log.Warn(logPrefix, "cannot record run: ", err)
	}
}

func sendLog(ev Event) *log.Entry {
	return log.WithFields(log.Fields{
		"trace_id": ev.TraceID, "stage": "deliver", "doc_id": ev.DocID, "page_id": ev.PageID, "rule_id": ev.RuleID,
		"kind": ev.Kind, "before_hash": ev.BeforeHash, "after_hash": ev.AfterHash,
	})
}

// logSendFailure Error for each failed event (capped); a send cut short by
// shutdown is only a Warn, the events go again next run
func (s *Service) logSendFailure(ctx context.Context, uid string, send []Event, err error) {
	level := log.ErrorLevel
	if ctx.Err() != nil {
		level = log.WarnLevel
	}
	if len(send) == 0 {
		log.WithFields(log.Fields{"trace_id": "heartbeat", "stage": "deliver", "user": uid}).Log(level, logPrefix, "heartbeat failed: ", err)
		return
	}
	for i, ev := range send {
		if i == maxSendErrors {
			log.WithFields(log.Fields{"stage": "deliver", "user": uid}).Log(level, logPrefix, "send failed for ", len(send)-i, " more events: ", err)
			return
		}
		sendLog(ev).Log(level, logPrefix, "send failed: ", err)
	}
}

// trackLag counts, per (rule, doc, page), the runs in a row a pipeline event
// was emitted but not accepted, and logs one Error when the streak passes
// LagRuns. Dry mode sends no pipeline events on purpose, so it counts nothing.
func (s *Service) trackLag(ul *userLoop, plan Plan, accepted map[string]bool, dry bool) {
	seen := map[string]bool{}
	if !dry {
		for _, ev := range plan.Pipeline {
			if ev.PageID == "" {
				continue
			}
			key := ev.RuleID + "|" + ev.DocID + "|" + ev.PageID
			seen[key] = true
			if accepted[ev.TraceID] {
				delete(ul.lag, key)
				delete(ul.alerted, key)
				continue
			}
			ul.lag[key]++
			if ul.lag[key] > LagRuns && !ul.alerted[key] {
				ul.alerted[key] = true
				log.WithFields(log.Fields{
					"trace_id": ev.TraceID, "stage": "detect", "doc_id": ev.DocID, "page_id": ev.PageID, "rule_id": ev.RuleID,
				}).Error(logPrefix, "pipeline baseline lags the page for ", ul.lag[key], " runs")
			}
		}
	}
	for key := range ul.lag {
		if !seen[key] {
			delete(ul.lag, key)
			delete(ul.alerted, key)
		}
	}
}

// trackSkips logs every skip at Info and counts, per (doc, page), the runs in
// a row it was skipped; past LagRuns it logs one Error per streak, so a page
// that is never readable (or a doc with no .content) cannot stay silent.
// Recent skips never count: continuous writing is benign, and the page is
// sent once it settles.
func (s *Service) trackSkips(ul *userLoop, plan Plan) {
	seen := map[string]bool{}
	for _, n := range plan.Skipped {
		key := n.DocID + "|" + n.PageID
		if seen[key] {
			continue
		}
		seen[key] = true
		f := log.Fields{"stage": "detect", "user": ul.uid, "doc_id": n.DocID, "page_id": n.PageID, "reason": n.Reason}
		if rid := plan.SkipRules[n]; rid != "" {
			f["rule_id"] = rid
		}
		log.WithFields(f).Info(logPrefix, "skipped ", n.Reason)
		if n.Reason == SkipRecent {
			seen[key] = false // ends a streak of another reason too
			continue
		}
		ul.skips[key]++
		if ul.skips[key] > LagRuns && !ul.skipAlerted[key] {
			ul.skipAlerted[key] = true
			f["trace_id"] = "skip"
			log.WithFields(f).Error(logPrefix, "skipped (", n.Reason, ") for ", ul.skips[key], " runs in a row")
		}
	}
	for key := range ul.skips {
		if !seen[key] {
			delete(ul.skips, key)
			delete(ul.skipAlerted, key)
		}
	}
}

// trackMissing logs one Error per (rule, watch id) when a watched folder or
// doc disappears from the tree, again only after it came back and went
// missing anew; Status shows the current list.
func (s *Service) trackMissing(ul *userLoop, plan Plan) {
	seen := map[string]bool{}
	for _, m := range plan.MissingWatches {
		key := m.RuleID + "|" + m.WatchID
		seen[key] = true
		if ul.missingAlerted[key] {
			continue
		}
		ul.missingAlerted[key] = true
		what := " no longer exists"
		if m.InTrash {
			what = " is in the trash"
		}
		log.WithFields(log.Fields{
			"stage": "detect", "user": ul.uid, "rule_id": m.RuleID, "watch_id": m.WatchID, "watch_kind": m.Kind,
			"in_trash": m.InTrash,
		}).Error(logPrefix, "watched ", m.Kind, " ", m.WatchID, what)
	}
	for key := range ul.missingAlerted {
		if !seen[key] {
			delete(ul.missingAlerted, key)
		}
	}
	ul.missing.Store(append([]MissingWatch{}, plan.MissingWatches...))
}
