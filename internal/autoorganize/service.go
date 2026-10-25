package autoorganize

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// SourceID the fixed, non-device source id of the sweeper's sync notification;
// the hub skips the client whose device id equals the source, so it must not
// be a real device id
const SourceID = "auto-organize"

// Sweep triggers, shown in Runs
const (
	TriggerTimer      = "timer"
	TriggerTabletSync = "tablet sync"
	TriggerRunNow     = "run now"
)

// Errors returned to the API
var (
	ErrBusy    = errors.New("sweep running")
	ErrPaused  = errors.New("auto-organize is paused")
	ErrSync10  = errors.New("auto-organize requires sync15")
	ErrNoEntry = errors.New("not found")
)

const logPrefix = "[auto-organize] "

// Storage what the sweeper needs from the file system storage
type Storage interface {
	GetCachedTree(uid string) (*models.HashTree, error)
	BlobStorage(uid string) *fs.LocalBlobStorage
	CreateBlobFolder(uid, name, parent string) (*storage.Document, error)
	GetUsers() ([]*model.User, error)
	GetUser(uid string) (*model.User, error)
	UserDataPath(uid string) string
}

// Notifier the hub
type Notifier interface {
	NotifySync(uid, deviceID string) string
}

// Service the auto-organize sweeper: one loop per sync15 user
type Service struct {
	storage  Storage
	notifier Notifier
	store    *Store

	// Now the clock, replaceable in tests
	Now func() time.Time
	// Debounce delay between the last root write and the sweep it triggers
	Debounce time.Duration
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
	uid     string
	sweepMu sync.Mutex
	kick    chan string
	reset   chan struct{}
	running atomic.Bool
	nextAt  atomic.Int64

	debounceMu sync.Mutex
	timer      *time.Timer
}

// NewService a sweeper; call Start to run the loops
func NewService(st Storage, notifier Notifier, defaultInterval int) *Service {
	return &Service{
		storage:      st,
		notifier:     notifier,
		store:        NewStore(st.UserDataPath, defaultInterval),
		Now:          time.Now,
		Debounce:     20 * time.Second,
		UserRescan:   time.Minute,
		IntervalUnit: time.Minute,
		users:        map[string]*userLoop{},
	}
}

// Store the config/state store
func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) loop(uid string) *userLoop {
	s.mu.Lock()
	defer s.mu.Unlock()
	ul, ok := s.users[uid]
	if !ok {
		ul = &userLoop{uid: uid, kick: make(chan string, 1), reset: make(chan struct{}, 1)}
		s.users[uid] = ul
	}
	return ul
}

// Start runs the supervisor until ctx is cancelled. It returns immediately.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.wg.Add(1)
	go s.supervise(ctx)
}

// Wait for every loop to exit after the context was cancelled
func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) supervise(ctx context.Context) {
	defer s.wg.Done()
	started := map[string]bool{}
	for {
		users, err := s.storage.GetUsers()
		if err != nil {
			// do not sweep until the user list is readable
			log.Warn(logPrefix, "cannot read users: ", err)
		}
		for _, u := range users {
			if !u.Sync15 || started[u.ID] {
				continue
			}
			started[u.ID] = true
			ul := s.loop(u.ID)
			s.wg.Add(1)
			go s.run(ctx, ul)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.UserRescan):
		}
	}
}

func (s *Service) interval(uid string) time.Duration {
	cfg, err := s.store.LoadConfig(uid)
	if err != nil || !validInterval(cfg.Settings.IntervalMinutes) {
		return DefaultInterval * s.IntervalUnit
	}
	return time.Duration(cfg.Settings.IntervalMinutes) * s.IntervalUnit
}

// run the per-user loop: a root-write trigger sweeps after the debounce, the
// ticker at the user's interval is the fallback
func (s *Service) run(ctx context.Context, ul *userLoop) {
	defer s.wg.Done()
	log.Info(logPrefix, "sweeper started for ", ul.uid)
	for {
		wait := s.interval(ul.uid)
		ul.nextAt.Store(s.Now().Add(wait).UnixMilli())
		timer := time.NewTimer(wait)
		trigger := ""
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			trigger = TriggerTimer
		case trigger = <-ul.kick:
			timer.Stop()
		case <-ul.reset:
			timer.Stop()
			continue
		}
		ul.sweepMu.Lock()
		// this sweep also covers a trigger queued while waiting for the lock
		select {
		case <-ul.kick:
		default:
		}
		_, err := s.sweepLocked(ctx, ul, trigger, false)
		ul.sweepMu.Unlock()
		if err != nil && !errors.Is(err, ErrPaused) {
			log.Warn(logPrefix, ul.uid, ": ", err)
		}
	}
}

