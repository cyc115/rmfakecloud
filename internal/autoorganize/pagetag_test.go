package autoorganize

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseContentPageTags(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"notebook","pageCount":0,"tags":["Doc"],"pageTags":[
		{"name":" ToDo ","pageId":"p1","timestamp":1693424634489},
		{"name":"todo","pageId":"p1","timestamp":1693424634000},
		{"name":"Security","pageId":"p1","timestamp":"bad"},
		{"name":"todo","pageId":"p2"},
		{"name":"  ","pageId":"p3","timestamp":1},
		{"name":"x","pageId":"","timestamp":1},
		{"name":7,"pageId":"p4"},
		"junk"]}`))
	if !ci.Readable() {
		t.Fatalf("unreadable: %+v", ci)
	}
	want := map[string]map[string]int64{
		"p1": {"todo": 1693424634489, "security": 0},
		"p2": {"todo": 0},
	}
	if !reflect.DeepEqual(ci.PageTags, want) {
		t.Fatalf("page tags = %v, want %v", ci.PageTags, want)
	}
	at, ok := ci.PageTag("p1", "  TODO")
	if !ok || !at.Equal(time.UnixMilli(1693424634489)) {
		t.Fatalf("PageTag(p1, TODO) = %v %v", at, ok)
	}
	if at, ok := ci.PageTag("p2", "todo"); !ok || !at.IsZero() {
		t.Fatalf("PageTag(p2) = %v %v, want tagged with no time", at, ok)
	}
	for _, c := range [][2]string{{"p1", "to do"}, {"p3", ""}, {"p9", "todo"}, {"p1", "doc"}} {
		if _, ok := ci.PageTag(c[0], c[1]); ok {
			t.Errorf("PageTag(%q, %q) = true", c[0], c[1])
		}
	}
	// document tags are unchanged and page tags never reach them
	if !reflect.DeepEqual(ci.Tags, []string{"Doc"}) || ci.HasTag("todo") {
		t.Fatalf("document tags = %v", ci.Tags)
	}
	if ci := ParseContent([]byte(`{"fileType":"notebook","pageTags":{"a":1}}`)); !ci.Readable() || ci.PageTags != nil {
		t.Fatalf("a non-array pageTags is no tags: %+v", ci)
	}
}

// entries cached by the parser before pageTags were read are re-read
func TestContentCacheReparsesEntriesWithoutPageTags(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	ft.Notebook("nb", "Notes", "")
	ft.SetPage("nb", "p1", []byte("a"))
	ft.Docs["nb"].Content["pageTags"] = []any{map[string]any{"name": "todo", "pageId": "p1", "timestamp": 5}}
	ft.Sync()
	tree, _ := storage.GetCachedTree(uid)
	var ch string
	for _, d := range tree.Docs {
		if d.EntryName == "nb" {
			ch = contentEntry(d).Hash
		}
	}
	old := map[string]ContentInfo{ch: {FileType: "notebook", PageCount: 1, PageOrder: []string{"p1"}, ParseVersion: 10}}
	cache := NewContentCache(old, storage.BlobStorage(uid).GetReader)
	snap := NewSnapshot(tree)
	ci := cache.Get(snap.Docs["nb"])
	if _, ok := ci.PageTag("p1", "todo"); !ok {
		t.Fatalf("a version 10 entry was trusted: %+v", ci)
	}
}

func pageTaggedRule() V1Rule {
	r := pipelineV1()
	r.Trigger = V1Trigger{Type: TriggerPageTagged, Tag: "todo"}
	return r
}

func TestPageTaggedValidation(t *testing.T) {
	f := false
	cases := []struct {
		name string
		edit func(r *V1Rule)
		// move a move rule instead of a pipeline rule
		move bool
		// want "" = valid, else a substring of the error
		want string
	}{
		{name: "tag", edit: func(r *V1Rule) {}},
		{name: "padded tag", edit: func(r *V1Rule) { r.Trigger.Tag = "  Todo " }},
		{name: "empty tag", edit: func(r *V1Rule) { r.Trigger.Tag = "" }, want: "tag"},
		{name: "blank tag", edit: func(r *V1Rule) { r.Trigger.Tag = " \t" }, want: "tag"},
		{name: "empty watch is the whole tree", edit: func(r *V1Rule) { r.Watch = nil }},
		{name: "trash watch", edit: func(r *V1Rule) { r.Watch = []WatchRef{{ID: "trash", Kind: WatchFolder}} }, want: "trash"},
		{name: "bad watch kind", edit: func(r *V1Rule) { r.Watch = []WatchRef{{ID: "x", Kind: "page"}} }, want: "kind"},
		{name: "scope", edit: func(r *V1Rule) { r.Scope = "pdf" }, want: "scope"},
		{name: "just save notes", edit: func(r *V1Rule) { r.Action.Tasks = &f }},
		{name: "page_changed still needs a watch", edit: func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerPageChanged}; r.Watch = nil }, want: "watch"},
		{name: "page_changed just saves notes", edit: func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerPageChanged}; r.Action.Tasks = &f }},
		{name: "move trigger on a pipeline rule", edit: func(r *V1Rule) { r.Trigger = V1Trigger{Type: TriggerTagIs, Tag: "x"} }, want: "trigger"},
		{name: "page_tagged on a move rule", move: true, edit: func(r *V1Rule) {
			r.Scope = ScopeAll
			r.Trigger = V1Trigger{Type: TriggerPageTagged, Tag: "todo"}
		}, want: "page_tagged"},
		{name: "tasks on a move rule", move: true, edit: func(r *V1Rule) { r.Action.Tasks = &f }, want: "tasks"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := pageTaggedRule()
			if c.move {
				r = seedV1()
			}
			c.edit(&r)
			err := r.Validate()
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestNormalizePageTaggedAndTasks(t *testing.T) {
	tr, f := true, false
	cfg := &V1Config{Rules: []V1Rule{
		{Trigger: V1Trigger{Type: TriggerPageTagged, Tag: "  ToDo "}, Action: V1Action{Type: ActionPipeline, Tasks: &tr}},
		{Trigger: V1Trigger{Type: TriggerPageTagged, Tag: "todo"}, Action: V1Action{Type: ActionPipeline, Tasks: &f}},
	}}
	cfg.Normalize()
	a, b := cfg.Rules[0], cfg.Rules[1]
	if a.Trigger.Tag != "ToDo" {
		t.Errorf("tag not trimmed: %q", a.Trigger.Tag)
	}
	if a.Scope != ScopeAll || a.Action.Prompt != DefaultPipelinePrompt || a.Action.Model != DefaultPipelineModel {
		t.Errorf("pipeline defaults not filled: %+v", a)
	}
	// true is the default: stored as absent so existing rules serialise unchanged
	if a.Action.Tasks != nil || !a.Action.ExtractsTasks() {
		t.Errorf("tasks true not normalised away: %v", a.Action.Tasks)
	}
	if b.Action.Tasks == nil || *b.Action.Tasks || b.Action.ExtractsTasks() {
		t.Errorf("tasks false lost: %v", b.Action.Tasks)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("normalized config invalid: %v", err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if strings.Contains(string(ja), `"tasks"`) || !strings.Contains(string(jb), `"tasks":false`) {
		t.Errorf("tasks json: %s / %s", ja, jb)
	}
	var back V1Rule
	if err := json.Unmarshal(jb, &back); err != nil || back.Action.ExtractsTasks() || back.Trigger.Tag != "todo" {
		t.Errorf("round trip: %+v %v", back, err)
	}
}

func TestPageTaggedEvalSkipsNeverMatches(t *testing.T) {
	r := moveRule("r", "src", "dst", trig(TriggerPageTagged, P{Tag: "todo"}))
	ev := evalRule(&r, &DocSnap{}, &ContentInfo{}, nil, time.Now())
	if ev.match || ev.skip == "" {
		t.Errorf("page_tagged under the move planner must skip, got %+v", ev)
	}
}

func TestPageTagSpellingsMatch(t *testing.T) {
	c := &ContentInfo{PageTags: pageTags([]any{
		map[string]any{"name": "To-Do", "pageId": "p1", "timestamp": float64(1000)},
	})}
	for _, tag := range []string{"to do", "to_do", "TO-DO", " to  do "} {
		if _, ok := c.PageTag("p1", tag); !ok {
			t.Errorf("rule tag %q does not match page tag To-Do", tag)
		}
	}
	if NormalizePageTag("to_do") != NormalizeTag("to-do") {
		t.Error("page and document tags normalise differently")
	}
}

func TestParseContentDocTagTimes(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"notebook","pageCount":0,"tags":[{"name":"Extract","timestamp":1700000000000},"Work"]}`))
	if at, ok := ci.DocTag("extract"); !ok || !at.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("extract: %v %v", at, ok)
	}
	if at, ok := ci.DocTag("work"); !ok || !at.IsZero() {
		t.Fatalf("work: %v %v", at, ok)
	}
	if _, ok := ci.DocTag("x"); ok {
		t.Fatal("x should be absent")
	}
	if !reflect.DeepEqual(ci.Tags, []string{"Extract", "Work"}) {
		t.Fatalf("tags: %v", ci.Tags)
	}
}

