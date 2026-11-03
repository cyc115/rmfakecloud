package pagewatch

import (
	"testing"
	"time"
)

// setRedir sets cPages redir.value of the doc's pages, in page order
func (e *env) setRedir(doc string, redir ...int) {
	e.t.Helper()
	cp := e.ft.Docs[doc].Content["cPages"].(map[string]any)
	var items []map[string]any
	switch ps := cp["pages"].(type) {
	case []any:
		for _, p := range ps {
			items = append(items, p.(map[string]any))
		}
	case []map[string]any:
		items = ps
	}
	if len(items) != len(redir) {
		e.t.Fatalf("%d pages, %d redirs", len(items), len(redir))
	}
	for i, m := range items {
		m["redir"] = map[string]any{"value": redir[i]}
	}
}

func pdfPages(evs []Event) map[string]any {
	out := map[string]any{}
	for _, ev := range evs {
		if ev.PDFPage == nil {
			out[ev.PageID] = nil
		} else {
			out[ev.PageID] = *ev.PDFPage
		}
	}
	return out
}

func TestEventsCarryPDFPage(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(""))
	e.ft.PDF("pdf", "Report", "", 2, 0, time.Time{})
	for _, p := range []string{"a", "b", "c"} {
		e.page("pdf", p, "strokes_a.rm")
	}
	e.setRedir("pdf", 0, -1, 1) // b is a note page inserted between PDF pages 1 and 2
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()

	for _, p := range []string{"a", "b", "c"} {
		e.page("pdf", p, "strokes_ab.rm")
	}
	e.page("nb", "p1", "strokes_ab.rm")
	p := e.detect()
	want := map[string]any{"a": 0, "b": -1, "c": 1, "p1": nil}
	for what, evs := range map[string][]Event{"feed": p.Feed, "pipeline": p.Pipeline} {
		if got := pdfPages(evs); len(got) != 4 || got["a"] != want["a"] || got["b"] != want["b"] || got["c"] != want["c"] || got["p1"] != nil {
			t.Fatalf("%s pdf_page = %v, want %v", what, got, want)
		}
		for _, ev := range evs {
			if ev.PageID == "c" && ev.PageIndex != 2 {
				t.Fatalf("%s page_index of c = %d", what, ev.PageIndex)
			}
		}
	}
}
