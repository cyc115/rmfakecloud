package autoorganize

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRegistryCoversEveryTriggerType(t *testing.T) {
	want := []string{TriggerAgeSinceCreated, TriggerFinishedReading, TriggerNameMatches, TriggerNotModified,
		TriggerNotOpened, TriggerPinnedIs, TriggerReadProgress, TriggerTagIs}
	got := TriggerTypes()
	if len(got) != len(want) {
		t.Fatalf("TriggerTypes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TriggerTypes = %v, want %v", got, want)
		}
	}
	for typ, def := range triggerDefs {
		if def.Eval == nil {
			t.Errorf("%s has no evaluator", typ)
		}
	}
	pageBased := map[string]bool{}
	for typ, def := range triggerDefs {
		if def.PageBased {
			pageBased[typ] = true
		}
	}
	if len(pageBased) != 2 || !pageBased[TriggerFinishedReading] || !pageBased[TriggerReadProgress] {
		t.Errorf("page-based triggers = %v", pageBased)
	}
}

func TestRegistryDefaultsAndJSONAbsence(t *testing.T) {
	cfg := &Config{Rules: []Rule{
		{Trigger: Trigger{Type: TriggerNotOpened}},
		{Trigger: Trigger{Type: TriggerFinishedReading}},
		{Trigger: Trigger{Type: TriggerReadProgress}},
		{Trigger: Trigger{Type: TriggerNotModified}},
		{Trigger: Trigger{Type: TriggerAgeSinceCreated}},
		{Trigger: Trigger{Type: TriggerPinnedIs}},
	}}
	cfg.Normalize()
	if cfg.Rules[0].Trigger.Days != 30 || cfg.Rules[1].Trigger.MinPages != 3 || cfg.Rules[2].Trigger.Percent != 80 ||
		cfg.Rules[3].Trigger.Days != 90 || cfg.Rules[4].Trigger.Days != 14 {
		t.Errorf("defaults not applied: %+v", cfg.Rules)
	}
	var notOpened, created Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":3}`), &notOpened); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"age_since_created","days":3}`), &created); err != nil {
		t.Fatal(err)
	}
	if notOpened.MinAgeDays != 7 || created.MinAgeDays != 0 {
		t.Errorf("absent minAgeDays: not_opened=%d age_since_created=%d", notOpened.MinAgeDays, created.MinAgeDays)
	}
	var explicit Trigger
	if err := json.Unmarshal([]byte(`{"type":"not_opened_for_days","days":3,"minAgeDays":0}`), &explicit); err != nil {
		t.Fatal(err)
	}
	if explicit.MinAgeDays != 0 {
		t.Errorf("an explicit minAgeDays of 0 must be kept, got %d", explicit.MinAgeDays)
	}
}

func TestUnknownTriggerIsRejectedAndSkipped(t *testing.T) {
	r := Rule{Scope: ScopeAll, Action: Action{Type: ActionMove}, Source: FolderRef{ID: "src"},
		Destination: Destination{FolderRef: FolderRef{ID: "dst"}}, Trigger: Trigger{Type: "has_coffee_stains"}}
	if err := r.Validate(); err == nil || err.Error() != `unknown trigger "has_coffee_stains"` {
		t.Errorf("Validate = %v", err)
	}
	ev := evalTrigger(&r, &DocSnap{}, &ContentInfo{}, nil, time.Now())
	if ev.skip != SkipInvalidRule {
		t.Errorf("evalTrigger skip = %q", ev.skip)
	}
}
