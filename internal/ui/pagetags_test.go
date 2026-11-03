package ui

import (
	"net/http"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
	"github.com/gin-gonic/gin"
)

func TestPageTagsListsDistinctTagsOfLivePages(t *testing.T) {
	a := newAOAPI(t)
	tok := a.token("mike", true)

	code, body := a.do("GET", "/ui/api/automation/page-tags", tok, nil)
	if code != http.StatusOK || len(body["tags"].([]any)) != 0 {
		t.Fatalf("no tags yet: %d %v", code, body)
	}

	ft := tablet.New(t, a.storage, "mike")
	if err := ft.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ft.Folder("proj", "Projects", "")
	ft.Notebook("nb1", "Notes", a.inbox)
	ft.Notebook("nb2", "Plans", "proj")
	ft.Notebook("bin", "Old", "trash")
	for _, p := range []struct{ doc, page string }{{"nb1", "p1"}, {"nb1", "p2"}, {"nb2", "q1"}, {"bin", "x1"}} {
		if err := ft.SetPage(p.doc, p.page, []byte("rm-"+p.page)); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// "To-Do", "to_do" and "to do" are one tag, listed in one spelling
	must(ft.TagPage("nb1", "p1", "To-Do", now))
	must(ft.TagPage("nb1", "p1", "TO DO", now)) // same page, same tag: counted once
	must(ft.TagPage("nb1", "p2", " to_do ", now))
	must(ft.TagPage("nb1", "p2", "asset mgmt", now))
	must(ft.TagPage("nb2", "q1", "to do", now))
	must(ft.TagPage("nb2", "gone", "ghost", now)) // page not in the list
	must(ft.TagPage("bin", "x1", "trashed", now)) // doc in the Trash
	must(ft.Sync())

	code, body = a.do("GET", "/ui/api/automation/page-tags", tok, nil)
	if code != http.StatusOK {
		t.Fatalf("page-tags: %d %v", code, body)
	}
	tags := body["tags"].([]any)
	names := []string{}
	for _, x := range tags {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if len(names) != 2 || names[0] != "asset mgmt" || names[1] != "to do" {
		t.Fatalf("names: %v (%v)", names, tags)
	}
	todo := tags[1].(map[string]any)
	if todo["pages"].(float64) != 3 {
		t.Fatalf("todo pages: %v", todo)
	}
	docs := todo["docs"].([]any)
	if len(docs) != 2 {
		t.Fatalf("todo docs: %v", docs)
	}
	nb1, nb2 := docs[0].(map[string]any), docs[1].(map[string]any)
	if nb1["id"] != "nb1" || nb1["pages"].(float64) != 2 || nb1["kind"] != "notebooks" {
		t.Fatalf("nb1: %v", nb1)
	}
	if f := nb1["folders"].([]any); len(f) != 2 || f[0] != a.inbox || f[1] != "" {
		t.Fatalf("nb1 folders: %v", f)
	}
	if f := nb2["folders"].([]any); len(f) != 2 || f[0] != "proj" || f[1] != "" {
		t.Fatalf("nb2 folders: %v", f)
	}
}

func TestPageTagsWithoutBlobStorageIs503(t *testing.T) {
	a := newAOAPI(t)
	app := &ReactAppWrapper{cfg: a.cfg, organizer: autoorganize.NewService(a.storage, hub.NewHub(), 0)}
	router := gin.New()
	api := router.Group("/ui/api")
	auth := api.Group("")
	auth.Use(app.authMiddleware())
	app.registerAutomationRoutes(api, auth)
	a.router = router
	if code, body := a.do("GET", "/ui/api/automation/page-tags", a.token("mike", true), nil); code != http.StatusServiceUnavailable {
		t.Fatalf("nil blobs: %d %v", code, body)
	}
}