// Trigger schedules a debounced sweep after a device root write. A trigger
// that arrives during a sweep results in exactly one follow-up sweep.
func (s *Service) Trigger(uid string) {
	s.mu.Lock()
	ul, ok := s.users[uid]
	s.mu.Unlock()
	if !ok {
		// no loop: not a sync15 user, or not started (yet)
		return
	}
	ul.debounceMu.Lock()
	defer ul.debounceMu.Unlock()
	if ul.timer != nil {
		ul.timer.Stop()
	}
	ul.timer = time.AfterFunc(s.Debounce, func() {
		select {
		case ul.kick <- TriggerTabletSync:
		default:
			// a sweep is already queued
		}
	})
}

// ConfigChanged restarts the user's wait so a new interval applies at once
func (s *Service) ConfigChanged(uid string) {
	s.mu.Lock()
	ul, ok := s.users[uid]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ul.reset <- struct{}{}:
	default:
	}
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

// SweepResult a finished sweep
type SweepResult struct {
	Run  RunEntry `json:"run"`
	Plan *Plan    `json:"plan"`
}

// RunNow an explicit sweep in the configured mode; ErrBusy when one is running
func (s *Service) RunNow(uid string) (*SweepResult, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	if !ul.sweepMu.TryLock() {
		return nil, ErrBusy
	}
	defer ul.sweepMu.Unlock()
	ctx := context.Background()
	s.mu.Lock()
	if s.ctx != nil {
		ctx = s.ctx
	}
	s.mu.Unlock()
	return s.sweepLocked(ctx, ul, TriggerRunNow, true)
}

func (s *Service) planFor(uid string, cfg *Config, st *State, tree *models.HashTree) (*Snapshot, *ContentCache, *Plan) {
	snap := NewSnapshot(tree)
	st.UpdateFirstSeen(snap, s.Now())
	cache := NewContentCache(st.ContentCache, s.storage.BlobStorage(uid).GetReader)
	plan := BuildPlan(PlanInput{Snap: snap, Config: cfg, State: st, Content: cache.Get, Now: s.Now()})
	return snap, cache, plan
}

func findRule(cfg *Config, id string) (*Rule, int) {
	for i := range cfg.Rules {
		if cfg.Rules[i].ID == id {
			return &cfg.Rules[i], i + 1
		}
	}
	return nil, 0
}

// recheckFor the closure re-check for a rule's move: the no-automation tag,
// the guards and the trigger against the re-mirrored document. The inputs that
// held are written to *inputs.
func (s *Service) recheckFor(uid string, r *Rule, dest string, st *State, cache *ContentCache, inputs *map[string]string) func(t *models.HashTree, d *models.HashDoc) error {
	return func(t *models.HashTree, d *models.HashDoc) error {
		cur := NewSnapshot(t)
		snap, ok := cur.Docs[d.EntryName]
		if !ok {
			return ErrVanished
		}
		ci := cache.Get(snap)
		chk := checkDoc(r, dest, snap, &ci, st.FirstSeen[d.EntryName], cur.Locate, s.Now())
		if chk.skip != "" {
			return &NotQualifiedError{Reason: chk.detail}
		}
		if !chk.ev.match {
			return &NotQualifiedError{Reason: "trigger no longer holds (" + chk.ev.detail + ")"}
		}
		*inputs = chk.ev.inputs
		return nil
	}
}

func (s *Service) createFolder(uid string, r *Rule, cm *CreateIfMissing) (string, error) {
	parent := cm.Parent
	if parent == ParentSource {
		parent = r.Source.ID
	}
	doc, err := s.storage.CreateBlobFolder(uid, cm.Name, parent)
	if err != nil {
		return "", err
	}
	return doc.ID, nil
}