func TestPatchContentPageTags(t *testing.T) {
	raw := []byte(`{"fileType":"notebook","zoom":{"x":1.5},"pageTags":[{"name":"To-Do","pageId":"p1","timestamp":1}]}`)
	out, added, err := PatchContentPageTags(raw, "p1", []string{"to do", "extract"}, 1700000000000)
	if err != nil || !reflect.DeepEqual(added, []string{"extract"}) {
		t.Fatalf("added %v err %v", added, err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["zoom"].(map[string]any)["x"] != 1.5 || m["fileType"] != "notebook" {
		t.Fatalf("other keys changed: %s", out)
	}
	tags := m["pageTags"].([]any)
	if len(tags) != 2 || tags[0].(map[string]any)["name"] != "To-Do" {
		t.Fatalf("pageTags: %v", tags)
	}
	if n := tags[1].(map[string]any); n["name"] != "extract" || n["pageId"] != "p1" || n["timestamp"] != float64(1700000000000) {
		t.Fatalf("new tag: %v", n)
	}
	// again: nothing to add
	if out2, added2, err := PatchContentPageTags(out, "p1", []string{"Extract"}, 2); err != nil || len(added2) != 0 || out2 != nil {
		t.Fatalf("second: %s %v %v", out2, added2, err)
	}
	// another page: added
	if _, added3, _ := PatchContentPageTags(out, "p2", []string{"extract"}, 2); !reflect.DeepEqual(added3, []string{"extract"}) {
		t.Fatalf("p2: %v", added3)
	}
}

func TestPatchContentPageTagsRemove(t *testing.T) {
	raw := []byte(`{"pageTags":[{"name":"To-Do","pageId":"p1","timestamp":1},{"name":"todo","pageId":"p2","timestamp":1},{"name":"x","pageId":"p1","timestamp":1}],"k":1}`)
	out, removed, err := PatchContentPageTagsRemove(raw, "p1", []string{"to do", "nope"})
	if err != nil || !reflect.DeepEqual(removed, []string{"to do"}) {
		t.Fatalf("removed %v err %v", removed, err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	tags := m["pageTags"].([]any)
	if len(tags) != 2 || tags[0].(map[string]any)["pageId"] != "p2" || tags[1].(map[string]any)["name"] != "x" || m["k"] != float64(1) {
		t.Fatalf("left: %s", out)
	}
	if out2, removed2, _ := PatchContentPageTagsRemove(out, "p1", []string{"to do"}); out2 != nil || len(removed2) != 0 {
		t.Fatalf("second: %s %v", out2, removed2)
	}
}
