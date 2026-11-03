package autoorganize

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

// timeP created_time / modified_time parameters
func timeP(compare string, amount int, unit string) P {
	return P{Compare: compare, Amount: amount, Unit: unit}
}

// tagRule an enabled document rule over the documents directly in from that
// adds (or removes) tag when tr and conds hold
func tagRule(id, from, action, tag string, tr automation.Trigger, conds ...automation.Condition) automation.Rule {
	return automation.Rule{ID: id, Enabled: true,
		When:       automation.When{Subject: automation.SubjectDocument, In: []automation.Place{{ID: from, Kind: automation.PlaceFolder}}, Trigger: tr},
		Conditions: append([]automation.Condition{}, conds...),
		Actions:    []automation.Action{{Type: action, Tag: tag}}}
}

func TestCreatedTimeWithinAndOlder(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("dest", "Dest", "")
	created := func(h float64) func(md *models.MetadataFile) {
		return func(md *models.MetadataFile) { md.CreatedTime = hoursAgo(now, h); md.LastModified = hoursAgo(now, 0.5) }
	}
	m.doc("h2", "2 hours old", "inbox", ContentInfo{FileType: "notebook"}, created(2))
	m.doc("h30", "30 hours old", "inbox", pdfInfo(1), created(30))
	m.doc("d10", "10 days old", "inbox", pdfInfo(1), created(240))
	// no createdTime: a skip, never a guess from lastModified
	m.doc("none", "No createdTime", "inbox", pdfInfo(1), func(md *models.MetadataFile) { md.LastModified = hoursAgo(now, 1) })

	cases := []struct {
		p    P
		want string
	}{
		{timeP(automation.CompareWithin, 24, automation.UnitHours), "h2"},
		{timeP(automation.CompareOlder, 24, automation.UnitHours), "d10,h30"},
		{timeP(automation.CompareWithin, 2, automation.UnitDays), "h2,h30"},
		{timeP(automation.CompareOlder, 1, automation.UnitWeeks), "d10"},
		{timeP(automation.CompareWithin, 2, automation.UnitWeeks), "d10,h2,h30"},
	}
	for _, c := range cases {
		p := m.plan(t, now, nil, moveRule("r1", "inbox", "dest", trig(TriggerCreatedTime, c.p)))
		ids := movedIDs(p)
		sortStrings(ids)
		if got := strings.Join(ids, ","); got != c.want {
			t.Errorf("%+v: moves = %s, want %s", c.p, got, c.want)
		}
		if s := skipOf(p, "none"); s == nil || s.Reason != SkipTimeUnknown {
			t.Errorf("%+v: missing createdTime: %+v", c.p, s)
		}
	}
}

func TestModifiedTimeTriggerAndCondition(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("dest", "Dest", "")
	m.doc("fresh", "Edited an hour ago", "inbox", ContentInfo{FileType: "notebook"}, func(md *models.MetadataFile) {
		md.CreatedTime = daysAgo(now, 30)
		md.LastModified = hoursAgo(now, 1)
	})
	m.doc("cold", "Edited 3 days ago", "inbox", ContentInfo{FileType: "notebook"}, func(md *models.MetadataFile) {
		md.CreatedTime = daysAgo(now, 30)
		md.LastModified = daysAgo(now, 3)
	})
	p := m.plan(t, now, nil, moveRule("r1", "inbox", "dest", trig(TriggerModifiedTime, timeP(automation.CompareWithin, 6, automation.UnitHours))))
	if got := strings.Join(movedIDs(p), ","); got != "fresh" {
		t.Fatalf("trigger: moves = %s", got)
	}
	// as a condition: created over a week ago AND changed in the last 2 days
	r := moveRule("r1", "inbox", "dest", trig(TriggerCreatedTime, timeP(automation.CompareOlder, 1, automation.UnitWeeks)),
		cond(automation.CondModifiedTime, timeP(automation.CompareWithin, 2, automation.UnitDays)))
	p = m.plan(t, now, nil, r)
	if got := strings.Join(movedIDs(p), ","); got != "fresh" {
		t.Fatalf("condition: moves = %s", got)
	}
	if !strings.Contains(p.Moves[0].Detail, "created 30 days ago") || !strings.Contains(p.Moves[0].Detail, "changed 1 h ago") {
		t.Errorf("detail = %q", p.Moves[0].Detail)
	}
}

// TestTimeConditionOnPageRule the page watch evaluates created_time and
// modified_time on the page's document; a missing time keeps the doc out
func TestTimeConditionOnPageRule(t *testing.T) {
	now := time.Now()
	r := pageRule("p1", automation.Place{ID: RootID, Kind: automation.PlaceFolder})
	r.Conditions = []automation.Condition{cond(automation.CondCreatedTime, timeP(automation.CompareWithin, 1, automation.UnitDays))}
	ci := ContentInfo{FileType: "notebook"}
	newDoc := &DocSnap{ID: "a", CreatedTime: hoursAgo(now, 3)}
	oldDoc := &DocSnap{ID: "b", CreatedTime: daysAgo(now, 3)}
	if in, skip := DocConditionsMatch(&r, newDoc, &ci, now); !in || skip != "" {
		t.Errorf("new doc: in %v skip %q", in, skip)
	}
	if in, skip := DocConditionsMatch(&r, oldDoc, &ci, now); in || skip != "" {
		t.Errorf("old doc: in %v skip %q", in, skip)
	}
	if in, skip := DocConditionsMatch(&r, &DocSnap{ID: "c"}, &ci, now); in || !strings.HasPrefix(skip, SkipTimeUnknown) {
		t.Errorf("no createdTime: in %v skip %q", in, skip)
	}
}