// sweepLocked one sweep (§6.1); the caller holds ul.sweepMu
func (s *Service) sweepLocked(ctx context.Context, ul *userLoop, trigger string, explicit bool) (*SweepResult, error) {
	uid := ul.uid
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	cfg, err := s.store.LoadConfig(uid)
	if err != nil {
		return nil, err
	}
	if !cfg.Settings.Enabled {
		return nil, ErrPaused
	}
	ul.running.Store(true)
	defer ul.running.Store(false)

	start := s.Now()
	st, err := s.store.LoadState(uid)
	if err != nil {
		return nil, err
	}
	live := cfg.Settings.Mode == ModeLive
	run := RunEntry{ID: uuid.NewString(), At: start.UnixMilli(), Mode: EntryDry, Trigger: trigger}
	if live {
		run.Mode = EntryLive
	}

	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		run.Errors++
		run.ErrorDetails = append(run.ErrorDetails, "cannot load tree: "+err.Error())
		s.finishRun(uid, st, &run, start, nil)
		return &SweepResult{Run: run}, err
	}
	st.PruneHistory(start)
	snap, cache, plan := s.planFor(uid, cfg, st, tree)
	for _, h := range plan.ClearHolds {
		st.ClearHold(h.RuleID, h.DocID)
	}
	run.Scanned = plan.Scanned
	run.Skipped = plan.SkippedCount()

	treeChanged := false
	created := map[string]string{}
	failedRules := map[string]bool{}
	for _, mv := range plan.Moves {
		if ctx.Err() != nil {
			run.ErrorDetails = append(run.ErrorDetails, "sweep interrupted by shutdown")
			break
		}
		entry := MoveEntry{At: s.Now().UnixMilli(), Mode: EntryDry, DocID: mv.DocID, DocName: mv.DocName,
			RuleID: mv.RuleID, RuleName: mv.RuleName, RuleNumber: mv.RuleNumber,
			From: mv.From, FromName: mv.FromName, To: mv.To, ToName: mv.ToName, Detail: mv.Detail, Kind: "rule"}
		if !live {
			if mv.CreateFolder != nil {
				entry.ToName += " (new folder)"
			}
			st.AppendMove(entry)
			run.Planned++
			continue
		}
		if failedRules[mv.RuleID] {
			continue
		}
		rule, _ := findRule(cfg, mv.RuleID)
		to := mv.To
		if to == "" && mv.CreateFolder != nil {
			if id, ok := created[mv.RuleID]; ok {
				to = id
			} else {
				id, err := s.createFolder(uid, rule, mv.CreateFolder)
				if err != nil {
					failedRules[mv.RuleID] = true
					run.Errors++
					run.ErrorDetails = append(run.ErrorDetails, fmt.Sprintf("rule %d: cannot create folder “%s”: %v", mv.RuleNumber, mv.CreateFolder.Name, err))
					continue
				}
				log.Info(logPrefix, uid, ": created folder ", mv.ToName, " ", id)
				created[mv.RuleID] = id
				st.CreatedFolders[mv.RuleID] = id
				treeChanged = true
				to = id
			}
			entry.To = to
		}
		var inputs map[string]string
		fresh, err := s.storage.GetCachedTree(uid)
		if err == nil {
			err = moveDoc(fresh, s.storage.BlobStorage(uid), mv.DocID, mv.From, to, s.recheckFor(uid, rule, to, st, cache, &inputs))
		}
		if err != nil {
			if IsNotQualified(err) {
				run.Skipped++
				run.ErrorDetails = append(run.ErrorDetails, fmt.Sprintf("%s: skipped at move time, %v", mv.DocName, err))
				continue
			}
			run.Errors++
			run.ErrorDetails = append(run.ErrorDetails, fmt.Sprintf("%s: could not update after retries: %v", mv.DocName, err))
			continue
		}
		treeChanged = true
		run.Moved++
		entry.Mode = EntryLive
		now := s.Now()
		entry.At = now.UnixMilli()
		st.AppendMove(entry)
		st.History = append(st.History, HistoryEntry{DocID: mv.DocID, RuleID: mv.RuleID, RuleNumber: mv.RuleNumber,
			From: mv.From, To: to, At: now.UnixMilli(), Fingerprint: mv.Fingerprint})
		st.SetHold(Hold{RuleID: mv.RuleID, DocID: mv.DocID, Inputs: inputs, Source: mv.From, MovedTo: to,
			CreatedAt: now.UnixMilli(), Reason: "moved"})
		log.Info(logPrefix, uid, ": moved ", mv.DocName, " ", mv.FromName, " -> ", entry.ToName, " (rule ", mv.RuleNumber, ")")
	}

	st.HeldNow = nil
	for _, sk := range plan.Skipped {
		if sk.Reason == SkipHeld {
			st.HeldNow = append(st.HeldNow, HeldNow{HoldID: sk.HoldID, RuleID: sk.RuleID, DocID: sk.DocID})
		}
	}
	keep := map[string]bool{}
	for _, d := range snap.Docs {
		if d.ContentHash != "" {
			keep[d.ContentHash] = true
		}
	}
	st.PruneContentCache(keep)
	s.finishRun(uid, st, &run, start, plan)

	if treeChanged {
		s.notifier.NotifySync(uid, SourceID)
	}
	return &SweepResult{Run: run, Plan: plan}, nil
}

