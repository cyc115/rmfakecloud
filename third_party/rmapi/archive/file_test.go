package archive

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestContentUnmarshalPageTagsObjectForm(t *testing.T) {
	// firmware 3.x / auto-organize: pageTags are objects
	var c Content
	err := json.Unmarshal([]byte(`{
		"fileType": "notebook",
		"pageCount": 2,
		"pageTags": [
			{"name": "todo", "pageId": "p1", "timestamp": 1793543844647},
			{"name": "review", "pageId": "p2", "timestamp": 1793543845000}
		]
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"todo", "review"}; !reflect.DeepEqual(c.Tags, want) {
		t.Fatalf("Tags = %v, want %v", c.Tags, want)
	}
}

func TestContentUnmarshalPageTagsStringForm(t *testing.T) {
	// old firmware: pageTags are plain strings
	var c Content
	err := json.Unmarshal([]byte(`{
		"fileType": "notebook",
		"pageCount": 1,
		"pageTags": ["todo", "review"]
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"todo", "review"}; !reflect.DeepEqual(c.Tags, want) {
		t.Fatalf("Tags = %v, want %v", c.Tags, want)
	}
}

func TestContentUnmarshalPageTagsMixedAndJunk(t *testing.T) {
	var c Content
	err := json.Unmarshal([]byte(`{
		"pageTags": ["todo", {"name": "review", "pageId": "p2"}, 42, {"pageId": "p3"}, ""]
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"todo", "review"}; !reflect.DeepEqual(c.Tags, want) {
		t.Fatalf("Tags = %v, want %v", c.Tags, want)
	}
}

func TestContentUnmarshalPageTagsMissing(t *testing.T) {
	var c Content
	if err := json.Unmarshal([]byte(`{"fileType": "notebook"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Tags == nil || len(c.Tags) != 0 {
		t.Fatalf("Tags = %v, want empty non-nil", c.Tags)
	}
}

func TestContentUnmarshalCPages(t *testing.T) {
	// a real firmware 3.x .content: no flat "pages" key, cPages with idx
	// order, a deleted page in register form, and a redir
	var c Content
	err := json.Unmarshal([]byte(`{
		"fileType": "notebook",
		"formatVersion": 2,
		"pageCount": 2,
		"cPages": {"pages": [
			{"id": "page-b", "idx": {"timestamp": "1:2", "value": "ab"}, "redir": {"value": 0}},
			{"id": "page-a", "idx": {"timestamp": "1:2", "value": "aa"}},
			{"id": "page-gone", "idx": {"timestamp": "1:2", "value": "ac"}, "deleted": {"timestamp": "1:2", "value": 1}}
		]},
		"pageTags": [{"name": "todo", "pageId": "page-a", "timestamp": 1793543844647}]
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Pages) != 0 {
		t.Fatalf("Pages = %v, want empty (v3 has no flat pages)", c.Pages)
	}
	if len(c.CPages.Pages) != 3 {
		t.Fatalf("CPages.Pages = %d entries, want 3", len(c.CPages.Pages))
	}
	b := c.CPages.Pages[0]
	if b.ID != "page-b" || b.Idx.Value != "ab" || b.Redir == nil || b.Redir.Value != 0 {
		t.Fatalf("page-b = %+v, want idx ab redir 0", b)
	}
	a := c.CPages.Pages[1]
	if a.ID != "page-a" || a.Idx.Value != "aa" || a.Redir != nil {
		t.Fatalf("page-a = %+v, want idx aa no redir", a)
	}
	gone := c.CPages.Pages[2]
	if gone.ID != "page-gone" || !bool(gone.Deleted) {
		t.Fatalf("page-gone = %+v, want Deleted true", gone)
	}
	if want := []string{"todo"}; !reflect.DeepEqual(c.Tags, want) {
		t.Fatalf("Tags = %v, want %v", c.Tags, want)
	}
}

func TestContentUnmarshalCPagesJunkIgnored(t *testing.T) {
	// junk in cPages must not fail the whole blob
	var c Content
	err := json.Unmarshal([]byte(`{
		"pageTags": ["todo"],
		"cPages": {"pages": ["junk", {"id": "page-a", "idx": {"value": "aa"}}, 42]}
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"todo"}; !reflect.DeepEqual(c.Tags, want) {
		t.Fatalf("Tags = %v, want %v", c.Tags, want)
	}
	if len(c.CPages.Pages) != 1 || c.CPages.Pages[0].ID != "page-a" {
		t.Fatalf("CPages.Pages = %+v, want only page-a", c.CPages.Pages)
	}
}

func TestContentUnmarshalCPagesNotAnArray(t *testing.T) {
	var c Content
	err := json.Unmarshal([]byte(`{
		"pageTags": ["todo"],
		"cPages": {"pages": "junk"}
	}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.CPages.Pages) != 0 {
		t.Fatalf("CPages.Pages = %+v, want empty", c.CPages.Pages)
	}
}
