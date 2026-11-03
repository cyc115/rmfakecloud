package ui

import (
	"fmt"
	"net/http"
	"strings"
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

func TestApplyPageTags(t *testing.T) {
	a := newAOAPI(t)
	tok := "Bearer " + apiToken
	ft := tablet.New(t, a.storage, "mike")
	if err := ft.Load(); err != nil {
		t.Fatal(err)
	}
	ft.Notebook("nb1", "Notes", a.inbox)
	if err := ft.SetPage("nb1", "p1", []byte("rm-p1")); err != nil {
		t.Fatal(err)
	}
	if err := ft.TagPage("nb1", "p1", "Work", time.Now()); err != nil {
		t.Fatal(err) // "work" is a known tag: it is on a page
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	apply := func(token string, body map[string]any) (int, map[string]any) {
		return a.do("POST", "/ui/api/automation/page-tags/apply", token, body)
	}
	if code, _ := apply("", map[string]any{"doc_id": "nb1", "page_id": "p1", "tags": []any{"work"}}); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
	if code, body := apply(tok, map[string]any{"doc_id": "nb1", "page_id": "p1", "tags": []any{"nope"}}); code != http.StatusBadRequest {
		t.Fatalf("unknown tag: %d %v", code, body)
	}
	for _, b := range []map[string]any{{"doc_id": "missing", "page_id": "p1", "tags": []any{"work"}}, {"doc_id": "nb1", "page_id": "p9", "tags": []any{"work"}}} {
		if code, body := apply(tok, b); code != http.StatusNotFound {
			t.Fatalf("%v: %d %v", b, code, body)
		}
	}
	// a second page gets the known tag
	if err := ft.SetPage("nb1", "p2", []byte("rm-p2")); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	code, body := apply(tok, map[string]any{"doc_id": "nb1", "page_id": "p2", "tags": []any{"work"}})
	if code != http.StatusOK || fmt.Sprint(body["added"]) != "[work]" {
		t.Fatalf("apply: %d %v", code, body)
	}
	if code, body := apply(tok, map[string]any{"doc_id": "nb1", "page_id": "p2", "tags": []any{"Work"}}); code != http.StatusOK || fmt.Sprint(body["added"]) != "[]" {
		t.Fatalf("again: %d %v", code, body)
	}
	tags, _ := ft.ServerContent("nb1")["pageTags"].([]any)
	found := false
	for _, x := range tags {
		m := x.(map[string]any)
		found = found || (m["pageId"] == "p2" && m["name"] == "work")
	}
	if !found {
		t.Fatalf("server .content lacks the tag: %v", tags)
	}
}

func TestEditDocumentTags(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)
	ft := tablet.New(t, a.storage, "mike")
	if err := ft.Load(); err != nil {
		t.Fatal(err)
	}
	ft.Notebook("nb1", "Notes", a.inbox)
	for _, p := range []string{"p1", "p2"} {
		if err := ft.SetPage("nb1", p, []byte("rm-"+p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	edit := func(body map[string]any) (int, map[string]any) {
		return a.do("POST", "/ui/api/documents/nb1/tags", tok, body)
	}
	code, body := edit(map[string]any{"add": []any{"Work", "Q3"}})
	if code != http.StatusOK || fmt.Sprint(body["tags"]) != "[Work Q3]" {
		t.Fatalf("add doc tags: %d %v", code, body)
	}
	code, body = edit(map[string]any{"remove": []any{"work"}})
	if code != http.StatusOK || fmt.Sprint(body["tags"]) != "[Q3]" {
		t.Fatalf("remove doc tag: %d %v", code, body)
	}
	code, body = edit(map[string]any{"page_id": "p2", "add": []any{"extract"}})
	if code != http.StatusOK || fmt.Sprint(body["pages"]) != "[map[id:p1 tags:[]] map[id:p2 tags:[extract]]]" {
		t.Fatalf("add page tag: %d %v", code, body)
	}
	code, body = edit(map[string]any{"page_id": "p2", "remove": []any{"Extract"}})
	if code != http.StatusOK || fmt.Sprint(body["pages"]) != "[map[id:p1 tags:[]] map[id:p2 tags:[]]]" {
		t.Fatalf("remove page tag: %d %v", code, body)
	}
	if code, _ := edit(map[string]any{"page_id": "p9", "add": []any{"x"}}); code != http.StatusNotFound {
		t.Fatalf("missing page: %d", code)
	}
	if code, _ := edit(map[string]any{"add": []any{"  "}}); code != http.StatusBadRequest {
		t.Fatalf("blank tag: %d", code)
	}
	if code, _ := a.do("POST", "/ui/api/documents/nb1/tags", "", map[string]any{"add": []any{"x"}}); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
	if got := fmt.Sprint(ft.ServerContent("nb1")["tags"]); !strings.Contains(got, "Q3") {
		t.Fatalf("server content tags: %s", got)
	}
}
