package autoorganize

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// RuleIssue a rule one format cannot carry as is: a v2 rule with no v1 form
// (ToV1), or a v1 rule migrated disabled (MigrateV1, X3). Index is its config position.
type RuleIssue struct {
	Index  int
	RuleID string
	Reason string
}

// onlyFields the params of spec's Fields; the others are zero. A nil spec
// (an unknown type) keeps nothing.
func onlyFields(spec *automation.Spec, p automation.Params) automation.Params {
	var out automation.Params
	if spec == nil || len(spec.Fields) == 0 {
		return out
	}
	b, err := json.Marshal(p)
	if err != nil {
		return out
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return out
	}
	kept := map[string]json.RawMessage{}
	for _, f := range spec.Fields {
		kept[f] = all[f]
	}
	b, _ = json.Marshal(kept)
	_ = json.Unmarshal(b, &out)
	return out
}

func hasField(spec *automation.Spec, field string) bool {
	if spec == nil {
		return false
	}
	for _, f := range spec.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// copyV1 a deep copy, so Normalize does not touch the caller's config
func copyV1(v1 *Config) *Config {
	c := *v1
	c.Rules = make([]Rule, len(v1.Rules))
	for i, r := range v1.Rules {
		if cm := r.Destination.CreateIfMissing; cm != nil {
			cp := *cm
			r.Destination.CreateIfMissing = &cp
		}
		if r.Action.Tasks != nil {
			b := *r.Action.Tasks
			r.Action.Tasks = &b
		}
		r.Watch = append([]WatchRef(nil), r.Watch...)
		c.Rules[i] = r
	}
	return &c
}

func destToV2(d Destination) *automation.Destination {
	out := &automation.Destination{FolderRef: automation.FolderRef(d.FolderRef)}
	if d.CreateIfMissing != nil {
		cm := automation.CreateIfMissing(*d.CreateIfMissing)
		out.CreateIfMissing = &cm
	}
	return out
}

func destToV1(d *automation.Destination) Destination {
	out := Destination{FolderRef: FolderRef(d.FolderRef)}
	if d.CreateIfMissing != nil {
		cm := CreateIfMissing(*d.CreateIfMissing)
		out.CreateIfMissing = &cm
	}
	return out
}

// MigrateV1 maps a v1 config (auto-organize.json) to v2. It normalizes a copy
// with the v1 rules first, so a hand-edited file migrates as if saved once.
// Rule order and ids are kept; the revision goes up by one. It never runs v2
// Validate (X2): a v1 rule that matched nothing migrates to one that matches
// nothing. Only an action other than move or pipeline is an error.
//
// X3: v1 never normalized on load, and the move planner skipped an enabled
// move rule that failed v1 Validate as it was stored ("Invalid rule"). Such a
// rule migrates disabled, and is returned as a RuleIssue for the caller to
// log. Pipeline rules are not checked: the v1 page watch ran them unvalidated.
func MigrateV1(v1 *Config) (*automation.Config, []RuleIssue, error) {
	if v1 == nil {
		return nil, nil, errors.New("no v1 config to migrate")
	}
	c := copyV1(v1)
	invalid := map[int]string{}
	for i := range c.Rules {
		raw := c.Rules[i]
		if !raw.Enabled || raw.IsPipeline() {
			continue
		}
		if err := raw.Validate(); err != nil {
			invalid[i] = err.Error()
		}
	}
	c.Normalize()
	out := &automation.Config{
		Version:  automation.Version,
		Revision: c.Revision + 1,
		Settings: automation.Settings(c.Settings),
		Rules:    make([]automation.Rule, 0, len(c.Rules)),
	}
	var issues []RuleIssue
	for i := range c.Rules {
		r := &c.Rules[i]
		nr, err := ruleToV2(r)
		if err != nil {
			return nil, nil, fmt.Errorf("rule %d (%s): %w", i+1, r.ID, err)
		}
		if reason, ok := invalid[i]; ok {
			nr.Enabled = false
			issues = append(issues, RuleIssue{Index: i, RuleID: r.ID,
				Reason: "v1 skipped it as an invalid rule (" + reason + "); migrated disabled"})
		}
		out.Rules = append(out.Rules, nr)
	}
	return out, issues, nil
}

func ruleToV2(r *Rule) (automation.Rule, error) {
	nr := automation.Rule{ID: r.ID, Name: r.Name, Enabled: r.Enabled, Conditions: []automation.Condition{}}
	trig := automation.Trigger{Type: r.Trigger.Type, Params: onlyFields(automation.Triggers[r.Trigger.Type], r.Trigger.params())}
	if r.Scope != ScopeAll {
		nr.Conditions = append(nr.Conditions, automation.Condition{Type: automation.CondKindIs, Params: automation.Params{Kind: r.Scope}})
	}
	switch r.Action.Type {
	case ActionMove:
		nr.When = automation.When{
			Subject: automation.SubjectDocument,
			In:      []automation.Place{{ID: r.Source.ID, Kind: automation.PlaceFolder, Name: r.Source.Name}},
			Trigger: trig,
		}
		// P1: min age is a condition only for not_opened_for_days
		if r.Trigger.Type == TriggerNotOpened && r.Trigger.MinAgeEnabled {
			nr.Conditions = append(nr.Conditions, automation.Condition{Type: automation.CondMinAgeDays, Params: automation.Params{Days: r.Trigger.MinAgeDays}})
		}
		nr.Actions = []automation.Action{{Type: automation.ActionMove, To: destToV2(r.Destination)}}
	case ActionPipeline:
		in := make([]automation.Place, 0, len(r.Watch))
		for _, w := range r.Watch {
			in = append(in, automation.Place{ID: w.ID, Kind: w.Kind, Name: w.Name})
		}
		// the v1 page watch ran every pipeline trigger but page_tagged as page_changed
		if r.Trigger.Type != TriggerPageTagged {
			trig = automation.Trigger{Type: automation.TriggerPageChanged}
		}
		nr.When = automation.When{Subject: automation.SubjectPage, In: in, Trigger: trig}
		a := automation.Action{Type: automation.ActionExtractTasks, Prompt: r.Action.Prompt, Model: r.Action.Model}
		if !r.Action.ExtractsTasks() {
			a.Type = automation.ActionSaveNotes
		}
		nr.Actions = []automation.Action{a}
	default:
		return nr, fmt.Errorf("cannot migrate action %q", r.Action.Type)
	}
	return nr, nil
}

// ToV1 maps a v2 config back to v1 (rollback). A rule v1 cannot express is
// left out and reported as a RuleIssue; every rule in the result passes v1
// Validate. The revision is max(base, c)+1, so the old binary sees a newer
// file. base may be nil.
func ToV1(c *automation.Config, base *Config) (*Config, []RuleIssue) {
	rev := c.Revision
	if base != nil && base.Revision > rev {
		rev = base.Revision
	}
	out := &Config{Revision: rev + 1, Settings: Settings(c.Settings), Rules: []Rule{}}
	var issues []RuleIssue
	seen := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		v1r, reason := ruleToV1(r)
		if reason == "" && seen[r.ID] {
			reason = "duplicate rule id"
		}
		if reason == "" {
			if err := v1r.Validate(); err != nil {
				reason = "v1 rejects it: " + err.Error()
			}
		}
		if reason != "" {
			issues = append(issues, RuleIssue{Index: i, RuleID: r.ID, Reason: reason})
			continue
		}
		seen[r.ID] = true
		out.Rules = append(out.Rules, v1r)
	}
	return out, issues
}

