package pagewatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// fakeSender records every call; accept picks the ids to accept (nil = all);
// failCall fails the call with that 1-based number (0 = none)
type fakeSender struct {
	mu       sync.Mutex
	calls    [][]Event
	err      error
	accept   func(Event) bool
	failCall int
}

func (f *fakeSender) Send(_ context.Context, _ string, events []Event) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]Event(nil), events...))
	if f.err != nil {
		return nil, f.err
	}
	if f.failCall == len(f.calls) {
		return nil, errors.New("chunk failed")
	}
	acc := map[string]bool{}
	for _, e := range events {
		if f.accept == nil || f.accept(e) {
			acc[e.TraceID] = true
		}
	}
	return acc, nil
}

func (f *fakeSender) last() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type svcEnv struct {
	t      *testing.T
	dir    string
	st     *fs.FileSystemStorage
	ft     *tablet.Tablet
	cfgs   *autoorganize.Store
	sender *fakeSender
	svc    *Service
	cfg    *automation.Config
}

func newSvcEnv(t *testing.T) *svcEnv {
	t.Helper()
	dir := t.TempDir()
	st := tablet.NewStorage(dir)
	if err := tablet.RegisterUser(st, uid, true); err != nil {
		t.Fatal(err)
	}
	e := &svcEnv{t: t, dir: dir, st: st, ft: tablet.New(t, st, uid), cfgs: autoorganize.NewStore(st.UserDataPath, 0), sender: &fakeSender{}}
	e.cfg = &automation.Config{Version: automation.Version, Settings: automation.Settings{Enabled: true, Mode: automation.ModeLive, IntervalMinutes: 15}}
	e.save()
	e.svc = e.newService()
	return e
}

func (e *svcEnv) newService() *Service {
	s := NewService(e.st, e.cfgs, e.sender)
	s.MinAge = time.Nanosecond
	return s
}

