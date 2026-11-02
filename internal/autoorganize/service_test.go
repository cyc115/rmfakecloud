package autoorganize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

const uid = "mike"

func TestMoveChangesOnlyParentAndVersion(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.PDF("d", "Q3 <Market> & Outlook", "rr", 14, 13, now.Add(-9*time.Hour))
	ft.Sync()
	before := ft.ServerMetaRaw("d")

	tree, err := storage.GetCachedTree(uid)
	if err != nil {
		t.Fatal(err)
	}
	if err := moveDoc(tree, storage.BlobStorage(uid), "d", "rr", "read", nil); err != nil {
		t.Fatal(err)
	}
	after := ft.ServerMetaRaw("d")
	if after["parent"] != "read" {
		t.Fatalf("parent = %v", after["parent"])
	}
	if after["version"].(float64) != before["version"].(float64)+1 {
		t.Fatalf("version %v -> %v", before["version"], after["version"])
	}
	for k, v := range before {
		if k == "parent" || k == "version" {
			continue
		}
		if after[k] != v {
			t.Errorf("field %s changed: %v -> %v", k, v, after[k])
		}
	}
	if after["lastOpenedTool"] != "fineliner" {
		t.Error("unknown metadata field dropped by the move")
	}
	if len(after) != len(before) {
		t.Errorf("key count %d -> %d", len(before), len(after))
	}
	// the cached tree agrees with the blobs
	cached, _ := storage.GetCachedTree(uid)
	d, _ := cached.FindDoc("d")
	if d.Parent != "read" || d.DocumentName != "Q3 <Market> & Outlook" || d.Version != 2 {
		t.Errorf("cached doc: %+v", d.MetadataFile)
	}
}

