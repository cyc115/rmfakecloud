package pagewatch

import (
	"context"
	"errors"
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

// Errors returned to callers
var (
	ErrBusy   = errors.New("run in progress")
	ErrSync10 = errors.New("page watch requires sync15")
)

// Service the page watch: one loop per sync15 user. Each run loads the
// snapshot, calls Detect, sends the events to rm-pages and saves the next
// state. The auto-organize config is read, never written; the page watch
// keeps its own state file and content cache, so the two services never share
// a file.
type Service struct {
	storage autoorganize.Storage
	cfgs    *autoorganize.Store
	sender  Sender
	store   *Store

	// Now the clock, replaceable in tests
	Now func() time.Time
	// MinAge a page blob younger than this is skipped; 0 means DefaultMinAge
	MinAge time.Duration
	// UserRescan how often the user list is re-read to start new loops
	UserRescan time.Duration
	// IntervalUnit the unit of Settings.IntervalMinutes (a minute; shorter in tests)
	IntervalUnit time.Duration

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
	started bool // guarded by Service.mu
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
		UserRescan:   time.Minute,
		IntervalUnit: time.Minute,
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
		ul = &userLoop{uid: uid, cache: map[string]autoorganize.ContentInfo{}, lag: map[string]int{}, alerted: map[string]bool{}}
		ul.lastErr.Store("")
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
		_, err := s.runLocked(ctx, ul, trigger)
		ul.runMu.Unlock()
		if err != nil && !errors.Is(err, ErrSync10) {
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
	fail := func(err error) (*RunEntry, error) {
		run.Error = err.Error()
		s.finish(ul, run)
		return run, err
	}
	cfg, err := s.cfgs.LoadConfig(uid)
	if err != nil {
		return fail(err)
	}
	dry := cfg.Settings.Mode != autoorganize.ModeLive
	if dry {
		run.Mode = ModeDry
	}
	st, err := s.store.LoadState(uid)
	if err != nil {
		return fail(err)
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return fail(err)
	}
	blobs := s.storage.BlobStorage(uid)
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(ul.cache, blobs.GetReader)
	snap.AttachPages(cc.Get)
	plan := Detect(Input{
		Snap: snap, Config: cfg, State: st, Content: cc.Get, ModTime: blobs.ModTime,
		Now: start, MinAge: s.MinAge, User: uid,
	})
	for h := range ul.cache {
		if !cc.Used()[h] {
			delete(ul.cache, h)
		}
	}
	run.Feed, run.Pipeline, run.Skipped = len(plan.Feed), len(plan.Pipeline), len(plan.Skipped)

	// dry mode sends the feed only; the pipeline events stay planned, so their
	// baselines do not move
	send := append([]Event(nil), plan.Feed...)
	if !dry {
		send = append(send, plan.Pipeline...)
	}
	accepted, sendErr := s.sender.Send(ctx, uid, send)
	if sendErr != nil {
		accepted = nil
		run.Error = "send failed: " + sendErr.Error()
		s.logSendFailure(uid, send, sendErr)
	} else {
		for _, ev := range send {
			if accepted[ev.TraceID] {
				run.Accepted++
				sendLog(ev).Info(logPrefix, "delivered ", ev.Kind)
			} else {
				sendLog(ev).Warn(logPrefix, "rm-pages did not accept ", ev.Kind)
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
		return fail(err)
	}
	s.finish(ul, run)
	return run, nil
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
	})
}

func (s *Service) logSendFailure(uid string, send []Event, err error) {
	if len(send) == 0 {
		log.WithFields(log.Fields{"stage": "deliver", "user": uid}).Error(logPrefix, "heartbeat failed: ", err)
		return
	}
	for i, ev := range send {
		if i == maxSendErrors {
			log.WithFields(log.Fields{"stage": "deliver", "user": uid}).Error(logPrefix, "send failed for ", len(send)-i, " more events: ", err)
			return
		}
		sendLog(ev).Error(logPrefix, "send failed: ", err)
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