func (e *svcEnv) save() {
	e.t.Helper()
	cur, err := e.cfgs.LoadConfig(uid)
	if err != nil {
		e.t.Fatal(err)
	}
	e.cfg.Revision = cur.Revision
	saved, err := e.cfgs.SaveConfig(uid, e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.cfg = saved
}

func (e *svcEnv) rule(id string) {
	r := pageRule(id, folderWatch(autoorganize.RootID))
	r.Actions[0].Prompt, r.Actions[0].Model = "p", "m"
	e.cfg.Rules = append(e.cfg.Rules, r)
	e.save()
}

func (e *svcEnv) page(doc, page, fix string) {
	e.t.Helper()
	if err := e.ft.SetPage(doc, page, fixture(e.t, fix)); err != nil {
		e.t.Fatal(err)
	}
	if err := e.ft.Sync(); err != nil {
		e.t.Fatal(err)
	}
}

// timerRun one run as the loop's timer makes it (MinAge applies)
func (e *svcEnv) timerRun() *RunEntry {
	e.t.Helper()
	ul := e.svc.loop(uid)
	ul.runMu.Lock()
	defer ul.runMu.Unlock()
	r, err := e.svc.runLocked(context.Background(), ul, TriggerTimer)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *svcEnv) run() *RunEntry {
	e.t.Helper()
	r, err := e.svc.RunNow(uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func TestRunNowSendsAndAdvances(t *testing.T) {
	e := newSvcEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")

	r := e.run() // first run: baselines only
	if r.Feed != 0 || e.sender.count() != 1 || len(e.sender.last()) != 0 {
		t.Fatalf("first run %+v calls=%d", r, e.sender.count())
	}
	e.page("nb", "p1", "strokes_ab.rm")
	r = e.run()
	evs := e.sender.last()
	if len(evs) != 1 || evs[0].Kind != KindEdit || evs[0].PageID != "p1" || evs[0].User != uid {
		t.Fatalf("events %+v", evs)
	}
	if r.Feed != 1 || r.Accepted != 1 || r.Error != "" || r.Mode != "live" || r.Trigger != TriggerRunNow {
		t.Fatalf("run %+v", r)
	}
	e.run()
	if len(e.sender.last()) != 0 {
		t.Fatalf("resent %+v", e.sender.last())
	}
	runs, err := e.svc.Store().LoadRuns(uid)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs %d %v", len(runs), err)
	}
	if runs[0].Feed != 0 || runs[1].Feed != 1 {
		t.Fatalf("runs not newest first: %+v", runs)
	}
	if _, err := os.Stat(filepath.Join(e.st.UserDataPath(uid), "page-watch.state.json")); err != nil {
		t.Fatal(err)
	}
}

func TestFailedSendDoesNotAdvanceAndResendsNextRun(t *testing.T) {
	e := newSvcEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.run()
	e.page("nb", "p1", "strokes_ab.rm")

	e.sender.err = errors.New("rm-pages down")
	r := e.run()
	if r.Error == "" || r.Accepted != 0 {
		t.Fatalf("run %+v", r)
	}
	st, err := e.svc.Store().LoadState(uid)
	if err != nil || st.Feed["nb/p1"] == "" {
		t.Fatalf("state %+v %v", st, err)
	}
	first := e.sender.last()

	e.sender.err = nil
	r = e.run()
	again := e.sender.last()
	if len(first) != 1 || len(again) != 1 || again[0].PageID != "p1" || again[0].AfterHash != first[0].AfterHash || again[0].BeforeHash != first[0].BeforeHash {
		t.Fatalf("first %+v again %+v", first, again)
	}
	if r.Accepted != 1 || r.Error != "" {
		t.Fatalf("run %+v", r)
	}
	e.run()
	if len(e.sender.last()) != 0 {
		t.Fatalf("resent after accept: %+v", e.sender.last())
	}
}

func TestFailedSendStillPersistsAlwaysEffects(t *testing.T) {
	e := newSvcEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sender.err = errors.New("rm-pages down")
	e.run()
	st, err := e.svc.Store().LoadState(uid)
	if err != nil || !st.FeedInitialised || len(st.Seen) == 0 {
		t.Fatalf("always-effects not saved: %+v %v", st, err)
	}
}

func TestDryModeSendsFeedOnly(t *testing.T) {
	e := newSvcEnv(t)
	e.cfg.Settings.Mode = automation.ModeDry
	e.rule("r1")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.run()
	e.page("nb", "p1", "strokes_ab.rm")

	r := e.run()
	for _, ev := range e.sender.last() {
		if ev.Watched {
			t.Fatalf("pipeline event sent in dry mode: %+v", ev)
		}
	}
	if r.Mode != "dry" || r.Pipeline == 0 || r.Feed != 1 {
		t.Fatalf("run %+v", r)
	}
	st, _ := e.svc.Store().LoadState(uid)
	if rs := st.Rules["r1"]; rs != nil && (len(rs.Baselines) > 0 || len(rs.Members) > 0) {
		t.Fatalf("dry mode moved the pipeline baseline: %+v", rs)
	}
	if v, _ := e.svc.Status(uid); v.PendingPipeline == 0 {
		t.Fatalf("status %+v", v)
	}

	// going live sends what dry mode held back
	e.cfg.Settings.Mode = automation.ModeLive
	e.save()
	e.run()
	var pipe int
	for _, ev := range e.sender.last() {
		if ev.Watched {
			pipe++
		}
	}
	if pipe == 0 {
		t.Fatalf("live run sent no pipeline events: %+v", e.sender.last())
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	e := newSvcEnv(t)
	e.rule("r1")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.run()
	e.page("nb", "p1", "strokes_ab.rm")
	e.run()
	e.run()

	restarted := e.newService()
	before := e.sender.count()
	if _, err := restarted.RunNow(uid); err != nil {
		t.Fatal(err)
	}
	if e.sender.count() != before+1 || len(e.sender.last()) != 0 {
		t.Fatalf("restart re-emitted %+v", e.sender.last())
	}
}

func lagErrors(h *logtest.Hook) []*log.Entry {
	var out []*log.Entry
	for _, en := range h.AllEntries() {
		if en.Level == log.ErrorLevel && en.Data["stage"] == "detect" {
			out = append(out, en)
		}
	}
	return out
}

func TestLaggingBaselineLogsErrorAfterTwoRuns(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })

	e := newSvcEnv(t)
	e.rule("r1")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sender.accept = func(Event) bool { return false }

	e.run()
	e.run()
	if n := len(lagErrors(hook)); n != 0 {
		t.Fatalf("%d lag errors after 2 runs", n)
	}
	e.run()
	got := lagErrors(hook)
	if len(got) != 1 {
		t.Fatalf("%d lag errors after 3 runs", len(got))
	}
	d := got[0].Data
	if d["doc_id"] != "nb" || d["page_id"] != "p1" || d["rule_id"] != "r1" || d["trace_id"] == "" || d["trace_id"] == nil {
		t.Fatalf("fields %v", d)
	}
	e.run()
	e.run()
	if n := len(lagErrors(hook)); n != 1 {
		t.Fatalf("%d lag errors after 5 runs, want 1 per streak", n)
	}

	// accepted again: the streak ends, a new one can alert
	e.sender.accept = nil
	e.run()
	e.sender.accept = func(Event) bool { return false }
	e.page("nb", "p1", "strokes_ab.rm")
	e.run()
	e.run()
	e.run()
	if n := len(lagErrors(hook)); n != 2 {
		t.Fatalf("%d lag errors after second streak, want 2", n)
	}
}

func TestSendFailureLogsDeliverErrorWithTags(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.run()
	e.page("nb", "p1", "strokes_ab.rm")
	e.sender.err = errors.New("boom")
	e.run()
	var found bool
	for _, en := range hook.AllEntries() {
		if en.Level == log.ErrorLevel && en.Data["stage"] == "deliver" && en.Data["doc_id"] == "nb" && en.Data["page_id"] == "p1" && en.Data["trace_id"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("no deliver error: %v", hook.AllEntries())
	}
}

func TestMinAgeOverride(t *testing.T) {
	e := newSvcEnv(t)
	e.svc.MinAge = time.Hour // blobs just written are younger
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.timerRun()
	e.page("nb", "p1", "strokes_ab.rm")
	r := e.timerRun()
	if r.Feed != 0 || r.Skipped == 0 {
		t.Fatalf("recent page not skipped: %+v", r)
	}
	e.svc.MinAge = time.Nanosecond
	r = e.timerRun()
	if r.Feed != 1 {
		t.Fatalf("not sent once old enough: %+v", r)
	}
}

// Run now is pressed right after writing: it ignores MinAge, the timer does not
func TestRunNowIgnoresMinAge(t *testing.T) {
	e := newSvcEnv(t)
	e.svc.MinAge = time.Hour
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.timerRun() // baseline
	e.page("nb", "p1", "strokes_ab.rm")
	if r := e.timerRun(); r.Feed != 0 || r.Skipped == 0 {
		t.Fatalf("timer run sent a page younger than MinAge: %+v", r)
	}
	r := e.run()
	if r.Feed != 1 || r.Trigger != TriggerRunNow {
		t.Fatalf("run now did not send the fresh page: %+v", r)
	}
}

func TestStartRunsLoopAndSendsHeartbeat(t *testing.T) {
	e := newSvcEnv(t)
	e.svc.UserRescan = 5 * time.Millisecond
	e.svc.IntervalUnit = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for e.sender.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	e.svc.Wait()
	if e.sender.count() < 2 {
		t.Fatalf("loop sent %d times", e.sender.count())
	}
}

func TestRunNowBusyWhileRunHoldsLock(t *testing.T) {
	e := newSvcEnv(t)
	ul := e.svc.loop(uid)
	ul.runMu.Lock()
	if _, err := e.svc.RunNow(uid); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	ul.runMu.Unlock()
	if _, err := e.svc.RunNow(uid); err != nil {
		t.Fatal(err)
	}
}

func TestLocalFailureLogsDetectError(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	if err := os.WriteFile(filepath.Join(e.st.UserDataPath(uid), StateFileName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RunNow(uid); err == nil {
		t.Fatal("no error for a broken state file")
	}
	if n := len(lagErrors(hook)); n != 1 {
		t.Fatalf("%d detect errors, want 1", n)
	}
}

func TestHeartbeatFailureCarriesHeartbeatTraceID(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.sender.err = errors.New("down")
	e.run()
	for _, en := range hook.AllEntries() {
		if en.Level == log.ErrorLevel && en.Data["stage"] == "deliver" && en.Data["trace_id"] == "heartbeat" {
			return
		}
	}
	t.Fatalf("no heartbeat error: %v", hook.AllEntries())
}

func TestShutdownCancelledSendLogsWarnNotError(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.svc.logSendFailure(ctx, uid, []Event{{TraceID: "t"}}, errors.New("context canceled"))
	for _, en := range hook.AllEntries() {
		if en.Level == log.ErrorLevel {
			t.Fatalf("Error logged on shutdown: %v", en.Message)
		}
	}
	if len(hook.AllEntries()) != 1 {
		t.Fatalf("entries %v", hook.AllEntries())
	}
}

func synthEvents(n int) []Event {
	out := make([]Event, n)
	for i := range out {
		out[i] = Event{TraceID: fmt.Sprintf("t%03d", i), Kind: KindEdit, DocID: "d", PageID: fmt.Sprintf("p%d", i)}
	}
	return out
}

func TestDeliverSendsInChunksOf200(t *testing.T) {
	e := newSvcEnv(t)
	evs := synthEvents(450)
	acc, err := e.svc.deliver(context.Background(), uid, evs)
	if err != nil || len(acc) != 450 {
		t.Fatalf("accepted %d err %v", len(acc), err)
	}
	if e.sender.count() != 3 {
		t.Fatalf("%d POSTs, want 3", e.sender.count())
	}
	for i, want := range []int{200, 200, 50} {
		if got := len(e.sender.calls[i]); got != want {
			t.Fatalf("chunk %d has %d events, want %d", i+1, got, want)
		}
	}
}

func TestDeliverFailedChunkLosesOnlyItsIDs(t *testing.T) {
	e := newSvcEnv(t)
	e.sender.failCall = 2
	evs := synthEvents(450)
	acc, err := e.svc.deliver(context.Background(), uid, evs)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 chunks") {
		t.Fatalf("err = %v", err)
	}
	for i, ev := range evs {
		if want := i < 200 || i >= 400; acc[ev.TraceID] != want {
			t.Fatalf("event %d accepted=%v, want %v", i, acc[ev.TraceID], want)
		}
	}
}

func TestDeliverChunksByBytes(t *testing.T) {
	e := newSvcEnv(t)
	big := strings.Repeat("x", 1<<20) // ~1 MiB of prompt per event
	evs := synthEvents(5)
	for i := range evs {
		evs[i].Prompt = big
	}
	if _, err := e.svc.deliver(context.Background(), uid, evs); err != nil {
		t.Fatal(err)
	}
	if e.sender.count() != 5 {
		t.Fatalf("%d POSTs, want 5 (one ~1 MiB event each under 2 MiB)", e.sender.count())
	}
	for _, c := range e.sender.calls {
		b, _ := json.Marshal(c)
		if len(b) > DefaultChunkBytes+1024 {
			t.Fatalf("chunk of %d bytes", len(b))
		}
	}
}

func TestDeliverNoEventsIsOneHeartbeat(t *testing.T) {
	e := newSvcEnv(t)
	if _, err := e.svc.deliver(context.Background(), uid, nil); err != nil {
		t.Fatal(err)
	}
	if e.sender.count() != 1 || len(e.sender.last()) != 0 {
		t.Fatalf("calls %v", e.sender.calls)
	}
}

func TestRunAdvancesChunksThatWereAccepted(t *testing.T) {
	e := newSvcEnv(t)
	e.svc.ChunkEvents = 2
	e.ft.Notebook("nb", "Notes", "")
	for i := 1; i <= 6; i++ {
		e.page("nb", fmt.Sprintf("p%d", i), "strokes_a.rm")
	}
	e.run()
	for i := 1; i <= 6; i++ {
		e.page("nb", fmt.Sprintf("p%d", i), "strokes_ab.rm")
	}
	e.sender.calls = nil
	e.sender.failCall = 2
	r := e.run()
	if e.sender.count() != 3 || r.Accepted != 4 || r.Error == "" {
		t.Fatalf("run %+v calls %d", r, e.sender.count())
	}
	lost := map[string]bool{}
	for _, ev := range e.sender.calls[1] {
		lost[ev.PageID] = true
	}
	st, err := e.svc.Store().LoadState(uid)
	if err != nil {
		t.Fatal(err)
	}
	snapNew := e.sender.calls[0][0].AfterHash
	for i := 1; i <= 6; i++ {
		page := fmt.Sprintf("p%d", i)
		if moved := st.Feed["nb/"+page] == snapNew; moved == lost[page] {
			t.Fatalf("%s baseline moved=%v, lost=%v", page, moved, lost[page])
		}
	}
	// next run re-sends only the lost chunk
	e.sender.calls = nil
	e.sender.failCall = 0
	e.run()
	if got := len(e.sender.calls[0]); got != 2 || !lost[e.sender.calls[0][0].PageID] {
		t.Fatalf("resent %+v", e.sender.calls)
	}
}

func skipErrors(h *logtest.Hook) []*log.Entry {
	var out []*log.Entry
	for _, en := range h.AllEntries() {
		if en.Level == log.ErrorLevel && en.Data["trace_id"] == "skip" {
			out = append(out, en)
		}
	}
	return out
}

func TestPersistentSkipLogsOneErrorPerStreak(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	broken := func(b bool) {
		e.ft.Docs["nb"].RawContent = nil
		if b {
			e.ft.Docs["nb"].RawContent = []byte("{not json")
		}
		if err := e.ft.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	broken(true)
	e.run()
	e.run()
	if n := len(skipErrors(hook)); n != 0 {
		t.Fatalf("%d skip errors after 2 runs", n)
	}
	var infos int
	for _, en := range hook.AllEntries() {
		if en.Level == log.InfoLevel && en.Data["doc_id"] == "nb" && en.Data["reason"] == SkipContent && en.Data["stage"] == "detect" {
			infos++
		}
	}
	if infos != 2 {
		t.Fatalf("%d skip infos, want one per run", infos)
	}
	e.run()
	got := skipErrors(hook)
	if len(got) != 1 || got[0].Data["doc_id"] != "nb" || got[0].Data["stage"] != "detect" || got[0].Data["page_id"] != "" {
		t.Fatalf("skip errors %v", got)
	}
	e.run()
	e.run()
	if n := len(skipErrors(hook)); n != 1 {
		t.Fatalf("%d skip errors after 5 runs, want 1 per streak", n)
	}
	broken(false) // the streak ends
	e.run()
	broken(true)
	e.run()
	e.run()
	e.run()
	if n := len(skipErrors(hook)); n != 2 {
		t.Fatalf("%d skip errors after the second streak, want 2", n)
	}
}

func TestSkipErrorCarriesRuleAndPage(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.rule("r1")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	// the page blob's mtime cannot be read: never treated as old
	hash, _, err := models.Hash(bytes.NewReader(fixture(t, "strokes_a.rm")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.st.UserDataPath(uid), "sync", hash)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.run()
	}
	got := skipErrors(hook)
	if len(got) != 1 {
		t.Fatalf("skip errors %v", got)
	}
	if d := got[0].Data; d["page_id"] != "p1" || d["rule_id"] != "r1" || d["reason"] != SkipMtime {
		t.Fatalf("fields %v", d)
	}
}

func TestRecentSkipNeverAlerts(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.svc.MinAge = time.Hour // continuous writing: every blob stays recent
	e.rule("r1")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	for i := 0; i < 5; i++ {
		e.timerRun() // the timer keeps MinAge; Run now does not
	}
	if n := len(skipErrors(hook)); n != 0 {
		t.Fatalf("%d skip errors for recent pages", n)
	}
	var infos int
	for _, en := range hook.AllEntries() {
		if en.Level == log.InfoLevel && en.Data["reason"] == SkipRecent && en.Data["page_id"] == "p1" {
			infos++
		}
	}
	if infos != 5 {
		t.Fatalf("%d recent infos, want 5", infos)
	}
}

func TestMissingWatchLogsOncePerStreakAndShowsInStatus(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	e.ft.Folder("w", "Watched", "")
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	r := pageRule("r1", folderWatch("w"))
	r.Actions[0].Prompt, r.Actions[0].Model = "p", "m"
	e.cfg.Rules = append(e.cfg.Rules, r)
	e.save()
	missingErrs := func() []*log.Entry {
		var out []*log.Entry
		for _, en := range hook.AllEntries() {
			if en.Level == log.ErrorLevel && en.Data["watch_id"] == "w" {
				out = append(out, en)
			}
		}
		return out
	}
	e.run()
	if v, _ := e.svc.Status(uid); len(v.MissingWatches) != 0 || len(missingErrs()) != 0 {
		t.Fatalf("present folder reported missing: %+v", v.MissingWatches)
	}
	delete(e.ft.Docs, "w")
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	e.run()
	e.run()
	got := missingErrs()
	if len(got) != 1 || got[0].Data["rule_id"] != "r1" || got[0].Data["stage"] != "detect" {
		t.Fatalf("missing errors %v", got)
	}
	v, _ := e.svc.Status(uid)
	if len(v.MissingWatches) != 1 || v.MissingWatches[0].RuleID != "r1" || v.MissingWatches[0].WatchID != "w" {
		t.Fatalf("status %+v", v.MissingWatches)
	}
	e.ft.Folder("w", "Watched", "") // restored: the streak ends
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	e.run()
	if v, _ := e.svc.Status(uid); len(v.MissingWatches) != 0 {
		t.Fatalf("status after restore %+v", v.MissingWatches)
	}
	delete(e.ft.Docs, "w")
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	e.run()
	if n := len(missingErrs()); n != 2 {
		t.Fatalf("%d missing errors after the second streak, want 2", n)
	}
}

func TestEmptyTreeSkipsRunWithError(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	e := newSvcEnv(t)
	for i := 0; i <= EmptyTreeGuard; i++ {
		id := fmt.Sprintf("nb%02d", i)
		e.ft.Notebook(id, id, "")
		e.page(id, "p1", "strokes_a.rm")
	}
	e.run()
	statePath := filepath.Join(e.st.UserDataPath(uid), StateFileName)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for id := range e.ft.Docs {
		delete(e.ft.Docs, id)
	}
	if err := e.ft.Sync(); err != nil {
		t.Fatal(err)
	}
	calls := e.sender.count()
	r, err := e.svc.RunNow(uid)
	if err == nil || r == nil || r.Error == "" {
		t.Fatalf("run %+v err %v", r, err)
	}
	if e.sender.count() != calls {
		t.Fatal("an untrusted snapshot was sent")
	}
	after, _ := os.ReadFile(statePath)
	if string(after) != string(before) {
		t.Fatal("state saved on an empty tree")
	}
	if n := len(lagErrors(hook)); n != 1 {
		t.Fatalf("%d detect errors, want 1", n)
	}
}

func TestServiceDefaultMinAge(t *testing.T) {
	e := newSvcEnv(t)
	if s := NewService(e.st, e.cfgs, e.sender); s.MinAge != DefaultMinAge {
		t.Fatalf("MinAge = %v", s.MinAge)
	}
}
