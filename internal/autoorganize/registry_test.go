package autoorganize

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

func TestRegistryCoversEveryTriggerType(t *testing.T) {
	want := []string{TriggerAgeSinceCreated, TriggerCreatedTime, TriggerFinishedReading, TriggerModifiedTime, TriggerNameMatches,
		TriggerNotModified, TriggerNotOpened, TriggerPageChanged, TriggerPageTagged, TriggerPinnedIs, TriggerReadProgress, TriggerTagIs}
	got := TriggerTypes()
	if len(got) != len(want) {
		t.Fatalf("TriggerTypes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TriggerTypes = %v, want %v", got, want)
		}
	}
	for _, typ := range got {
		if evaluators[typ] == nil {
			t.Errorf("%s has no evaluator", typ)
		}
	}
	pageBased := map[string]bool{}
	for typ, def := range automation.Triggers {
		if def.PageBased {
			pageBased[typ] = true
		}
	}
	if len(pageBased) != 2 || !pageBased[TriggerFinishedReading] || !pageBased[TriggerReadProgress] {
		t.Errorf("page-based triggers = %v", pageBased)
	}
}

func TestRegistryDefaultsAndJSONAbsence(t *testing.T) {
	cfg := &V1Config{Rules: []V1Rule{
		{Trigger: V1Trigger{Type: TriggerNotOpened}},
		{Trigger: V1Trigger{Type: TriggerFinishedReading}},
		{Trigger: V1Trigger{Type: TriggerReadProgress}},
		{Trigger: V1Trigger{Type: TriggerNotModified}},
		{Trigger: V1Trigger{Type: TriggerAgeSinceCreated}},
		{Trigger: V1Trigger{Type: TriggerPinnedIs}},
	}}
	cfg.Normalize()
	if cfg.Rules[0].Trigger.Days != 30 || cfg.Rules[1].Trigger.MinPages != 3 || cfg.Rules[2].Trigger.Percent != 80 ||
		cfg.Rules[3].Trigger.Days != 90 || cfg.Rules[4].Trigger.Days != 14 {
		t.Errorf("defaults not applied: %+v", cfg.Rules)
	}
	var notOpened, created V1Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":3}`), &notOpened); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"age_since_created","days":3}`), &created); err != nil {
		t.Fatal(err)
	}
	if notOpened.MinAgeDays != 7 || created.MinAgeDays != 0 {
		t.Errorf("absent minAgeDays: not_opened=%d age_since_created=%d", notOpened.MinAgeDays, created.MinAgeDays)
	}
	var explicit V1Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":3,"minAgeDays":0}`), &explicit); err != nil {
		t.Fatal(err)
	}
	if explicit.MinAgeDays != 0 {
		t.Errorf("an explicit minAgeDays of 0 must be kept, got %d", explicit.MinAgeDays)
	}
}

func TestUnknownTriggerIsRejectedAndSkipped(t *testing.T) {
	r := moveRule("r", "src", "dst", trig("has_coffee_stains", P{}))
	if err := r.Validate(); err == nil || err.Error() != `unknown trigger "has_coffee_stains"` {
		t.Errorf("Validate = %v", err)
	}
	v1 := V1Rule{Scope: ScopeAll, Action: V1Action{Type: ActionMove}, Source: FolderRef{ID: "src"},
		Destination: Destination{FolderRef: FolderRef{ID: "dst"}}, Trigger: V1Trigger{Type: "has_coffee_stains"}}
	if err := v1.Validate(); err == nil || err.Error() != `unknown trigger "has_coffee_stains"` {
		t.Errorf("v1 Validate = %v", err)
	}
	ev := evalRule(&r, &DocSnap{}, &ContentInfo{}, nil, time.Now())
	if ev.skip != SkipInvalidRule {
		t.Errorf("evalRule skip = %q", ev.skip)
	}
}

func TestPageChangedEvalSkipsNeverMatches(t *testing.T) {
	r := moveRule("r", "src", "dst", trig(TriggerPageChanged, P{}))
	ev := evalRule(&r, &DocSnap{}, &ContentInfo{}, nil, time.Now())
	if ev.match || ev.skip == "" {
		t.Errorf("page_changed under the move planner must skip, got %+v", ev)
	}
}
