package pagewatch

import (
	"reflect"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

// TestPageChangedTraceIDsGolden pins the trace ids of page_changed rules and
// the feed: rm-pages dedupes on them (R17), so a change to the key would make
// every unaccepted change look new. The values were captured on master before
// page_tagged existed; they must never change.
func TestPageChangedTraceIDsGolden(t *testing.T) {
	e := newEnv(t)
	e.rule("r1", folderWatch(autoorganize.RootID))
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.page("nb", "p2", "text_todo.rm")
	got := map[string]string{}
	collect := func(p Plan) {
		for _, ev := range p.Feed {
			got["feed "+ev.Kind+" "+ev.DocID+"/"+ev.PageID] = ev.TraceID
		}
		for _, ev := range p.Pipeline {
			got[ev.Kind+" "+ev.DocID+"/"+ev.PageID] = ev.TraceID
		}
	}
	collect(e.sweep()) // join: transcribe_only p1, p2
	e.page("nb", "p1", "strokes_ab.rm")
	if err := e.ft.DeletePage("nb", "p2"); err != nil {
		t.Fatal(err)
	}
	collect(e.sweep()) // member: edit p1 (before = a), page_deleted p2
	e.ft.Notebook("nb2", "New", "")
	e.page("nb2", "p1", "strokes_a.rm")
	collect(e.sweep()) // new doc: edit with an empty before
	want := map[string]string{
		"transcribe_only nb/p1": "35e70ff86c7051430c27482e3fa9ce97",
		"transcribe_only nb/p2": "3c657ca943154dc63009ac41d28ef446",
		"edit nb/p1":            "e66e4e4e251fd6b2833521fe95fcbeaa",
		"page_deleted nb/p2":    "a3d5c290f2342b44c927fa53c3849896",
		"edit nb2/p1":           "ce696444ba0668081d3a79103bc3c671",
		"feed edit nb/p1":       "2e3672f7bc346933a8276eddc48ee922",
		"feed edit nb2/p1":      "4bba64c53d2cf171e558f04f6cbf5a00",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace ids changed:\n got %#v\nwant %#v", got, want)
	}
}
