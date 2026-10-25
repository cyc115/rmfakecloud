package autoorganize

import (
	"strings"
	"testing"
	"time"
)

// The move closure re-checks the destination against the re-mirrored tree: a
// tablet sync between plan and move that trashes or deletes it stops the move.
func TestMoveRecheckRefusesUnusableDestination(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(ft *fakeTablet)
		want   string
	}{
		{"destination trashed", func(ft *fakeTablet) { ft.docs["read"].meta["parent"] = "trash" }, "in the Trash"},
		{"destination parent trashed", func(ft *fakeTablet) { ft.docs["box"].meta["parent"] = "trash" }, "in the Trash"},
		{"destination deleted", func(ft *fakeTablet) { delete(ft.docs, "read") }, "destination folder is gone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ft, storage := newHarness(t, uid, true)
			now := time.Now()
			ft.folder("rr", "Research Reports", "")
			ft.folder("box", "Box", "")
			ft.folder("read", "Read", "box")
			ft.pdf("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
			ft.sync()
			svc, _, _ := newService(t, storage)
			rule := moveRule("r1", "rr", "read", Trigger{Type: TriggerFinishedReading, MinPages: 3, SettleHours: 6})
			st := NewState()

			// the sweeper read the tree, then the tablet synced a change
			stale, err := storage.GetCachedTree(uid)
			if err != nil {
				t.Fatal(err)
			}
			ft.pull()
			c.change(ft)
			ft.sync()

			cache := NewContentCache(st.ContentCache, storage.BlobStorage(uid).GetReader)
			var inputs map[string]string
			err = moveDoc(stale, storage.BlobStorage(uid), "d", "rr", "read", svc.recheckFor(uid, &rule, "read", st, cache, &inputs))
			if !IsNotQualified(err) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if p := ft.parentOf("d"); p != "rr" {
				t.Fatalf("doc moved into an unusable folder: parent %s", p)
			}
		})
	}
}

// Undo refuses when the document carries the opt-out tag, when the folder it
// would return to is gone or trashed, when it has moved since, and when a
// newer sweeper move of the same document exists.
func TestUndoRefusals(t *testing.T) {
	setup := func(t *testing.T) (*fakeTablet, *Service, string) {
		ft, storage := newHarness(t, uid, true)
		now := time.Now()
		ft.folder("box", "Box", "")
		ft.folder("rr", "Research Reports", "box")
		ft.folder("read", "Read", "rr")
		ft.folder("other", "Other", "")
		ft.pdf("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
		ft.sync()
		svc, _, _ := newService(t, storage)
		saveConfig(t, svc, uid, ModeLive, readRule())
		if res, err := svc.RunNow(uid); err != nil || res.Run.Moved != 1 {
			t.Fatalf("first move: %+v %v", res, err)
		}
		st, _ := svc.Store().LoadState(uid)
		ft.pull()
		return ft, svc, st.Moves[0].ID
	}
	for _, c := range []struct {
		name   string
		change func(ft *fakeTablet)
		want   string
	}{
		{"tagged no automation since", func(ft *fakeTablet) {
			ft.docs["d"].content["tags"] = []any{map[string]any{"name": "No-Automation", "timestamp": 1}}
		}, "no automation"},
		{"original folder trashed", func(ft *fakeTablet) { ft.docs["box"].meta["parent"] = "trash" }, "in the Trash"},
		{"original folder deleted", func(ft *fakeTablet) {
			delete(ft.docs, "rr")
			ft.docs["read"].meta["parent"] = ""
		}, "gone"},
		{"moved elsewhere since", func(ft *fakeTablet) { ft.docs["d"].meta["parent"] = "other" }, "no longer in"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ft, svc, moveID := setup(t)
			c.change(ft)
			ft.sync()
			before := ft.parentOf("d")
			_, err := svc.Undo(uid, moveID)
			if !IsNotQualified(err) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if p := ft.parentOf("d"); p != before {
				t.Fatalf("refused undo moved the doc: %s -> %s", before, p)
			}
			st, _ := svc.Store().LoadState(uid)
			if st.MoveByID(moveID).UndoneAt != 0 || st.FindHold("r1", "d").Reason != "moved" {
				t.Fatal("a refused undo must not mark the entry undone or set an undo hold")
			}
		})
	}

	t.Run("a newer move supersedes", func(t *testing.T) {
		ft, svc, firstID := setup(t)
		// undo, then re-apply: the re-apply is now the newest LIVE move of the doc
		if _, err := svc.Undo(uid, firstID); err != nil {
			t.Fatal(err)
		}
		st, _ := svc.Store().LoadState(uid)
		hold := st.FindHold("r1", "d")
		if _, err := svc.Reapply(uid, hold.ID); err != nil {
			t.Fatal(err)
		}
		st, _ = svc.Store().LoadState(uid)
		reapplyID := st.Moves[0].ID
		// a second move entry exists for the doc; the old one cannot be undone
		st.MoveByID(firstID).UndoneAt = 0
		if err := svc.Store().SaveState(uid, st); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Undo(uid, firstID); err == nil || !strings.Contains(err.Error(), "moved this document again") {
			t.Fatalf("undo of a superseded move: %v", err)
		}
		l, _ := svc.Log(uid)
		for _, m := range l.Moves {
			if m.ID == firstID && m.CanUndo {
				t.Fatal("the log offers undo on a superseded move")
			}
			if m.ID == reapplyID && !m.CanUndo {
				t.Fatal("the newest move must be undoable")
			}
		}
		if _, err := svc.Undo(uid, reapplyID); err != nil || ft.parentOf("d") != "rr" {
			t.Fatalf("undo of the newest move: %v parent %s", err, ft.parentOf("d"))
		}
	})
}

// The doc index lists the metadata entry with the size of the blob the mover wrote.
func TestMoveRecordsMetadataSize(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.folder("rr", "Research Reports", "")
	ft.folder("read", "Read", "rr")
	ft.pdf("d", "Doc", "rr", 5, 4, now.Add(-9*time.Hour))
	ft.sync()
	tree, _ := storage.GetCachedTree(uid)
	if err := moveDoc(tree, storage.BlobStorage(uid), "d", "rr", "read", nil); err != nil {
		t.Fatal(err)
	}
	d := ft.serverDoc("d")
	for _, f := range d.Files {
		if !f.IsMetadata() {
			continue
		}
		r, err := storage.BlobStorage(uid).GetReader(f.Hash)
		if err != nil {
			t.Fatal(err)
		}
		n := int64(0)
		buf := make([]byte, 512)
		for {
			k, err := r.Read(buf)
			n += int64(k)
			if err != nil {
				break
			}
		}
		r.Close()
		if f.Size != n {
			t.Fatalf("index size %d, blob %d bytes", f.Size, n)
		}
		return
	}
	t.Fatal("no metadata entry")
}
