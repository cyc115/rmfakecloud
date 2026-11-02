package pagewatch

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
)

// update rewrites the golden files in testdata instead of comparing
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// Ids of the golden scenario. The rule, folder and watched-doc ids are the
// fixture's (../autoorganize/testdata/v1-golden.json); all are synthetic.
const (
	gRuleMove   = "0a1b2c3d-0000-4000-8000-000000000001"
	gRulePage   = "0a1b2c3d-0000-4000-8000-000000000002"
	gReading    = "0a1b2c3d-0000-4000-8000-0000000000f1"
	gJournal    = "0a1b2c3d-0000-4000-8000-0000000000d1"
	gIdeas      = "0a1b2c3d-0000-4000-8000-0000000000d2"
	gFinished   = "0a1b2c3d-0000-4000-8000-0000000000a1"
	gShort      = "0a1b2c3d-0000-4000-8000-0000000000a2"
	gFresh      = "0a1b2c3d-0000-4000-8000-0000000000a3"
	gScratch    = "0a1b2c3d-0000-4000-8000-0000000000a4"
	gOptedOut   = "0a1b2c3d-0000-4000-8000-0000000000a5"
	gOther      = "0a1b2c3d-0000-4000-8000-0000000000e1"
	gDoneFolder = "0a1b2c3d-0000-4000-8000-0000000000c1"
)

// goldenNow the fixed clock of the golden scenario (newEnv's start)
var goldenNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// goldenConfig the v1 fixture as the server loads it (Store.LoadConfig: a
// plain unmarshal of the saved, already normalised file)
func goldenConfig(t *testing.T) *autoorganize.Config {
	t.Helper()
	b, err := os.ReadFile("../autoorganize/testdata/v1-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &autoorganize.Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	return cfg
}

// fixed pins a doc's metadata times to the golden clock (the testkit uses
// the wall clock), so blobs and plans are byte-stable across runs
func fixed(d *tablet.Doc) *tablet.Doc {
	at := tablet.Ms(goldenNow.Add(-30 * 24 * time.Hour))
	d.Meta["createdTime"] = at
	d.Meta["lastModified"] = at
	return d
}

// goldenTree the fixed scenario on the testkit tablet, with goldenConfig:
//
//	Reading List (f1): Finished (moves, Done to be created), Short (1 page),
//	  Fresh (not settled), Scratch (notebook: out of scope), Opted out
//	  (tagged no automation)
//	root: Journal d1 (p1, p2), Ideas d2 (p1), both watched; Other (not watched)
func goldenTree(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	if !e.now.Equal(goldenNow) {
		t.Fatalf("env clock %v, golden clock %v", e.now, goldenNow)
	}
	e.cfg = goldenConfig(t)
	now := goldenNow
	fixed(e.ft.Folder(gReading, "Reading List", autoorganize.RootID))
	fixed(e.ft.PDF(gFinished, "Finished", gReading, 3, 2, now.Add(-7*time.Hour)))
	fixed(e.ft.PDF(gShort, "Short", gReading, 1, 0, now.Add(-7*time.Hour)))
	fixed(e.ft.PDF(gFresh, "Fresh", gReading, 3, 2, now.Add(-1*time.Hour)))
	fixed(e.ft.Notebook(gScratch, "Scratch", gReading))
	opt := fixed(e.ft.PDF(gOptedOut, "Opted out", gReading, 3, 2, now.Add(-7*time.Hour)))
	opt.Content["tags"] = []any{map[string]any{"name": autoorganize.NoAutomationTag, "timestamp": now.Add(-48 * time.Hour).UnixMilli()}}

	fixed(e.ft.Notebook(gJournal, "Journal", autoorganize.RootID))
	e.page(gJournal, "p1", "strokes_a.rm")
	e.page(gJournal, "p2", "text_note.rm")
	fixed(e.ft.Notebook(gIdeas, "Ideas", autoorganize.RootID))
	e.page(gIdeas, "p1", "text_todo.rm")
	fixed(e.ft.Notebook(gOther, "Other", autoorganize.RootID))
	e.page(gOther, "p1", "strokes_a.rm")
	return e
}

// pageWatchStep the tablet change before page watch sweep step (1-5)
func pageWatchStep(e *env, step int) {
	e.t.Helper()
	switch step {
	case 2:
		e.page(gJournal, "p1", "strokes_ab.rm")
		e.page(gIdeas, "p2", "text_note.rm")
	case 3:
		if err := e.ft.DeletePage(gJournal, "p2"); err != nil {
			e.t.Fatal(err)
		}
	case 5:
		e.page(gIdeas, "p1", "strokes_ab.rm")
	}
}

// goldenFile compares v's indented JSON with testdata/name (-update writes
// it) and returns the pinned bytes
func goldenFile(t *testing.T, name string, v any) []byte {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return got
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Errorf("%s changed at line %d:\n got %s\nwant %s", path, i+1, g, w)
				break
			}
		}
	}
	return want
}

type goldenSweep struct {
	Pipeline []Event `json:"pipeline"`
	State    *State  `json:"state"`
}

type enginesGolden struct {
	Plan1  *autoorganize.Plan `json:"plan1"`
	Plan2  *autoorganize.Plan `json:"plan2"`
	Sweeps []goldenSweep      `json:"sweeps"`
}