func TestTagRulePlan(t *testing.T) {
	now := time.Now()
	m := newMemTree()
	m.folder("inbox", "Inbox", "")
	m.folder("dest", "Dest", "")
	created := func(h float64, pinned bool) func(md *models.MetadataFile) {
		return func(md *models.MetadataFile) { md.CreatedTime = hoursAgo(now, h); md.Pinned = pinned }
	}
	m.doc("new", "New note", "inbox", ContentInfo{FileType: "notebook"}, created(2, false))
	m.doc("tagged", "Already tagged", "inbox", ContentInfo{FileType: "notebook", Tags: []string{"New"}}, created(3, false))
	m.doc("pinned", "Pinned new note", "inbox", ContentInfo{FileType: "notebook"}, created(4, true))
	m.doc("old", "Old note", "inbox", ContentInfo{FileType: "notebook", Tags: []string{"new", "x"}}, created(48, false))
	m.doc("optout", "Opted out", "inbox", ContentInfo{FileType: "notebook", Tags: []string{"no automation"}}, created(1, false))

	add := tagRule("add", "inbox", automation.ActionAddTag, "new", trig(TriggerCreatedTime, timeP(automation.CompareWithin, 24, automation.UnitHours)))
	rm := tagRule("rm", "inbox", automation.ActionRemoveTag, "new", trig(TriggerCreatedTime, timeP(automation.CompareOlder, 24, automation.UnitHours)),
		cond(automation.CondTagIs, P{Tag: "new"}))
	// a move rule ahead of the tag rules: tagging ignores its claim
	mv := moveRule("mv", "inbox", "dest", trig(TriggerNameMatches, P{Pattern: "New note"}))
	p := m.plan(t, now, nil, mv, add, rm)

	got := map[string]string{}
	for _, x := range p.Moves {
		got[x.RuleID+":"+x.DocID] = x.Action + " " + x.Tag
	}
	want := map[string]string{
		"mv:new":     " ",
		"add:new":    "add_tag new",
		"add:pinned": "add_tag new",
		"rm:old":     "remove_tag new",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planned = %v, want %v", got, want)
	}
	for _, x := range p.Moves {
		if x.Action != "" && (x.To != x.From || x.ToName != x.FromName) {
			t.Errorf("tag change must not move: %+v", x)
		}
	}
	if s := skipOf(p, "optout"); s == nil || s.Reason != SkipNoAutomation {
		t.Errorf("no-automation: %+v", s)
	}
	if rr := ruleResult(p, "add"); rr == nil || rr.DestName != "add tag “new”" || len(rr.Problems) != 0 || rr.Matches != 2 {
		t.Errorf("add rule result: %+v", rr)
	}
}

func TestPatchContentTags(t *testing.T) {
	const now = int64(1762000000000)
	cases := []struct {
		name string
		in   string
		tag  string
		add  bool
		want string // "" = unchanged
	}{
		{"add to objects", `{"fileType":"notebook","tags":[{"name":"x","timestamp":1}],"textScale":1.25}`, "new", true,
			`{"fileType":"notebook","tags":[{"name":"x","timestamp":1},{"name":"new","timestamp":1762000000000}],"textScale":1.25}`},
		{"add to strings", `{"tags":["x"]}`, "new", true, `{"tags":["x","new"]}`},
		{"add without list", `{"fileType":"pdf"}`, "new", true, `{"fileType":"pdf","tags":[{"name":"new","timestamp":1762000000000}]}`},
		{"add present", `{"tags":[{"name":"New","timestamp":1}]}`, "new", true, ""},
		{"remove normalised", `{"tags":[{"name":"New","timestamp":1},"x","new"]}`, "new", false, `{"tags":["x"]}`},
		{"remove absent", `{"tags":["x"]}`, "new", false, ""},
		{"keeps unknown keys", `{"cPages":{"pages":[{"id":"p1"}]},"tags":[],"zoomMode":"bestFit"}`, "new", true,
			`{"cPages":{"pages":[{"id":"p1"}]},"tags":[{"name":"new","timestamp":1762000000000}],"zoomMode":"bestFit"}`},
	}
	for _, c := range cases {
		out, ok, err := PatchContentTags([]byte(c.in), c.tag, c.add, now)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if c.want == "" {
			if ok {
				t.Errorf("%s: changed to %s", c.name, out)
			}
			continue
		}
		var g, w any
		json.Unmarshal(out, &g)
		json.Unmarshal([]byte(c.want), &w)
		if !ok || !reflect.DeepEqual(g, w) {
			t.Errorf("%s: got %s (changed %v), want %s", c.name, out, ok, c.want)
		}
	}
	if _, _, err := PatchContentTags([]byte(`{"tags":"x"}`), "new", true, now); err == nil {
		t.Error("tags that are not a list must be an error, not overwritten")
	}
}