func (s *Service) finishRun(uid string, st *State, run *RunEntry, start time.Time, plan *Plan) {
	run.DurationMs = s.Now().Sub(start).Milliseconds()
	st.LastSweep = run
	st.AppendRun(*run)
	if err := s.store.SaveState(uid, st); err != nil {
		log.Error(logPrefix, uid, ": cannot save state: ", err)
	}
}

// Preview a dry evaluation that changes nothing. With draft set, the draft
// replaces the rule with the same id (or is appended) and is evaluated in its
// list position, enabled.
func (s *Service) Preview(uid string, draft *Rule) (*Plan, *Config, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, nil, err
	}
	cfg, err := s.store.LoadConfig(uid)
	if err != nil {
		return nil, nil, err
	}
	if draft != nil {
		d := *draft
		d.Enabled = true
		tmp := &Config{Settings: cfg.Settings, Rules: []Rule{d}}
		tmp.Normalize()
		d = tmp.Rules[0]
		replaced := false
		rules := make([]Rule, len(cfg.Rules))
		copy(rules, cfg.Rules)
		for i := range rules {
			if rules[i].ID == d.ID {
				rules[i] = d
				replaced = true
			}
		}
		if !replaced {
			rules = append(rules, d)
		}
		cfg.Rules = rules
		draft.ID = d.ID
	}
	st, err := s.store.LoadState(uid)
	if err != nil {
		return nil, nil, err
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return nil, nil, err
	}
	_, _, plan := s.planFor(uid, cfg, st, tree)
	return plan, cfg, nil
}

// Status for the header chip
type Status struct {
	Running   bool      `json:"running"`
	LastSweep *RunEntry `json:"lastSweep,omitempty"`
	NextAt    int64     `json:"nextAt,omitempty"`
	LoopAlive bool      `json:"loopAlive"`
}

// Status of the user's sweeper
func (s *Service) Status(uid string, st *State) Status {
	s.mu.Lock()
	ul, ok := s.users[uid]
	s.mu.Unlock()
	status := Status{LastSweep: st.LastSweep}
	if ok {
		status.Running = ul.running.Load()
		status.NextAt = ul.nextAt.Load()
		status.LoopAlive = status.NextAt != 0
	}
	return status
}