// TestEnginesGolden pins what both engines produce today for the v1 fixture:
// the move planner (a fresh sweep, then a held one) and five page watch
// sweeps. Rules v2 must keep every byte (never miss a note); the testdata
// files are never edited by hand.
func TestEnginesGolden(t *testing.T) {
	e := goldenTree(t)
	now := goldenNow
	snap, content := e.snapshot()
	p1 := autoorganize.BuildPlan(autoorganize.PlanInput{Snap: snap, Config: e.cfg, State: autoorganize.NewState(), Content: content, Now: now})
	if len(p1.Moves) != 1 || p1.Moves[0].DocID != gFinished || p1.Moves[0].CreateFolder == nil || p1.Moves[0].To != "" {
		t.Fatalf("plan1 moves = %+v, want Finished into Done (to be created)", p1.Moves)
	}

	// the first live move created Done; the user then undid the move
	fixed(e.ft.Folder(gDoneFolder, "Done", gReading))
	st := autoorganize.NewState()
	st.CreatedFolders[gRuleMove] = gDoneFolder
	st.Holds = []*autoorganize.Hold{{RuleID: gRuleMove, DocID: gFinished, Inputs: p1.Moves[0].Inputs, Source: gReading,
		MovedTo: gDoneFolder, CreatedAt: now.Add(-time.Hour).UnixMilli(), Reason: "undo"}}
	pinned := autoorganize.NewState()
	if err := json.Unmarshal(goldenFile(t, "sweep-state-v1.json", st), pinned); err != nil {
		t.Fatal(err)
	}
	snap, content = e.snapshot()
	p2 := autoorganize.BuildPlan(autoorganize.PlanInput{Snap: snap, Config: e.cfg, State: pinned, Content: content, Now: now})
	held := false
	for _, s := range p2.Skipped {
		if s.DocID == gFinished && s.Reason == autoorganize.SkipHeld {
			held = true
		}
	}
	if !held || len(p2.Moves) != 0 {
		t.Fatalf("plan2: Finished not held: moves %+v, skipped %+v", p2.Moves, p2.Skipped)
	}

	want := [][]string{
		{"transcribe_only " + gJournal + "/p1 " + gRulePage, "transcribe_only " + gJournal + "/p2 " + gRulePage,
			"transcribe_only " + gIdeas + "/p1 " + gRulePage},
		{"edit " + gJournal + "/p1 " + gRulePage, "edit " + gIdeas + "/p2 " + gRulePage},
		{"page_deleted " + gJournal + "/p2 " + gRulePage},
		{},
		{"edit " + gIdeas + "/p1 " + gRulePage},
	}
	var sweeps []goldenSweep
	for step := 1; step <= 5; step++ {
		pageWatchStep(e, step)
		p := e.sweep()
		wantEvents(t, fmt.Sprintf("sweep %d", step), p.Pipeline, want[step-1]...)
		sweeps = append(sweeps, goldenSweep{Pipeline: p.Pipeline, State: e.state})
		if step == 3 {
			// sweep 4 reads the pinned v1 state, as after a restart
			e.state = NewState()
			if err := json.Unmarshal(goldenFile(t, "pagewatch-state-v1.json", sweeps[2].State), e.state); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(sweeps[3].Pipeline) != 0 {
		t.Fatalf("sweep 4 (no change) pipeline = %+v", sweeps[3].Pipeline)
	}
	if s5 := sweeps[4].Pipeline; len(s5) != 1 || s5[0].Kind != KindEdit {
		t.Fatalf("sweep 5 pipeline = %+v, want exactly one edit", s5)
	}
	goldenFile(t, "engines-golden.json", enginesGolden{Plan1: p1, Plan2: p2, Sweeps: sweeps})
}

// TestPageRuleIgnoresTriggerLeftovers (R-B3): a page rule carries the move
// fields (settleHours, minAge, pinned, source, destination) from the v1 form;
// the page watch never reads them, so v2 may drop them.
func TestPageRuleIgnoresTriggerLeftovers(t *testing.T) {
	e := goldenTree(t)
	a, b := goldenConfig(t), goldenConfig(t)
	r := &b.Rules[1]
	if r.ID != gRulePage {
		t.Fatalf("rule 2 is %s", r.ID)
	}
	r.Trigger.SettleHours, r.Trigger.MinAgeEnabled, r.Trigger.MinAgeDays, r.Trigger.Pinned = 0, false, 99, false
	r.Source = autoorganize.FolderRef{ID: "x"}
	r.Destination = autoorganize.Destination{FolderRef: autoorganize.FolderRef{ID: "y"}}
	if reflect.DeepEqual(a, b) {
		t.Fatal("configs do not differ")
	}
	stA, stB := NewState(), NewState()
	for step := 1; step <= 3; step++ {
		pageWatchStep(e, step)
		e.now = e.now.Add(time.Minute)
		snap, content := e.snapshot()
		in := e.input(snap, content)
		in.Config, in.State = a, stA
		pa := Detect(in)
		in.Config, in.State = b, stB
		pb := Detect(in)
		if len(pa.Pipeline) == 0 {
			t.Fatalf("sweep %d: no pipeline events to compare", step)
		}
		if !reflect.DeepEqual(pa.Feed, pb.Feed) || !reflect.DeepEqual(pa.Pipeline, pb.Pipeline) {
			t.Fatalf("sweep %d events differ:\n%+v\n%+v", step, pa.Pipeline, pb.Pipeline)
		}
		stA, stB = pa.Advance(all(pa)), pb.Advance(all(pb))
		if !reflect.DeepEqual(stA, stB) {
			t.Fatalf("sweep %d states differ:\n%s\n%s", step, stateJSON(t, stA), stateJSON(t, stB))
		}
	}
}
