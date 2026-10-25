package autoorganize

import (
	"fmt"
	"testing"
	"time"
)

// Repeated dry sweeps keep one DRY entry per would-be move instead of filling
// the 50-entry log, and LIVE entries (which carry Undo) outlive DRY ones.
func TestMovesLogKeepsLiveEntriesOverDry(t *testing.T) {
	st := NewState()
	for i := 0; i < 10; i++ {
		st.AppendMove(MoveEntry{Mode: EntryLive, RuleID: "r", DocID: fmt.Sprintf("live%d", i), From: "a", To: "b", At: int64(i)})
	}
	for sweep := 0; sweep < 200; sweep++ {
		st.AppendMove(MoveEntry{Mode: EntryDry, RuleID: "r", DocID: "pending", From: "a", To: "b", At: int64(100 + sweep)})
	}
	dry := 0
	for _, m := range st.Moves {
		if m.Mode == EntryDry {
			dry++
			if m.At != 299 {
				t.Errorf("the DRY entry is not the newest one: at %d", m.At)
			}
		}
	}
	if dry != 1 || len(st.Moves) != 11 || st.Moves[0].Mode != EntryDry {
		t.Fatalf("after 200 dry sweeps: %d DRY of %d entries", dry, len(st.Moves))
	}
	// a different would-be move is a separate entry
	st.AppendMove(MoveEntry{Mode: EntryDry, RuleID: "r", DocID: "pending", From: "a", To: "c"})
	if len(st.Moves) != 12 {
		t.Fatalf("distinct DRY moves merged: %d entries", len(st.Moves))
	}

	// many distinct DRY moves overflow the log: DRY entries go first
	for i := 0; i < 100; i++ {
		st.AppendMove(MoveEntry{Mode: EntryDry, RuleID: "r", DocID: fmt.Sprintf("dry%d", i), From: "a", To: "b"})
	}
	live := 0
	for _, m := range st.Moves {
		if m.Mode == EntryLive {
			live++
		}
	}
	if len(st.Moves) != logLimit || live != 10 {
		t.Fatalf("overflow: %d entries, %d LIVE (want %d, 10)", len(st.Moves), live, logLimit)
	}
	// LIVE entries alone still roll over oldest first
	for i := 0; i < 60; i++ {
		st.AppendMove(MoveEntry{Mode: EntryLive, RuleID: "r", DocID: fmt.Sprintf("new%d", i)})
	}
	if len(st.Moves) != logLimit || st.Moves[0].DocID != "new59" || st.Moves[logLimit-1].DocID != "new10" {
		t.Fatalf("live roll over: %d entries, first %s last %s", len(st.Moves), st.Moves[0].DocID, st.Moves[logLimit-1].DocID)
	}
}

// A dry sweeper running every interval logs each pending move once.
func TestDrySweepsDoNotFloodTheLog(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.folder("inbox", "Inbox", "")
	ft.folder("news", "News", "")
	ft.pdf("w", "WSJ Weekend", "inbox", 3, 0, time.Time{})
	ft.sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, moveRule("r", "inbox", "news", Trigger{Type: TriggerNameMatches, Pattern: "WSJ *"}))
	ft.pdf("w2", "WSJ Monday", "inbox", 3, 0, time.Time{})
	ft.sync()
	if res, _ := svc.RunNow(uid); res.Run.Moved != 2 {
		t.Fatal("live move")
	}
	ft.pull()
	ft.pdf("w3", "WSJ Tuesday", "inbox", 3, 0, time.Time{})
	ft.sync()
	saveConfig(t, svc, uid, ModeDry, moveRule("r", "inbox", "news", Trigger{Type: TriggerNameMatches, Pattern: "WSJ *"}))
	for i := 0; i < 80; i++ {
		if _, err := svc.RunNow(uid); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := svc.Store().LoadState(uid)
	live, dry := 0, 0
	for _, m := range st.Moves {
		if m.Mode == EntryLive {
			live++
		} else {
			dry++
		}
	}
	if live != 2 || dry != 1 || len(st.Runs) != logLimit {
		t.Fatalf("after 80 dry sweeps: %d LIVE, %d DRY, %d runs", live, dry, len(st.Runs))
	}
	l, _ := svc.Log(uid)
	undoable := 0
	for _, m := range l.Moves {
		if m.CanUndo {
			undoable++
		}
	}
	if undoable != 2 {
		t.Fatalf("live moves lost their undo: %d undoable", undoable)
	}
}

// The rule row's "N held" chip counts documents back in the source and held
// now, not the hold every sweeper move records while the doc sits in the
// destination.
func TestViewHeldCountsDocsHeldNow(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.folder("rr", "Research Reports", "")
	ft.folder("read", "Read", "rr")
	ft.pdf("a", "A", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.pdf("b", "B", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, readRule())
	if res, _ := svc.RunNow(uid); res.Run.Moved != 2 {
		t.Fatalf("moved %d", res.Run.Moved)
	}
	view, err := svc.View(uid)
	if err != nil {
		t.Fatal(err)
	}
	if view.Rules[0].Holds != 0 {
		t.Fatalf("held chip after a plain move: %d, want 0", view.Rules[0].Holds)
	}
	// the tablet's copy wins for one doc: it is back in the source, held
	ft.pull()
	ft.docs["a"].meta["parent"] = "rr"
	ft.sync()
	view, _ = svc.View(uid)
	if view.Rules[0].Holds != 1 || view.Rules[0].Matches != 0 {
		t.Fatalf("held chip %d matches %d, want 1 and 0", view.Rules[0].Holds, view.Rules[0].Matches)
	}
}

// Holds and created-folder ids of deleted rules, and holds of documents gone
// from the tree, are dropped at the next sweep.
func TestSweepPrunesStaleHolds(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.folder("rr", "Research Reports", "")
	ft.pdf("a", "A", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.pdf("b", "B", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, seedRule())
	if res, _ := svc.RunNow(uid); res.Run.Moved != 2 {
		t.Fatalf("moved %d", res.Run.Moved)
	}
	st, _ := svc.Store().LoadState(uid)
	if len(st.Holds) != 2 || st.CreatedFolders["seed"] == "" {
		t.Fatalf("state after move: %+v", st)
	}
	// doc b is deleted on the tablet
	ft.pull()
	delete(ft.docs, "b")
	ft.sync()
	svc.RunNow(uid)
	st, _ = svc.Store().LoadState(uid)
	if len(st.Holds) != 1 || st.Holds[0].DocID != "a" {
		t.Fatalf("hold of a deleted doc kept: %+v", st.Holds)
	}
	// the rule is deleted
	saveConfig(t, svc, uid, ModeLive, moveRule("other", "rr", "rr2", Trigger{Type: TriggerPinnedIs, Pinned: true}))
	svc.RunNow(uid)
	st, _ = svc.Store().LoadState(uid)
	if len(st.Holds) != 0 || len(st.CreatedFolders) != 0 {
		t.Fatalf("state of a deleted rule kept: holds %+v created %+v", st.Holds, st.CreatedFolders)
	}
}
