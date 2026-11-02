package pagewatch

import (
	"reflect"
	"testing"
	"time"

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

// TestPageTaggedTraceIDsGolden pins the trace ids of a page_tagged rule over
// the whole tree, and its state trigger key: both feed rm-pages dedupe (R17)
// and the rule's baselines. Captured on f38d168 before rules v2; they must
// never change.
func TestPageTaggedTraceIDsGolden(t *testing.T) {
	e := newEnv(t)
	e.tagRule("t1", "To-Do")
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
	e.tag("nb", "p1", "To-Do", e.now.Add(-24*time.Hour))
	p := e.sweep() // tagged before the first sweep: transcribe_only p1
	wantEvents(t, "sweep 1", p.Pipeline, "transcribe_only nb/p1 t1")
	collect(p)
	e.tag("nb", "p2", "To-Do", e.now)
	p = e.sweep() // tagged after the first sweep: edit p2, empty before
	wantEvents(t, "sweep 2", p.Pipeline, "edit nb/p2 t1")
	if p.Pipeline[0].BeforeHash != "" {
		t.Fatalf("newly tagged page has a before: %+v", p.Pipeline[0])
	}
	collect(p)
	e.page("nb", "p1", "strokes_ab.rm")
	p = e.sweep() // edited while tagged: edit p1
	wantEvents(t, "sweep 3", p.Pipeline, "edit nb/p1 t1")
	collect(p)
	e.untag("nb", "p2", "To-Do")
	p = e.sweep() // untagged: no event
	wantEvents(t, "sweep 4", p.Pipeline)
	collect(p)
	want := map[string]string{
		"transcribe_only nb/p1": "7b01cdd14994aac0c23c6b4dfab22d0b",
		"edit nb/p2":            "adf9aecad6c562f6f46ae66e5ae20aae",
		"edit nb/p1":            "4aff3dd25870a5f017857257893adb24",
		"feed edit nb/p1":       "2e3672f7bc346933a8276eddc48ee922",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace ids changed:\n got %#v\nwant %#v", got, want)
	}
	if tr := e.state.Rules["t1"].Trigger; tr != "page_tagged:to do" {
		t.Fatalf("state trigger = %q, want %q", tr, "page_tagged:to do")
	}
}
