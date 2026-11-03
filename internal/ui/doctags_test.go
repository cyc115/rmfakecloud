package ui

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

func TestDocTagsListsDocAndPageTagsInPageOrder(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)

	ft := tablet.New(t, a.storage, "mike")
	if err := ft.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	nb := ft.Notebook("nb1", "July 2026", a.inbox)
	nb.Content["tags"] = []any{map[string]any{"name": "Work", "timestamp": now.UnixMilli()}, "work", "Q3"}
	ft.Folder("dir", "Projects", "")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"p1", "p2", "p3"} {
		must(ft.SetPage("nb1", p, []byte("rm-"+p)))
	}
	must(ft.MovePageBefore("nb1", "p3", "p1")) // display order: p3 p1 p2
	must(ft.TagPage("nb1", "p1", "To-Do", now))
	must(ft.TagPage("nb1", "p1", "to do", now)) // same tag: listed once, first spelling
	must(ft.TagPage("nb1", "p1", "extract", now))
	must(ft.TagPage("nb1", "gone", "ghost", now)) // page not in the list
	must(ft.Sync())

	code, body := a.do("GET", "/ui/api/documents/nb1/tags", tok, nil)
	if code != http.StatusOK {
		t.Fatalf("tags: %d %v", code, body)
	}
	if got := fmt.Sprint(body["tags"]); got != "[Work Q3]" {
		t.Fatalf("doc tags: %s", got)
	}
	if got := fmt.Sprint(body["pages"]); got != "[map[id:p3 tags:[]] map[id:p1 tags:[To-Do extract]] map[id:p2 tags:[]]]" {
		t.Fatalf("pages: %s", got)
	}

	for _, id := range []string{"missing", "dir"} {
		if code, body := a.do("GET", "/ui/api/documents/"+id+"/tags", tok, nil); code != http.StatusNotFound {
			t.Fatalf("%s: %d %v", id, code, body)
		}
	}
	if code, _ := a.do("GET", "/ui/api/documents/nb1/tags", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}
