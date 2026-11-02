package pagewatch

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

func (e *env) preview(r *automation.Rule) PagePreview {
	e.t.Helper()
	snap, content := e.snapshot()
	return PreviewRule(snap, content, r)
}

func previewIDs(p PagePreview) []string {
	ids := []string{}
	for _, d := range p.Docs {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestPreviewRuleCoversInRuleDocs(t *testing.T) {
	e := newEnv(t)
	r := e.rule("r1", folderWatch("f"))
	r.Conditions = []automation.Condition{
		cond(automation.CondKindIs, automation.Params{Kind: automation.KindNotebooks}),
		cond(automation.CondNameMatches, automation.Params{Pattern: "Journal*"}),
	}
	e.ft.Folder("f", "Deep", "")
	e.ft.Notebook("hit", "Journal one", "f")
	e.page("hit", "p1", "strokes_a.rm")
	e.page("hit", "p2", "strokes_ab.rm")
	e.ft.Notebook("name", "Other", "f")
	e.page("name", "p1", "strokes_a.rm")
	e.ft.Notebook("outside", "Journal two", "")
	e.page("outside", "p1", "strokes_a.rm")
	e.ft.PDF("pdf", "Journal pdf", "f", 3, 0, e.now)
	e.page("pdf", "p1", "strokes_a.rm")
	e.ft.Notebook("bin", "Journal bin", "trash")
	e.page("bin", "p1", "strokes_a.rm")
	e.ft.Notebook("out", "Journal out", "f")
	e.page("out", "p1", "strokes_a.rm")
	e.ft.Docs["out"].Content["tags"] = []any{"no automation"}

	p := e.preview(r)
	if p.Subject != "page" || !reflect.DeepEqual(previewIDs(p), []string{"hit"}) || p.Pages != 2 {
		t.Fatalf("preview = %+v", p)
	}
	if d := p.Docs[0]; d.Name != "Journal one" || d.Path != "Deep / Journal one" || d.Pages != 2 {
		t.Fatalf("doc = %+v", d)
	}
	// the watcher agrees: its first sweep joins exactly these docs
	got := map[string]bool{}
	for _, ev := range e.detect().Pipeline {
		got[ev.DocID] = true
	}
	if len(got) != 1 || !got["hit"] {
		t.Fatalf("watcher docs = %v", got)
	}
}

func TestPreviewRuleEmptyAndMissing(t *testing.T) {
	e := newEnv(t)
	r := e.rule("r1", folderWatch("gone"))
	p := e.preview(r)
	if p.Docs == nil || p.MissingWatches == nil || len(p.Docs) != 0 || p.Pages != 0 || len(p.MissingWatches) != 1 || p.MissingWatches[0].WatchID != "gone" {
		t.Fatalf("preview = %+v", p)
	}
	if p := PreviewRule(nil, nil, r); p.Subject != "page" || p.Docs == nil {
		t.Fatalf("nil snapshot = %+v", p)
	}
}

func TestPreviewRuleTaggedPagesOnly(t *testing.T) {
	e := newEnv(t)
	r := e.tagRule("r1", "todo")
	e.ft.Notebook("a", "A", "")
	e.page("a", "p1", "strokes_a.rm")
	e.page("a", "p2", "strokes_ab.rm")
	e.page("a", "p3", "text_todo.rm")
	e.ft.Notebook("b", "B", "")
	e.page("b", "p1", "strokes_a.rm")
	e.ft.Notebook("c", "C", "")
	e.page("c", "p1", "strokes_a.rm")
	e.sweep()
	e.tag("a", "p1", " TODO ", e.now)
	e.tag("a", "p3", "todo", e.now)
	e.tag("b", "p1", "other", e.now)

	p := e.preview(r)
	if !reflect.DeepEqual(previewIDs(p), []string{"a"}) || p.Pages != 2 || p.Docs[0].Pages != 2 {
		t.Fatalf("tagged preview = %+v", p)
	}
	// a second page_tag_is condition narrows the set (P4)
	r.Conditions = append(r.Conditions, cond(automation.CondPageTagIs, automation.Params{Tag: "extra"}))
	e.tag("a", "p3", "extra", e.now)
	if p := e.preview(r); p.Pages != 1 || p.Docs[0].Pages != 1 {
		t.Fatalf("two tags = %+v", p)
	}
	// a blank tag matches no page
	blank := e.tagRule("r2", " ")
	if p := e.preview(blank); len(p.Docs) != 0 || p.Pages != 0 {
		t.Fatalf("blank tag = %+v", p)
	}
}