// Undo reverses a LIVE sweeper move and sets a hold keyed on the document's
// current trigger inputs, so the rule does not move it again while they are
// unchanged
func (s *Service) Undo(uid, moveID string) (*MoveEntry, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	if !ul.sweepMu.TryLock() {
		return nil, ErrBusy
	}
	defer ul.sweepMu.Unlock()

	st, err := s.store.LoadState(uid)
	if err != nil {
		return nil, err
	}
	entry := st.MoveByID(moveID)
	if entry == nil || entry.Mode != EntryLive || entry.Kind == "undo" {
		return nil, ErrNoEntry
	}
	if entry.UndoneAt != 0 {
		return nil, errors.New("already undone")
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	if err := moveDoc(tree, s.storage.BlobStorage(uid), entry.DocID, entry.To, entry.From, nil); err != nil {
		return nil, err
	}
	now := s.Now()
	entry.UndoneAt = now.UnixMilli()

	cfg, err := s.store.LoadConfig(uid)
	if err == nil {
		if rule, _ := findRule(cfg, entry.RuleID); rule != nil {
			after, err := s.storage.GetCachedTree(uid)
			if err == nil {
				snap := NewSnapshot(after)
				if d, ok := snap.Docs[entry.DocID]; ok {
					cache := NewContentCache(st.ContentCache, s.storage.BlobStorage(uid).GetReader)
					ci := cache.Get(d)
					ev := evalTrigger(rule, d, &ci, st.FirstSeen[d.ID], now)
					st.SetHold(Hold{RuleID: rule.ID, DocID: d.ID, Inputs: ev.inputs, Source: entry.From,
						MovedTo: entry.To, CreatedAt: now.UnixMilli(), Reason: "undo"})
				}
			}
		}
	}
	if err := s.store.SaveState(uid, st); err != nil {
		return nil, err
	}
	s.notifier.NotifySync(uid, SourceID)
	return entry, nil
}

// Reapply performs a held move and clears the hold. The no-automation tag and
// the rule's trigger are re-checked; the cycle guard is not (an explicit ask).
func (s *Service) Reapply(uid, holdID string) (*MoveEntry, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	if !ul.sweepMu.TryLock() {
		return nil, ErrBusy
	}
	defer ul.sweepMu.Unlock()

	st, err := s.store.LoadState(uid)
	if err != nil {
		return nil, err
	}
	hold := st.HoldByID(holdID)
	if hold == nil {
		return nil, ErrNoEntry
	}
	cfg, err := s.store.LoadConfig(uid)
	if err != nil {
		return nil, err
	}
	rule, num := findRule(cfg, hold.RuleID)
	if rule == nil {
		st.ClearHold(hold.RuleID, hold.DocID)
		s.store.SaveState(uid, st)
		return nil, errors.New("the rule of this hold no longer exists")
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	snap := NewSnapshot(tree)
	d, ok := snap.Docs[hold.DocID]
	if !ok {
		return nil, ErrVanished
	}
	dest := ResolveDestination(rule, snap, st)
	if dest.problem != "" {
		return nil, errors.New(dest.problem)
	}
	to := dest.id
	if to == "" && dest.create != nil {
		if to, err = s.createFolder(uid, rule, dest.create); err != nil {
			return nil, err
		}
		st.CreatedFolders[rule.ID] = to
		if tree, err = s.storage.GetCachedTree(uid); err != nil {
			return nil, err
		}
	}
	cache := NewContentCache(st.ContentCache, s.storage.BlobStorage(uid).GetReader)
	var inputs map[string]string
	err = moveDoc(tree, s.storage.BlobStorage(uid), d.ID, rule.Source.ID, to, s.recheckFor(uid, rule, to, st, cache, &inputs))
	if err != nil {
		return nil, err
	}
	now := s.Now()
	after := NewSnapshot(tree)
	entry := MoveEntry{At: now.UnixMilli(), Mode: EntryLive, DocID: d.ID, DocName: d.Name, RuleID: rule.ID,
		RuleName: ruleLabel(rule, snap.PathName(rule.Source.ID), after.PathName(to)), RuleNumber: num,
		From: rule.Source.ID, FromName: snap.PathName(rule.Source.ID), To: to, ToName: after.PathName(to),
		Detail: "re-applied after hold", Kind: "reapply"}
	st.AppendMove(entry)
	st.History = append(st.History, HistoryEntry{DocID: d.ID, RuleID: rule.ID, RuleNumber: num, From: rule.Source.ID,
		To: to, At: now.UnixMilli(), Fingerprint: d.Fingerprint})
	st.SetHold(Hold{RuleID: rule.ID, DocID: d.ID, Inputs: inputs, Source: rule.Source.ID, MovedTo: to,
		CreatedAt: now.UnixMilli(), Reason: "moved"})
	kept := st.HeldNow[:0]
	for _, h := range st.HeldNow {
		if h.HoldID != holdID {
			kept = append(kept, h)
		}
	}
	st.HeldNow = kept
	if err := s.store.SaveState(uid, st); err != nil {
		return nil, err
	}
	s.notifier.NotifySync(uid, SourceID)
	return &st.Moves[0], nil
}
