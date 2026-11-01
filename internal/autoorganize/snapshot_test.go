package autoorganize

import (
	"reflect"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
)

func snapshotOf(t *testing.T, storage *fs.FileSystemStorage) (*Snapshot, *State) {
	t.Helper()
	tree, err := storage.GetCachedTree(uid)
	if err != nil {
		t.Fatal(err)
	}
	st := NewState()
	cache := NewContentCache(st.ContentCache, storage.BlobStorage(uid).GetReader)
	snap := NewSnapshot(tree)
	snap.AttachPages(cache.Get)
	return snap, st
}

func pageIDs(d *DocSnap) []string {
	var ids []string
	for _, p := range d.Pages {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestSnapshotPagesInCPagesOrder(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	for _, p := range []string{"p1", "p2", "p3"} {
		if err := ft.SetPage("nb", p, []byte("rm-"+p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ft.MovePageBefore("nb", "p3", "p1"); err != nil {
		t.Fatal(err)
	}
	ft.Sync()
	snap, _ := snapshotOf(t, storage)
	d := snap.Docs["nb"]
	if got := pageIDs(d); !reflect.DeepEqual(got, []string{"p3", "p1", "p2"}) {
		t.Fatalf("pages = %v, want p3 p1 p2", got)
	}
	for i, p := range d.Pages {
		if p.Index != i || p.Hash == "" {
			t.Fatalf("page %d = %+v", i, p)
		}
	}
	if d.PDFHash != "" {
		t.Fatalf("notebook PDFHash = %q", d.PDFHash)
	}
}

func TestSnapshotSkipsTocAndDeletedPages(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.SetPage("nb", "p2", []byte("b"))
	ft.DeletePage("nb", "p2")
	ft.Docs["nb"].RM["toc"] = []byte("toc")
	ft.Sync()
	snap, _ := snapshotOf(t, storage)
	if got := pageIDs(snap.Docs["nb"]); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("pages = %v, want [p1]", got)
	}
}

func TestSnapshotBlankPageHasNoHash(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.SetPage("nb", "blank", []byte("x"))
	delete(ft.Docs["nb"].RM, "blank")
	ft.Sync()
	snap, _ := snapshotOf(t, storage)
	pages := snap.Docs["nb"].Pages
	if len(pages) != 2 || pages[0].Hash == "" || pages[1].ID != "blank" || pages[1].Hash != "" {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestSnapshotPDFHash(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.PDF("pdf", "Paper", "", 3, 0, time.Time{})
	ft.Sync()
	snap, _ := snapshotOf(t, storage)
	if snap.Docs["pdf"].PDFHash == "" {
		t.Fatal("PDFHash empty")
	}
}

// a cache entry written before PageOrder existed is re-read, not trusted
func TestContentCacheReparsesOldEntries(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.Sync()
	tree, _ := storage.GetCachedTree(uid)
	var ch string
	for _, d := range tree.Docs {
		if d.EntryName == "nb" {
			ch = contentEntry(d).Hash
		}
	}
	old := map[string]ContentInfo{ch: {FileType: "notebook", PageCount: 1}} // no ParseVersion, no PageOrder
	cache := NewContentCache(old, storage.BlobStorage(uid).GetReader)
	snap := NewSnapshot(tree)
	snap.AttachPages(cache.Get)
	if got := pageIDs(snap.Docs["nb"]); !reflect.DeepEqual(got, []string{"p1"}) {
		t.Fatalf("pages = %v, want [p1]", got)
	}
	if old[ch].ParseVersion != contentParseVersion {
		t.Fatalf("cache entry not refreshed: %+v", old[ch])
	}
}
