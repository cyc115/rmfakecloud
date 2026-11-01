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

func TestSnapshotUnreadableContentSetsPagesErr(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.Sync()
	tree, _ := storage.GetCachedTree(uid)
	snap := NewSnapshot(tree)
	if snap.PagesAttached {
		t.Fatal("PagesAttached before AttachPages")
	}
	snap.AttachPages(func(*DocSnap) ContentInfo { return ContentInfo{Error: "cannot read .content: boom"} })
	d := snap.Docs["nb"]
	if !snap.PagesAttached || d.Pages != nil || d.PagesErr == "" {
		t.Fatalf("attached=%v pages=%v err=%q", snap.PagesAttached, d.Pages, d.PagesErr)
	}
}

func TestPageOrderDeletedRegisterAndDuplicates(t *testing.T) {
	ci := ParseContent([]byte(`{"cPages":{"pages":[
	 {"id":"a","idx":{"value":"b"}},
	 {"id":"gone","idx":{"value":"c"},"deleted":{"timestamp":"1:2","value":1}},
	 {"id":"back","idx":{"value":"d"},"deleted":{"timestamp":"1:3","value":0}},
	 {"id":"a","idx":{"value":"e"}},
	 {"id":"odd","idx":{"value":"f"},"deleted":{"value":"x"}},
	 {"id":"first","idx":{"value":"a"}}]}}`))
	if want := []string{"first", "a", "back", "odd"}; !reflect.DeepEqual(ci.PageOrder, want) {
		t.Fatalf("order = %v, want %v", ci.PageOrder, want)
	}
}

func TestPlanForSnapshotHasPages(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.Sync()
	svc, _, _ := newService(t, storage)
	tree, _ := storage.GetCachedTree(uid)
	snap, _, _ := svc.planFor(uid, DefaultConfig(15), NewState(), tree)
	if !snap.PagesAttached || len(snap.Docs["nb"].Pages) != 1 {
		t.Fatalf("attached=%v pages=%v", snap.PagesAttached, snap.Docs["nb"].Pages)
	}
}

func TestSnapshotPagesFromFormatV1PagesArray(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.PDFv1("pdf", "Paper", "", []string{"p1", "p2", "p3"})
	ft.Docs["pdf"].RM = map[string][]byte{"p2": []byte("ink")}
	ft.Sync()
	snap, _ := snapshotOf(t, storage)
	d := snap.Docs["pdf"]
	if d.PagesErr != "" || len(d.Pages) != 3 {
		t.Fatalf("err=%q pages=%+v", d.PagesErr, d.Pages)
	}
	for i, want := range []string{"p1", "p2", "p3"} {
		p := d.Pages[i]
		if p.ID != want || p.Index != i || (p.ID == "p2") != (p.Hash != "") {
			t.Fatalf("page %d = %+v", i, p)
		}
	}
}

func TestFormatV1MalformedPagesIsUnreadable(t *testing.T) {
	for _, body := range []string{`{"pages":"nope"}`, `{"pages":[1,2]}`, `{"pages":{"a":1}}`} {
		if ci := ParseContent([]byte(body)); ci.Readable() {
			t.Fatalf("%s readable: %+v", body, ci)
		}
	}
	if ci := ParseContent([]byte(`{"pages":["a","","b","a"]}`)); !reflect.DeepEqual(ci.PageOrder, []string{"a", "b"}) {
		t.Fatalf("order = %v", ci.PageOrder)
	}
	if ci := ParseContent([]byte(`{"fileType":"pdf"}`)); !ci.Readable() || ci.PageOrder != nil {
		t.Fatalf("no pages = %+v", ci)
	}
}
