package tablet_test

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

func setup(t *testing.T) *tablet.Tablet {
	t.Helper()
	st := tablet.NewStorage(t.TempDir())
	if err := tablet.RegisterUser(st, "u", true); err != nil {
		t.Fatal(err)
	}
	return tablet.New(t, st, "u")
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pageOrder live page ids of the server .content, sorted by idx.value
func pageOrder(t *testing.T, ft *tablet.Tablet, doc string) []string {
	t.Helper()
	cp, _ := ft.ServerContent(doc)["cPages"].(map[string]any)
	type pg struct{ id, idx string }
	var ps []pg
	for _, it := range cp["pages"].([]any) {
		m := it.(map[string]any)
		if _, del := m["deleted"]; del {
			continue
		}
		ps = append(ps, pg{m["id"].(string), m["idx"].(map[string]any)["value"].(string)})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].idx < ps[j].idx })
	var ids []string
	for _, p := range ps {
		ids = append(ids, p.id)
	}
	return ids
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSetPageWritesRmEntryAndCPages(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	rm := fixture(t, "strokes_a.rm")
	if err := ft.SetPage("nb", "p1", rm); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	got := ft.ServerBlob("nb", "nb/p1.rm")
	if string(got) != string(rm) {
		t.Fatalf("rm blob mismatch: %d vs %d bytes", len(got), len(rm))
	}
	if order := pageOrder(t, ft, "nb"); !eq(order, []string{"p1"}) {
		t.Fatalf("cPages = %v", order)
	}
}

func TestAppendedPagesSortInOrder(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	var want []string
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10"} {
		if err := ft.SetPage("nb", id, []byte(id)); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := pageOrder(t, ft, "nb"); !eq(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestDeletePageRemovesEntry(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	if err := ft.SetPage("nb", "p1", fixture(t, "strokes_a.rm")); err != nil {
		t.Fatal(err)
	}
	if err := ft.SetPage("nb", "p2", fixture(t, "strokes_ab.rm")); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := ft.DeletePage("nb", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if ft.ServerBlob("nb", "nb/p1.rm") != nil {
		t.Fatal("deleted page still has an .rm entry")
	}
	if ft.ServerBlob("nb", "nb/p2.rm") == nil {
		t.Fatal("other page lost its .rm entry")
	}
	items := ft.ServerContent("nb")["cPages"].(map[string]any)["pages"].([]any)
	var marked bool
	for _, it := range items {
		m := it.(map[string]any)
		if m["id"] == "p1" {
			_, marked = m["deleted"]
		}
	}
	if !marked {
		t.Fatal("p1 not marked deleted in cPages")
	}
	if got := pageOrder(t, ft, "nb"); !eq(got, []string{"p2"}) {
		t.Fatalf("live pages = %v", got)
	}
}

func TestMovePageBeforeReorders(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	for _, id := range []string{"p1", "p2", "p3"} {
		if err := ft.SetPage("nb", id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := pageOrder(t, ft, "nb"); !eq(got, []string{"p1", "p2", "p3"}) {
		t.Fatalf("before = %v", got)
	}
	cases := []struct {
		page, before string
		want         []string
	}{
		{"p3", "p1", []string{"p3", "p1", "p2"}},
		{"p2", "p1", []string{"p3", "p2", "p1"}},
		{"p3", "p1", []string{"p2", "p3", "p1"}},
		{"p1", "p2", []string{"p1", "p2", "p3"}},
	}
	for _, c := range cases {
		if err := ft.MovePageBefore("nb", c.page, c.before); err != nil {
			t.Fatal(err)
		}
		if err := ft.Sync(); err != nil {
			t.Fatal(err)
		}
		if got := pageOrder(t, ft, "nb"); !eq(got, c.want) {
			t.Fatalf("move %s before %s: got %v, want %v", c.page, c.before, got, c.want)
		}
	}
}

func TestMovePageBeforeToFrontManyTimes(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	for _, id := range []string{"p1", "p2", "p3"} {
		if err := ft.SetPage("nb", id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	// alternate which page goes to the front so the first idx keeps shrinking
	front, other := "p1", "p3"
	for i := 0; i < 30; i++ {
		if err := ft.MovePageBefore("nb", other, front); err != nil {
			t.Fatal(err)
		}
		front, other = other, front
		if err := ft.Sync(); err != nil {
			t.Fatal(err)
		}
		got := pageOrder(t, ft, "nb")
		if len(got) != 3 || got[0] != front {
			t.Fatalf("move %d: order = %v, want %s first", i, got, front)
		}
	}
}

func TestBetween(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", ""}, {"i", ""}, {"", "i"}, {"", "0i"}, {"", "1"}, {"", "00i"},
		{"a", "b"}, {"a", "a1"}, {"az", "b"}, {"0i", "1"}, {"09", "0i"},
	}
	for _, c := range cases {
		got := tablet.Between(c.a, c.b)
		if !(c.a < got) || (c.b != "" && !(got < c.b)) {
			t.Errorf("Between(%q, %q) = %q, not strictly between", c.a, c.b, got)
		}
	}
	if got := tablet.Between("", "0"); got != "0" { // no key sorts below "0"
		t.Errorf(`Between("", "0") = %q`, got)
	}
}

func TestOpenSetsLastOpened(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := ft.Open("nb", 4, at); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	m := ft.ServerMetaRaw("nb")
	if m["lastOpened"] != tablet.Ms(at) {
		t.Fatalf("lastOpened = %v, want %s", m["lastOpened"], tablet.Ms(at))
	}
	if m["lastOpenedPage"] != float64(4) {
		t.Fatalf("lastOpenedPage = %v", m["lastOpenedPage"])
	}
}

func TestNilTestReturnsErrors(t *testing.T) {
	st := tablet.NewStorage(t.TempDir())
	if err := tablet.RegisterUser(st, "u", true); err != nil {
		t.Fatal(err)
	}
	ft := tablet.New(nil, st, "u")
	if err := ft.SetPage("nope", "p", nil); err == nil {
		t.Fatal("want error for unknown doc")
	}
	if err := ft.Open("nope", 1, time.Now()); err == nil {
		t.Fatal("want error for unknown doc")
	}
}

func TestLoadRoundTripsPages(t *testing.T) {
	ft := setup(t)
	ft.Notebook("nb", "Notes", "")
	if err := ft.SetPage("nb", "p1", fixture(t, "strokes_a.rm")); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	fresh := tablet.New(nil, ft.FS, "u")
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if err := fresh.SetPage("nb", "p2", fixture(t, "text_todo.rm")); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := pageOrder(t, ft, "nb"); !eq(got, []string{"p1", "p2"}) {
		t.Fatalf("order = %v", got)
	}
	if ft.ServerBlob("nb", "nb/p1.rm") == nil {
		t.Fatal("p1 lost after reload")
	}
}
