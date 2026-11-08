package autoorganize

import (
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// deleteRule "IF I tag a document or page “delete” anywhere THEN move it to the Trash"
func deleteRule() automation.Rule {
	return automation.Rule{
		ID: "del", Enabled: true,
		When: automation.When{Subject: automation.SubjectDocument,
			In:      []automation.Place{{ID: RootID, Kind: automation.PlaceFolder, Name: RootName}},
			Trigger: trig(TriggerTagIs, P{Tag: "delete"})},
		Actions: []automation.Action{{Type: automation.ActionTrash}},
	}
}

func setDocTag(ft *fakeTablet, id, tag string) {
	ft.Docs[id].Content["tags"] = []any{map[string]any{"name": tag, "timestamp": 1}}
}

func TestTrashValidation(t *testing.T) {
	r := deleteRule()
	if err := r.Validate(); err != nil {
		t.Fatalf("document trash rule rejected: %v", err)
	}
	withDest := deleteRule()
	withDest.Actions[0].To = &automation.Destination{FolderRef: automation.FolderRef{ID: "rr"}}
	if err := withDest.Validate(); err == nil || !strings.Contains(err.Error(), "no destination") {
		t.Errorf("trash with a destination: err %v", err)
	}
	page := deleteRule()
	page.When.Subject = automation.SubjectPage
	page.When.Trigger = trig(automation.TriggerPageTagged, P{Tag: "delete"})
	if err := page.Validate(); err == nil || !strings.Contains(err.Error(), "page rule cannot trash") {
		t.Errorf("page trash rule: err %v", err)
	}
}

// A document tagged (or with a page tagged) “delete” in any folder goes to the
// Trash; others stay. Undo puts it back and the hold keeps it there while the
// tag is unchanged.
func TestTrashRuleTrashesTaggedDocsAnywhere(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.Folder("sub", "Old", "rr")
	ft.PDF("d", "Tagged deep", "sub", 5, 1, now.Add(-time.Hour))
	ft.PDF("p", "Page tagged", "", 5, 1, now.Add(-time.Hour))
	ft.PDF("keep", "Untagged", "sub", 5, 1, now.Add(-time.Hour))
	setDocTag(ft, "d", "Delete")
	if err := ft.SetPage("p", "p1", []byte("rm-p1")); err != nil {
		t.Fatal(err)
	}
	ft.Sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, deleteRule())
	if _, err := svc.ApplyPageTags(uid, "p", "p1", []string{"delete"}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.RunNow(uid)
	if err != nil || res.Run.Moved != 2 {
		t.Fatalf("run: %+v %v", res, err)
	}
	ft.Pull()
	for id, want := range map[string]string{"d": "trash", "p": "trash", "keep": "sub"} {
		if got := ft.ParentOf(id); got != want {
			t.Errorf("%s parent %q, want %q", id, got, want)
		}
	}

	st, _ := svc.Store().LoadState(uid)
	var moveID string
	for _, m := range st.Moves {
		if m.DocID == "d" {
			moveID = m.ID
			if m.From != "sub" || m.To != "trash" || m.ToName != "Trash" {
				t.Errorf("log entry %+v", m)
			}
		}
	}
	if _, err := svc.Undo(uid, moveID); err != nil {
		t.Fatalf("undo: %v", err)
	}
	ft.Pull()
	if got := ft.ParentOf("d"); got != "sub" {
		t.Fatalf("undo left parent %q", got)
	}
	res, err = svc.RunNow(uid)
	if err != nil || res.Run.Moved != 0 {
		t.Fatalf("re-trashed after undo: %+v %v", res, err)
	}
	ft.Pull()
	if got := ft.ParentOf("d"); got != "sub" {
		t.Errorf("held doc moved: parent %q", got)
	}
}

// Restored from the Trash on the tablet: the "moved" hold keeps it out while
// the tag is unchanged.
func TestTrashRestoredOnTabletStaysRestored(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("rr", "Research Reports", "")
	ft.PDF("d", "Doc", "rr", 5, 1, now.Add(-time.Hour))
	setDocTag(ft, "d", "delete")
	ft.Sync()
	svc, _, _ := newService(t, storage)
	saveConfig(t, svc, uid, ModeLive, deleteRule())
	if res, err := svc.RunNow(uid); err != nil || res.Run.Moved != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	ft.Pull()
	ft.Docs["d"].Meta["parent"] = "rr" // restored on the tablet
	ft.Sync()
	if res, err := svc.RunNow(uid); err != nil || res.Run.Moved != 0 {
		t.Fatalf("re-trashed after restore: %+v %v", res, err)
	}
}