// TestTagRulesEndToEnd the "new" tag use case: one rule tags notes created in
// the last 24 hours, a second removes the tag once they are older
func TestTagRulesEndToEnd(t *testing.T) {
	ft, storage := newHarness(t, uid, true)
	now := time.Now()
	ft.Folder("inbox", "Inbox", "")
	nb := ft.Notebook("nb", "Fresh notes", "inbox")
	nb.Meta["createdTime"] = tablet.Ms(now.Add(-2 * time.Hour))
	nb.Content["cPages"] = map[string]any{"pages": []any{map[string]any{"id": "p1", "idx": map[string]any{"value": "ba"}}}}
	nb.Content["pageCount"] = 1
	old := ft.PDF("old", "Old paper", "inbox", 3, 0, time.Time{})
	old.Meta["createdTime"] = tablet.Ms(now.Add(-72 * time.Hour))
	ft.Sync()
	beforeMeta := ft.ServerMetaRaw("nb")
	beforeContent := ft.ServerContent("nb")

	svc, notifier, clk := newService(t, storage)
	add := tagRule("add", "inbox", automation.ActionAddTag, "new", trig(TriggerCreatedTime, timeP(automation.CompareWithin, 24, automation.UnitHours)))
	rm := tagRule("rm", "inbox", automation.ActionRemoveTag, "new", trig(TriggerCreatedTime, timeP(automation.CompareOlder, 24, automation.UnitHours)),
		cond(automation.CondTagIs, P{Tag: "new"}))

	// dry: planned and logged, nothing written
	saveConfig(t, svc, uid, ModeDry, add, rm)
	res, err := svc.RunNow(uid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Planned != 1 || res.Run.Tagged != 0 || notifier.count() != 0 {
		t.Fatalf("dry run: %+v", res.Run)
	}
	if !reflect.DeepEqual(ft.ServerContent("nb"), beforeContent) {
		t.Fatal("dry run changed the content")
	}

	// live: the tag is added, nothing else in the document changes
	saveConfig(t, svc, uid, ModeLive, add, rm)
	res, err = svc.RunNow(uid)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Tagged != 1 || res.Run.Moved != 0 || res.Run.Errors != 0 || notifier.count() != 1 {
		t.Fatalf("live run: %+v", res.Run)
	}
	afterContent := ft.ServerContent("nb")
	tags, _ := afterContent["tags"].([]any)
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "new" {
		t.Fatalf("tags = %v", afterContent["tags"])
	}
	for k, v := range beforeContent {
		if k != "tags" && !reflect.DeepEqual(afterContent[k], v) {
			t.Errorf("content %s changed: %v -> %v", k, v, afterContent[k])
		}
	}
	afterMeta := ft.ServerMetaRaw("nb")
	for k, v := range beforeMeta {
		if k != "version" && afterMeta[k] != v {
			t.Errorf("metadata %s changed: %v -> %v", k, v, afterMeta[k])
		}
	}
	if afterMeta["version"].(float64) != beforeMeta["version"].(float64)+1 {
		t.Errorf("version %v -> %v", beforeMeta["version"], afterMeta["version"])
	}
	if !reflect.DeepEqual(ft.ServerContent("old")["tags"], []any{}) {
		t.Errorf("old paper tagged: %v", ft.ServerContent("old")["tags"])
	}

	// the log shows it, without undo
	lg, err := svc.Log(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(lg.Moves) == 0 || lg.Moves[0].Action != automation.ActionAddTag || lg.Moves[0].CanUndo || lg.Moves[0].Mode != EntryLive {
		t.Fatalf("log: %+v", lg.Moves[0])
	}
	if _, err := svc.Undo(uid, lg.Moves[0].ID); !IsNotQualified(err) {
		t.Errorf("undo of a tag change: %v", err)
	}

	// a second sweep finds nothing to do
	res, _ = svc.RunNow(uid)
	if res.Run.Tagged != 0 || len(res.Plan.Moves) != 0 {
		t.Fatalf("second run: %+v", res.Run)
	}

	// a day later the second rule takes the tag off again
	clk.advance(23 * time.Hour)
	res, _ = svc.RunNow(uid)
	if res.Run.Tagged != 1 || res.Run.Errors != 0 {
		t.Fatalf("remove run: %+v %v", res.Run, res.Run.ErrorDetails)
	}
	if tags, _ := ft.ServerContent("nb")["tags"].([]any); len(tags) != 0 {
		t.Fatalf("tags after removal = %v", tags)
	}
	// the tablet still reads every page after both writes
	if err := ft.Load(); err != nil {
		t.Fatal(err)
	}
	if pages := ft.Docs["nb"].Content["cPages"]; !reflect.DeepEqual(pages, beforeContent["cPages"]) {
		t.Errorf("cPages after tag writes = %v", pages)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
