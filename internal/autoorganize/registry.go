package autoorganize

import (
	"sort"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// evalFunc evaluates one trigger or condition against one document after the
// common guards passed (checkDoc). Parameters, defaults and validation live in
// the automation catalogue; the evaluators live here, keyed by the same type.
type evalFunc func(p *automation.Params, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation

// evaluators the trigger evaluators, one per automation.Triggers entry
var evaluators = map[string]evalFunc{
	TriggerFinishedReading: evalFinished,
	TriggerReadProgress:    evalProgress,
	TriggerNotOpened:       evalNotOpened,
	TriggerNotModified:     evalNotModified,
	TriggerNameMatches:     evalNameMatches,
	TriggerTagIs:           evalTagIsTrigger,
	TriggerAgeSinceCreated: evalAgeSinceCreated,
	TriggerPinnedIs:        evalPinnedIs,
	TriggerCreatedTime:     evalCreatedTime,
	TriggerModifiedTime:    evalModifiedTime,
	// page_changed and page_tagged are evaluated by internal/pagewatch, never
	// by the move planner
	TriggerPageChanged: evalNotAMoveTrigger(TriggerPageChanged),
	TriggerPageTagged:  evalNotAMoveTrigger(TriggerPageTagged),
}

// conditionEvaluators the evaluators of the document conditions. kind_is has
// none: checkDoc runs it (evalKind) before the trigger. A condition named like
// a trigger shares the trigger's evaluator.
var conditionEvaluators = map[string]evalFunc{
	automation.CondTagIs:        evalTagIs,
	automation.CondTagIsNot:     evalTagIsNot,
	automation.CondNotOpened:    evalNotOpened,
	automation.CondMinAgeDays:   evalMinAge,
	automation.CondNameMatches:  evalNameMatches,
	automation.CondPinnedIs:     evalPinnedIs,
	automation.CondCreatedTime:  evalCreatedTime,
	automation.CondModifiedTime: evalModifiedTime,
}

// params the catalogue parameters of a v1 trigger (migration)
func (t *V1Trigger) params() automation.Params {
	return automation.Params{MinPages: t.MinPages, SettleHours: t.SettleHours, Percent: t.Percent,
		Days: t.Days, Pattern: t.Pattern, Tag: t.Tag, Pinned: t.Pinned}
}

// setParams writes catalogue parameters back to a v1 trigger
func (t *V1Trigger) setParams(p automation.Params) {
	t.MinPages, t.SettleHours, t.Percent, t.Days = p.MinPages, p.SettleHours, p.Percent, p.Days
	t.Pattern, t.Tag, t.Pinned = p.Pattern, p.Tag, p.Pinned
}

// isPageTrigger the trigger belongs to the page watch (v1 pipeline rules)
func isPageTrigger(s *automation.Spec) bool {
	for _, sub := range s.Subjects {
		if sub == automation.SubjectDocument {
			return false
		}
	}
	return true
}

// TriggerTypes every known trigger type, sorted
func TriggerTypes() []string {
	out := make([]string, 0, len(automation.Triggers))
	for k := range automation.Triggers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