func TestMoveRecheckRunsAgainAfterGenerationConflict(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.PDF("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.Sync()

	svc, _, _ := newService(t, storage)
	rule := readRule()
	rule.When.Trigger.SettleHours = 6
	st := NewState()

	// the sweeper's tree is now stale: the tablet re-opens the document and syncs
	stale, err := storage.GetCachedTree(uid)
	if err != nil {
		t.Fatal(err)
	}
	ft.Pull()
	ft.Docs["d"].Meta["lastOpened"] = ms(now.Add(-time.Minute))
	ft.Sync()

	calls := 0
	cache := NewContentCache(st.ContentCache, storage.BlobStorage(uid).GetReader)
	var inputs map[string]string
	inner := svc.recheckFor(uid, &rule, "read", st, cache, &inputs)
	err = moveDoc(stale, storage.BlobStorage(uid), "d", "rr", "read", func(tr *models.HashTree, d *models.HashDoc) error {
		calls++
		return inner(tr, d)
	})
	var nq *NotQualifiedError
	if !errors.As(err, &nq) || !strings.Contains(nq.Reason, "trigger no longer holds") {
		t.Fatalf("err = %v, want no longer qualifies", err)
	}
	if calls != 2 {
		t.Fatalf("closure ran %d times; the retry after Mirror must re-check", calls)
	}
	if p := ft.ParentOf("d"); p != "rr" {
		t.Fatalf("doc moved despite refusal: parent %s", p)
	}
}

func TestMoveRecheckRefusals(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.Folder("other", "Other", "")
	ft.PDF("tagged", "Tagged later", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.PDF("moved", "Moved by tablet", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.PDF("gone", "Deleted by tablet", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.Sync()
	svc, _, _ := newService(t, storage)
	rule := readRule()
	st := NewState()
	cache := NewContentCache(st.ContentCache, storage.BlobStorage(uid).GetReader)

	ft.Pull()
	ft.Docs["tagged"].Content["tags"] = []any{map[string]any{"name": "No Automation", "timestamp": 1}}
	ft.Docs["moved"].Meta["parent"] = "other"
	delete(ft.Docs, "gone")
	ft.Sync()

	var inputs map[string]string
	for id, check := range map[string]func(error) bool{
		"tagged": func(err error) bool { return err != nil && strings.Contains(err.Error(), "no automation") },
		"moved":  func(err error) bool { return err != nil && strings.Contains(err.Error(), "no longer in the source") },
		"gone":   func(err error) bool { return errors.Is(err, ErrVanished) },
	} {
		tree, _ := storage.GetCachedTree(uid)
		err := moveDoc(tree, storage.BlobStorage(uid), id, "rr", "read", svc.recheckFor(uid, &rule, "read", st, cache, &inputs))
		if !check(err) || !IsNotQualified(err) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if ft.ParentOf("tagged") != "rr" || ft.ParentOf("moved") != "other" {
		t.Error("a refused move changed the tree")
	}
}

// The seed rule end to end: preview, live move into a created Read folder,
// the move sticks after a rebuild from blobs and after the tablet syncs.
func TestSeedRuleEndToEnd(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.PDF("q3", "Q3 Market Outlook", "rr", 14, 13, now.Add(-9*time.Hour))
	ft.PDF("half", "Half read", "rr", 14, 6, now.Add(-9*time.Hour))
	ft.PDF("peek", "Last page peek", "rr", 8, 7, now.Add(-1*time.Hour))
	ft.PDF("upload", "Web upload", "rr", 0, 0, time.Time{})
	pinned := ft.PDF("fed", "Fed Minutes", "rr", 5, 4, now.Add(-9*time.Hour))
	pinned.Meta["pinned"] = true
	tagged := ft.PDF("staff", "Staff Guide", "rr", 5, 4, now.Add(-9*time.Hour))
	tagged.Content["tags"] = []any{"No Automation"}
	ft.Notebook("nb", "Sketchbook", "rr")
	ft.Sync()

	svc, notifier, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeDry, seedRule())

	plan, _, err := svc.Preview(uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].DocID != "q3" || plan.Moves[0].ToName != "Research Reports / Read" {
		t.Fatalf("preview moves: %+v", plan.Moves)
	}
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		reasons[s.DocID] = s.Reason
	}
	want := map[string]string{"upload": SkipPageCountUnknown, "fed": SkipPinned, "staff": SkipNoAutomation, "nb": SkipOutOfScope}
	for id, r := range want {
		if reasons[id] != r {
			t.Errorf("preview skip %s = %q, want %q", id, reasons[id], r)
		}
	}

	// dry run: logged as DRY, nothing changes, no folder created
	res, err := svc.RunNow(uid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Mode != EntryDry || res.Run.Planned != 1 || res.Run.Moved != 0 {
		t.Fatalf("dry run: %+v", res.Run)
	}
	if ft.ParentOf("q3") != "rr" || notifier.count() != 0 {
		t.Fatal("dry run changed the tree or notified")
	}
	st, _ := svc.Store().LoadState(uid)
	if len(st.Moves) != 1 || st.Moves[0].Mode != EntryDry || len(st.Holds) != 0 || len(st.History) != 0 || len(st.CreatedFolders) != 0 {
		t.Fatalf("dry state: %+v", st)
	}

	// live
	saveConfig(t, svc, uid, ModeLive, seedRule())
	res, err = svc.RunNow(uid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Mode != EntryLive || res.Run.Moved != 1 || res.Run.Errors != 0 {
		t.Fatalf("live run: %+v", res.Run)
	}
	st, _ = svc.Store().LoadState(uid)
	readID := st.CreatedFolders["seed"]
	if readID == "" {
		t.Fatal("created folder UUID not recorded in the state file")
	}
	cfgBytes, _ := os.ReadFile(filepath.Join(storage.UserDataPath(uid), ConfigFileName))
	if strings.Contains(string(cfgBytes), readID) {
		t.Fatal("the sweeper wrote into the UI config")
	}
	if notifier.count() != 1 || notifier.calls[0] != uid+"|"+SourceID {
		t.Fatalf("notify: %v", notifier.calls)
	}
	readFolder := ft.ServerDoc(readID)
	if readFolder == nil || readFolder.DocumentName != "Read" || readFolder.Parent != "rr" {
		t.Fatalf("Read folder: %+v", readFolder)
	}
	if p := ft.ParentOf("q3"); p != readID {
		t.Fatalf("q3 parent %s, want %s (rebuilt from blobs)", p, readID)
	}
	if d := ft.ServerDoc("q3"); d.DocumentName != "Q3 Market Outlook" {
		t.Fatalf("name changed: %q", d.DocumentName)
	}
	for _, id := range []string{"half", "peek", "upload", "fed", "staff", "nb"} {
		if ft.ParentOf(id) != "rr" {
			t.Errorf("%s moved", id)
		}
	}
	if h := st.FindHold("seed", "q3"); h == nil || h.Inputs["pageCount"] != "14" || h.Inputs["lastOpenedPage"] != "13" {
		t.Fatalf("hold not recorded: %+v", st.Holds)
	}
	if len(st.Moves) != 2 || st.Moves[0].Mode != EntryLive || st.Moves[0].To != readID {
		t.Fatalf("moves log: %+v", st.Moves)
	}

	// the tablet syncs (pulls the move, uploads its own state): the move sticks
	ft.Pull()
	ft.Sync()
	if p := ft.ParentOf("q3"); p != readID {
		t.Fatalf("after tablet sync q3 parent %s", p)
	}
	// later sweeps change nothing and do not create a second folder
	res, err = svc.RunNow(uid)
	if err != nil || res.Run.Moved != 0 {
		t.Fatalf("second live run: %+v %v", res.Run, err)
	}
	tree, _ := storage.GetCachedTree(uid)
	folders := 0
	for _, d := range tree.Docs {
		if d.DocumentName == "Read" {
			folders++
		}
	}
	if folders != 1 {
		t.Fatalf("%d Read folders", folders)
	}
	if notifier.count() != 1 {
		t.Fatal("a sweep that moved nothing must not notify")
	}
	// a fresh service (restart) sees the same state
	svc2, _, _ := newService(t, storage)
	plan, _, _ = svc2.Preview(uid, nil)
	if len(plan.Moves) != 0 || plan.Rules[0].DestID != readID {
		t.Fatalf("after restart: %+v %+v", plan.Moves, plan.Rules[0])
	}
}

// Every v1 trigger moves a qualifying document and leaves the others, with a
// live sweep against real blobs.
func TestEveryTriggerMovesThroughTheService(t *testing.T) {
	now := time.Now()
	type tc struct {
		name    string
		trigger automation.Trigger
		conds   []automation.Condition
		setup   func(ft *fakeTablet)
		move    []string
		stay    map[string]string // doc -> expected skip reason ("" = simply no match)
	}
	day := 24 * time.Hour
	cases := []tc{
		{"finished_reading", trig(TriggerFinishedReading, P{MinPages: 3, SettleHours: 6}), nil, func(ft *fakeTablet) {
			ft.PDF("yes", "Done", "src", 3, 2, now.Add(-7*time.Hour))
			ft.PDF("no", "Short", "src", 2, 1, now.Add(-7*time.Hour))
			ft.PDF("zero", "Zero", "src", 0, 0, now.Add(-7*time.Hour))
		}, []string{"yes"}, map[string]string{"no": "", "zero": SkipPageCountUnknown}},
		{"read_progress", trig(TriggerReadProgress, P{Percent: 50, SettleHours: 0}), nil, func(ft *fakeTablet) {
			ft.PDF("yes", "Half", "src", 10, 4, now.Add(-time.Minute))
			ft.PDF("no", "Third", "src", 10, 2, now.Add(-time.Minute))
		}, []string{"yes"}, map[string]string{"no": ""}},
		{"not_opened_for_days", trig(TriggerNotOpened, P{Days: 30}), []automation.Condition{cond(automation.CondMinAgeDays, P{Days: 7})}, func(ft *fakeTablet) {
			ft.PDF("yes", "Old", "src", 3, 0, now.Add(-40*day))
			ft.PDF("no", "Recent", "src", 3, 0, now.Add(-2*day))
			nb := ft.Notebook("nbyes", "Unopened notebook", "src")
			nb.Meta["createdTime"] = ms(now.Add(-60 * day))
		}, []string{"yes", "nbyes"}, map[string]string{"no": ""}},
		{"not_modified_for_days", trig(TriggerNotModified, P{Days: 90}), nil, func(ft *fakeTablet) {
			d := ft.PDF("yes", "Stale", "src", 3, 0, time.Time{})
			d.Meta["lastModified"] = ms(now.Add(-100 * day))
			d = ft.PDF("no", "Fresh", "src", 3, 0, time.Time{})
			d.Meta["lastModified"] = ms(now.Add(-1 * day))
		}, []string{"yes"}, map[string]string{"no": ""}},
		{"name_matches", trig(TriggerNameMatches, P{Pattern: "WSJ *"}), nil, func(ft *fakeTablet) {
			ft.PDF("yes", "WSJ Weekend", "src", 0, 0, time.Time{})
			ft.PDF("no", "FT Weekend", "src", 0, 0, time.Time{})
		}, []string{"yes"}, map[string]string{"no": ""}},
		{"tag_is", trig(TriggerTagIs, P{Tag: "done"}), nil, func(ft *fakeTablet) {
			d := ft.PDF("yes", "Tagged", "src", 3, 0, time.Time{})
			d.Content["tags"] = []any{map[string]any{"name": "Done", "timestamp": 1}}
			d = ft.PDF("no", "Other tag", "src", 3, 0, time.Time{})
			d.Content["tags"] = []any{"later"}
		}, []string{"yes"}, map[string]string{"no": ""}},
		{"age_since_created", trig(TriggerAgeSinceCreated, P{Days: 14}), nil, func(ft *fakeTablet) {
			d := ft.PDF("yes", "Arrived long ago", "src", 3, 0, time.Time{})
			d.Meta["createdTime"] = ms(now.Add(-20 * day))
			d = ft.PDF("no", "Arrived yesterday", "src", 3, 0, time.Time{})
			d.Meta["createdTime"] = ms(now.Add(-1 * day))
		}, []string{"yes"}, map[string]string{"no": ""}},
		{"pinned_is", trig(TriggerPinnedIs, P{Pinned: true}), nil, func(ft *fakeTablet) {
			d := ft.PDF("yes", "Pinned", "src", 3, 0, time.Time{})
			d.Meta["pinned"] = true
			ft.PDF("no", "Not pinned", "src", 3, 0, time.Time{})
		}, []string{"yes"}, map[string]string{"no": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ft, storage := newHarness(t, uid, true)
			ft.Folder("src", "Source", "")
			ft.Folder("dst", "Destination", "")
			c.setup(ft)
			// every trigger also honours the opt-out tag
			opt := ft.PDF("optout", "WSJ Opted out", "src", 3, 2, now.Add(-50*day))
			opt.Meta["pinned"] = true
			opt.Meta["createdTime"] = ms(now.Add(-200 * day))
			opt.Meta["lastModified"] = ms(now.Add(-200 * day))
			opt.Content["tags"] = []any{"done", map[string]any{"name": "no_automation"}}
			ft.Sync()

			svc, _, _ := newService(t, storage)
			saveConfig(t, svc, uid, ModeLive, moveRule("r", "src", "dst", c.trigger, c.conds...))
			res, err := svc.RunNow(uid)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range c.move {
				if p := ft.ParentOf(id); p != "dst" {
					t.Errorf("%s not moved (parent %s); skipped %+v", id, p, res.Plan.Skipped)
				}
			}
			for id, reason := range c.stay {
				if p := ft.ParentOf(id); p != "src" {
					t.Errorf("%s moved", id)
				}
				got := ""
				for _, s := range res.Plan.Skipped {
					if s.DocID == id {
						got = s.Reason
					}
				}
				if got != reason {
					t.Errorf("%s skip reason %q, want %q", id, got, reason)
				}
			}
			if ft.ParentOf("optout") != "src" {
				t.Error("no-automation document moved")
			}
			if res.Run.Moved != len(c.move) {
				t.Errorf("moved %d, want %d", res.Run.Moved, len(c.move))
			}
		})
	}
}

// §6.2 worked example: Rule A Inbox + not opened 30 d -> Archive,
// Rule B Archive + pinned -> Inbox. At most one cycle.
func TestWorkedExampleAtMostOneCycle(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	svc, _, clk := newService(t, storage)
	now := clk.Now()
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("archive", "Archive", "")
	ft.PDF("D", "Quarterly Letter", "inbox", 4, 0, now.Add(-40*24*time.Hour))
	ft.Sync()
	ruleA := moveRule("A", "inbox", "archive", trig(TriggerNotOpened, P{Days: 30}), cond(automation.CondMinAgeDays, P{Days: 7}))
	ruleB := moveRule("B", "archive", "inbox", trig(TriggerPinnedIs, P{Pinned: true}))
	cfg := saveConfig(t, svc, uid, ModeLive, ruleA, ruleB)
	if w := LoopWarnings(cfg, func(r *automation.Rule) (string, bool) { return r.Move().To.ID, true }); len(w) != 2 {
		t.Fatalf("save-time loop warning: %v", w)
	}

	// sweep 1: A moves D to Archive; B does not see it (snapshot)
	res, _ := svc.RunNow(uid)
	if res.Run.Moved != 1 || ft.ParentOf("D") != "archive" {
		t.Fatalf("sweep 1: %+v parent %s", res.Run, ft.ParentOf("D"))
	}

	// Mike pins D on the tablet; it syncs
	clk.advance(time.Hour)
	ft.Pull()
	ft.Docs["D"].Meta["pinned"] = true
	ft.Sync()

	// sweep 2: B wants D back in Inbox; refused by the cycle guard
	res, _ = svc.RunNow(uid)
	if res.Run.Moved != 0 || ft.ParentOf("D") != "archive" {
		t.Fatalf("sweep 2 must not move: %+v", res.Run)
	}
	var guard *Skip
	for i := range res.Plan.Skipped {
		if res.Plan.Skipped[i].DocID == "D" {
			guard = &res.Plan.Skipped[i]
		}
	}
	if guard == nil || guard.Reason != SkipCycleGuard || !strings.Contains(guard.Detail, "Inbox") || !strings.Contains(guard.Detail, "rule 1") {
		t.Fatalf("cycle guard skip: %+v", guard)
	}
	// sweep 3 inside 24 h: still refused
	clk.advance(10 * time.Hour)
	if res, _ = svc.RunNow(uid); res.Run.Moved != 0 {
		t.Fatal("sweep 3 within 24 h must not move")
	}

	// after 24 h, B may move it back
	clk.advance(14 * time.Hour)
	if res, _ = svc.RunNow(uid); res.Run.Moved != 1 || ft.ParentOf("D") != "inbox" {
		t.Fatalf("after 24 h: %+v", res.Run)
	}
	// Mike unpins it: A wants it in Archive again, refused for the symmetric reason
	clk.advance(time.Hour)
	ft.Pull()
	ft.Docs["D"].Meta["pinned"] = false
	ft.Sync()
	res, _ = svc.RunNow(uid)
	if res.Run.Moved != 0 || ft.ParentOf("D") != "inbox" {
		t.Fatalf("symmetric guard: %+v", res.Run)
	}
	// D is back in rule A's source with the inputs A moved it on, so A's
	// move-back hold (§6.4) refuses it; that outlasts the 24 h cycle window
	if s := skipOf(res.Plan, "D"); s == nil || s.Reason != SkipHeld || s.RuleID != "A" {
		t.Fatalf("symmetric refusal: %+v", s)
	}
	clk.advance(30 * time.Hour)
	if res, _ = svc.RunNow(uid); res.Run.Moved != 0 || ft.ParentOf("D") != "inbox" {
		t.Fatalf("after the cycle window the hold still applies: %+v", res.Run)
	}
	st, _ := svc.Store().LoadState(uid)
	moves := 0
	for _, m := range st.Moves {
		if m.Mode == EntryLive {
			moves++
		}
	}
	if moves != 2 {
		t.Fatalf("%d live moves in 26 h, want 2 (one round trip)", moves)
	}
}

func TestHoldAfterTabletMovesBackThenReapply(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	svc, _, clk := newService(t, storage)
	now := clk.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.PDF("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.Sync()
	saveConfig(t, svc, uid, ModeLive, readRule())

	if res, _ := svc.RunNow(uid); res.Run.Moved != 1 {
		t.Fatal("first move")
	}
	// the tablet's copy wins: the doc returns to the source, inputs unchanged,
	// and its version moves on (a version keyed hold would never match)
	ft.Pull()
	ft.Docs["d"].Meta["parent"] = "rr"
	ft.Docs["d"].Meta["version"] = 9
	ft.Sync()

	res, _ := svc.RunNow(uid)
	if res.Run.Moved != 0 || ft.ParentOf("d") != "rr" {
		t.Fatalf("held doc moved: %+v", res.Run)
	}
	var held *Skip
	for i := range res.Plan.Skipped {
		if res.Plan.Skipped[i].Reason == SkipHeld {
			held = &res.Plan.Skipped[i]
		}
	}
	if held == nil || held.HoldID == "" || !strings.Contains(held.Detail, "returned to source after move") {
		t.Fatalf("held skip: %+v", res.Plan.Skipped)
	}
	st, _ := svc.Store().LoadState(uid)
	if len(st.HeldNow) != 1 || st.HeldNow[0].HoldID != held.HoldID {
		t.Fatalf("held now: %+v", st.HeldNow)
	}

	// one-click re-apply
	entry, err := svc.Reapply(uid, held.HoldID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "reapply" || ft.ParentOf("d") != "read" {
		t.Fatalf("reapply: %+v parent %s", entry, ft.ParentOf("d"))
	}
	st, _ = svc.Store().LoadState(uid)
	if st.HoldByID(held.HoldID) != nil || len(st.HeldNow) != 0 {
		t.Fatal("the hold must be cleared by re-apply")
	}

	// moved back again, but re-opened: inputs change, the hold clears itself
	clk.advance(7 * time.Hour)
	ft.Pull()
	ft.Docs["d"].Meta["parent"] = "rr"
	ft.Docs["d"].Meta["lastOpened"] = ms(clk.Now().Add(-6*time.Hour - time.Minute))
	ft.Sync()
	if res, _ := svc.RunNow(uid); res.Run.Moved != 1 || ft.ParentOf("d") != "read" {
		t.Fatalf("changed inputs must move again: %+v %+v", res.Run, res.Plan.Skipped)
	}
}

func TestUndoMovesBackAndSetsHold(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	svc, notifier, _ := newService(t, storage)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	ft.PDF("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.Sync()
	saveConfig(t, svc, uid, ModeLive, readRule())
	svc.RunNow(uid)
	st, _ := svc.Store().LoadState(uid)
	moveID := st.Moves[0].ID

	if _, err := svc.Undo(uid, moveID); err != nil {
		t.Fatal(err)
	}
	if ft.ParentOf("d") != "rr" {
		t.Fatal("undo did not move the doc back")
	}
	st, _ = svc.Store().LoadState(uid)
	if st.Moves[0].UndoneAt == 0 {
		t.Fatal("entry not marked undone")
	}
	if h := st.FindHold("r1", "d"); h == nil || h.Reason != "undo" {
		t.Fatalf("undo must set a hold: %+v", st.Holds)
	}
	if _, err := svc.Undo(uid, moveID); err == nil {
		t.Fatal("double undo must fail")
	}
	res, _ := svc.RunNow(uid)
	if res.Run.Moved != 0 || ft.ParentOf("d") != "rr" {
		t.Fatal("the rule re-moved an undone document")
	}
	if notifier.count() != 2 {
		t.Fatalf("notify after move and undo: %d", notifier.count())
	}
}

// TestUndoHoldUsesRuleInputs Undo keys its hold on the rule's merged inputs
// (trigger + the min-age condition), so the next plan holds the document
// instead of clearing the hold and moving it again
func TestUndoHoldUsesRuleInputs(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	svc, _, clk := newService(t, storage)
	now := clk.Now()
	day := 24 * time.Hour
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("arch", "Archive", "")
	d := ft.PDF("d", "Old", "inbox", 3, 0, now.Add(-40*day))
	d.Meta["createdTime"] = ms(now.Add(-50 * day))
	ft.Sync()
	saveConfig(t, svc, uid, ModeLive, moveRule("r", "inbox", "arch", trig(TriggerNotOpened, P{Days: 30}), cond(automation.CondMinAgeDays, P{Days: 7})))

	plan, _, err := svc.Preview(uid, nil)
	if err != nil || len(plan.Moves) != 1 {
		t.Fatalf("preview: %+v %v", plan, err)
	}
	planned := plan.Moves[0].Inputs
	if _, ok := planned["ageAnchor"]; !ok {
		t.Fatalf("planner inputs lack ageAnchor: %v", planned)
	}
	if res, _ := svc.RunNow(uid); res.Run.Moved != 1 || ft.ParentOf("d") != "arch" {
		t.Fatal("live move")
	}
	st, _ := svc.Store().LoadState(uid)
	if _, err := svc.Undo(uid, st.Moves[0].ID); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Store().LoadState(uid)
	h := st.FindHold("r", "d")
	if h == nil || h.Reason != "undo" || !reflect.DeepEqual(h.Inputs, planned) {
		t.Fatalf("hold %+v, want inputs %v", h, planned)
	}
	plan, _, err = svc.Preview(uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, s := range plan.Skipped {
		held = held || (s.DocID == "d" && s.Reason == SkipHeld)
	}
	if !held || len(plan.Moves) != 0 || len(plan.ClearHolds) != 0 {
		t.Fatalf("next plan: moves %+v skipped %+v clear %+v", plan.Moves, plan.Skipped, plan.ClearHolds)
	}
}

func TestPausedAndSync10(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Folder("rr", "Research Reports", "")
	ft.Sync()
	svc, _, _ := newService(t, storage)
	cfg := saveConfig(t, svc, uid, ModeLive, readRule())
	cfg.Settings.Enabled = false
	if _, err := svc.Store().SaveConfig(uid, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunNow(uid); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused run now: %v", err)
	}
	if _, _, err := svc.Preview(uid, nil); err != nil {
		t.Fatalf("preview works while paused: %v", err)
	}

	_, storage10 := newHarness(t, "old", false)
	svc10, _, _ := newService(t, storage10)
	if _, err := svc10.RunNow("old"); !errors.Is(err, ErrSync10) {
		t.Fatalf("sync10 run now: %v", err)
	}
	if _, _, err := svc10.Preview("old", nil); !errors.Is(err, ErrSync10) {
		t.Fatalf("sync10 preview: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc10.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	svc10.mu.Lock()
	ul, started := svc10.users["old"]
	svc10.mu.Unlock()
	started = started && ul.nextAt.Load() != 0
	cancel()
	svc10.Wait()
	if started {
		t.Fatal("a loop was started for a sync10 user")
	}
}

func TestDebouncedTriggerSingleFlightAndLifecycle(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("news", "News", "")
	ft.Sync()
	svc, _, _ := newService(t, storage)
	svc.Debounce = 30 * time.Millisecond
	svc.IntervalUnit = time.Hour // the fallback ticker stays out of the way
	saveConfig(t, svc, uid, ModeDry, moveRule("r", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "*"})))
	runs := func() int {
		st, _ := svc.Store().LoadState(uid)
		return len(st.Runs)
	}
	waitFor := func(n int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for runs() < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if runs() != n {
			t.Fatalf("runs = %d, want %d", runs(), n)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		ul := svc.users[uid]
		svc.mu.Unlock()
		if ul != nil && ul.nextAt.Load() != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// a burst of root writes: one sweep after the debounce
	for i := 0; i < 5; i++ {
		svc.Trigger(uid)
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(1)
	time.Sleep(100 * time.Millisecond)
	if runs() != 1 {
		t.Fatalf("burst produced %d sweeps", runs())
	}
	st, _ := svc.Store().LoadState(uid)
	if st.Runs[0].Trigger != TriggerTabletSync {
		t.Fatalf("trigger label %q", st.Runs[0].Trigger)
	}

	// triggers arriving while a sweep holds the lock: exactly one follow-up
	ul := svc.loop(uid)
	ul.sweepMu.Lock()
	if _, err := svc.RunNow(uid); !errors.Is(err, ErrBusy) {
		t.Fatalf("run now while busy: %v", err)
	}
	for i := 0; i < 3; i++ {
		svc.Trigger(uid)
		time.Sleep(60 * time.Millisecond)
	}
	ul.sweepMu.Unlock()
	waitFor(2)
	time.Sleep(150 * time.Millisecond)
	if runs() != 2 {
		t.Fatalf("triggers during a sweep produced %d follow-ups, want 1", runs()-1)
	}

	// Stop: cancelling the context ends every goroutine
	cancel()
	done := make(chan struct{})
	go func() { svc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loops did not stop on cancel")
	}
}

func TestFallbackTicker(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("news", "News", "")
	ft.Sync()
	svc, _, _ := newService(t, storage)
	svc.IntervalUnit = 4 * time.Millisecond // 5 "minutes" = 20 ms
	cfg := saveConfig(t, svc, uid, ModeDry, moveRule("r", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "*"})))
	cfg.Settings.IntervalMinutes = 5
	svc.Store().SaveConfig(uid, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.Wait() }()
	svc.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := svc.Store().LoadState(uid)
		if len(st.Runs) >= 2 {
			if st.Runs[0].Trigger != TriggerTimer {
				t.Fatalf("trigger %q", st.Runs[0].Trigger)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the fallback ticker did not sweep")
}

func TestPreviewDraftRuleInPosition(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("news", "News", "")
	ft.Folder("archive", "Archive", "")
	ft.PDF("wsj", "WSJ Weekend", "inbox", 3, 0, time.Time{})
	ft.PDF("ft", "FT Weekend", "inbox", 3, 0, time.Time{})
	ft.Sync()
	svc, _, _ := newService(t, storage)
	first := moveRule("first", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "WSJ *"}))
	saveConfig(t, svc, uid, ModeDry, first)

	draft := moveRule("", "inbox", "archive", trig(TriggerNameMatches, P{Pattern: "*Weekend"}))
	draft.Enabled = false
	plan, _, err := svc.Preview(uid, &draft)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID == "" {
		t.Fatal("draft got no id")
	}
	matches := 0
	for _, m := range plan.Moves {
		if m.RuleID == draft.ID {
			matches++
			if m.DocID != "ft" {
				t.Errorf("draft claimed %s, which rule 1 claims first", m.DocID)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("draft matches %d, want 1 (evaluated after rule 1)", matches)
	}
	if st, _ := svc.Store().LoadState(uid); len(st.Runs) != 0 || len(st.Moves) != 0 {
		t.Fatal("preview must not write the state")
	}
}

func TestCorruptContentAndMissingContent(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Folder("inbox", "Inbox", "")
	ft.Folder("news", "News", "")
	d := ft.PDF("corrupt", "WSJ corrupt", "inbox", 3, 0, time.Time{})
	d.RawContent = []byte(`{"fileType":`)
	d = ft.PDF("nocontent", "WSJ no content", "inbox", 3, 0, time.Time{})
	d.NoContent = true
	ft.PDF("ok", "WSJ ok", "inbox", 3, 0, time.Time{})
	ft.Sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, moveRule("r", "inbox", "news", trig(TriggerNameMatches, P{Pattern: "WSJ *"})))
	res, err := svc.RunNow(uid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Moved != 1 || ft.ParentOf("ok") != "news" || ft.ParentOf("corrupt") != "inbox" || ft.ParentOf("nocontent") != "inbox" {
		t.Fatalf("run %+v skipped %+v", res.Run, res.Plan.Skipped)
	}
	for _, s := range res.Plan.Skipped {
		if s.Reason != SkipContentUnreadable {
			t.Errorf("%s: %s", s.DocID, s.Reason)
		}
	}
}

// A user the supervisor has not picked up yet (created after Start) gets a
// sweep from their first root write, without waiting for the user rescan.
// A sync10 user's root write starts nothing.
func TestTriggerStartsLoopForNewUser(t *testing.T) {
	ft, storage := newHarness(t, "early", true)
	svc, _, _ := newService(t, storage)
	svc.Debounce = 20 * time.Millisecond
	svc.UserRescan = time.Hour // the supervisor will not rescan during the test
	svc.IntervalUnit = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); svc.Wait() }()
	svc.Start(ctx)
	_ = ft

	// registered after the supervisor's only scan
	late, err := model.NewUser("late", "pw")
	if err != nil {
		t.Fatal(err)
	}
	late.ID, late.Sync15 = "late", true
	if err := storage.RegisterUser(late); err != nil {
		t.Fatal(err)
	}
	old, _ := model.NewUser("old", "pw")
	old.ID, old.Sync15 = "old", false
	if err := storage.RegisterUser(old); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	svc.Trigger("late")
	svc.Trigger("old")
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, _ := svc.Store().LoadState("late")
		if len(st.Runs) == 1 {
			if st.Runs[0].Trigger != TriggerTabletSync {
				t.Fatalf("trigger %q", st.Runs[0].Trigger)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first root write of a new user did not sweep")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond)
	if st, _ := svc.Store().LoadState("old"); len(st.Runs) != 0 {
		t.Fatal("a sync10 user's root write swept")
	}
	svc.mu.Lock()
	ul := svc.users["old"]
	svc.mu.Unlock()
	if ul != nil && ul.started {
		t.Fatal("a loop was started for a sync10 user")
	}
}

// TestUndoWhileNotSettledKeepsHold (ruling a′): finished_reading AND tag_is
// keep moves a doc; Mike opens it (not settled any more) and taps Undo. The
// hold must carry the condition inputs too, so once the doc settles again the
// hold still applies: no ClearHolds, no second move.
func TestUndoWhileNotSettledKeepsHold(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	svc, _, clk := newService(t, storage)
	now := clk.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("read", "Read", "rr")
	d := ft.PDF("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
	d.Content["tags"] = []any{"keep"}
	ft.Sync()
	r := readRule()
	r.Conditions = append(r.Conditions, cond(automation.CondTagIs, P{Tag: "keep"}))
	saveConfig(t, svc, uid, ModeLive, r)
	if res, err := svc.RunNow(uid); err != nil || res.Run.Moved != 1 {
		t.Fatalf("first run: %+v %v", res, err)
	}

	clk.advance(time.Minute)
	// the tablet syncs the move, then Mike opens the doc in Read
	if err := ft.Pull(); err != nil {
		t.Fatal(err)
	}
	if err := ft.Open("d", 4, clk.Now()); err != nil {
		t.Fatal(err)
	}
	ft.Sync()
	st, _ := svc.Store().LoadState(uid)
	if _, err := svc.Undo(uid, st.Moves[0].ID); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Store().LoadState(uid)
	h := st.FindHold("r1", "d")
	if h == nil || h.Reason != "undo" || h.Inputs["tags"] != "keep" {
		t.Fatalf("undo hold = %+v, want the tag_is input", h)
	}

	clk.advance(7 * time.Hour)
	plan, _, err := svc.Preview(uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rr := plan.Rules[0]; rr.Held != 1 || rr.Matches != 0 || len(plan.ClearHolds) != 0 {
		t.Fatalf("settled again: rule %+v, clear %+v", rr, plan.ClearHolds)
	}
	if res, _ := svc.RunNow(uid); res.Run.Moved != 0 || ft.ParentOf("d") != "rr" {
		t.Fatal("the undone document moved again")
	}
}
