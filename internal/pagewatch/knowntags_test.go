package pagewatch

import (
	"reflect"
	"testing"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

func TestKnownTagsUnion(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "Extract")
	off := e.tagRule("r2", "disabled tag")
	off.Enabled = false
	r3 := e.rule("r3", folderWatch(""))
	r3.Conditions = []automation.Condition{
		{Type: automation.CondTagIs, Params: automation.Params{Tag: "Q3"}},
		{Type: automation.CondPageTagIs, Params: automation.Params{Tag: "to_do"}},
	}
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.tag("nb", "p1", "To-Do", e.now)
	e.docTag("nb", "Work", e.now)
	e.ft.Notebook("bin", "Old", "trash")
	e.docTag("bin", "trashed", e.now)
	snap, content := e.snapshot()
	got := autoorganize.KnownTags(e.cfg, snap, content)
	if want := []string{"extract", "q3", "to do", "work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("known = %q, want %q", got, want)
	}
}

func TestFeedEditCarriesKnownTags(t *testing.T) {
	e := newEnv(t)
	e.tagRule("r1", "extract")
	e.ft.Notebook("nb", "Notes", "")
	e.page("nb", "p1", "strokes_a.rm")
	e.sweep()
	e.page("nb", "p1", "strokes_ab.rm")
	e.tag("nb", "p1", "extract", e.now)
	p := e.detect()
	wantEvents(t, "feed", p.Feed, "edit nb/p1")
	if got := p.Feed[0].KnownTags; !reflect.DeepEqual(got, []string{"extract"}) {
		t.Fatalf("feed known_tags = %q", got)
	}
	for _, ev := range p.Pipeline {
		if ev.KnownTags != nil {
			t.Fatalf("pipeline event has known_tags: %+v", ev)
		}
	}
}