// ruleToV1 the v1 form of r, or the reason it has none
func ruleToV1(r *automation.Rule) (Rule, string) {
	out := Rule{ID: r.ID, Name: r.Name, Enabled: r.Enabled, Scope: r.Kind()}
	if len(r.Actions) != 1 {
		return out, fmt.Sprintf("it has %d actions; a v1 rule has exactly one", len(r.Actions))
	}
	tr := &r.When.Trigger
	kinds := 0
	var minAge *automation.Condition
	for i := range r.Conditions {
		cond := &r.Conditions[i]
		switch {
		case cond.Type == automation.CondKindIs:
			kinds++
			if kinds > 1 {
				return out, "v1 has one scope, so at most one kind_is condition"
			}
		case cond.Type == automation.CondMinAgeDays && r.IsDocument():
			if tr.Type != TriggerNotOpened || minAge != nil {
				return out, "v1 holds min age only as one min_age_days condition with not_opened_for_days"
			}
			minAge = cond
		default:
			return out, fmt.Sprintf("v1 cannot express the %q condition on a %s rule", cond.Type, r.When.Subject)
		}
	}

	spec := automation.Triggers[tr.Type]
	t := Trigger{Type: tr.Type}
	t.setParams(onlyFields(spec, tr.Params))
	if !hasField(spec, "settleHours") {
		t.SettleHours = 6
	}
	if tr.Type != TriggerPinnedIs {
		t.Pinned = true
	}
	t.MinAgeEnabled = true
	if tr.Type == TriggerNotOpened {
		t.MinAgeEnabled = minAge != nil
		if minAge != nil {
			t.MinAgeDays = minAge.Days
		}
	}
	out.Trigger = t

	a := &r.Actions[0]
	switch {
	case r.IsDocument():
		if a.Type != automation.ActionMove {
			return out, fmt.Sprintf("a document rule with the %q action has no v1 form", a.Type)
		}
		if len(r.When.In) != 1 {
			return out, "a v1 move rule watches exactly one folder"
		}
		if a.To == nil {
			return out, "move needs a destination"
		}
		out.Source = FolderRef(r.Source())
		out.Destination = destToV1(a.To)
		out.Action = Action{Type: ActionMove}
	case r.IsPage():
		if a.Type != automation.ActionExtractTasks && a.Type != automation.ActionSaveNotes {
			return out, fmt.Sprintf("a page rule with the %q action has no v1 form", a.Type)
		}
		for _, p := range r.When.In {
			out.Watch = append(out.Watch, WatchRef{ID: p.ID, Kind: p.Kind, Name: p.Name})
		}
		out.Action = Action{Type: ActionPipeline, Prompt: a.Prompt, Model: a.Model}
		if a.Type == automation.ActionSaveNotes {
			f := false
			out.Action.Tasks = &f
		}
	default:
		return out, fmt.Sprintf("subject %q has no v1 form", r.When.Subject)
	}
	return out, ""
}
