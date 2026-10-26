package autoorganize

import (
	"errors"
	"sort"
	"time"
)

// triggerDef everything the package knows about one trigger type. Normalize,
// Validate, PageBased, the JSON defaults and the evaluator all read this table,
// so adding a trigger is one entry here (plus its evaluator and the UI's
// vocabulary in ui/src/pages/AutoOrganize/rules.js) — see doc.go.
type triggerDef struct {
	// PageBased needs a pageCount, which locks the rule to PDF & EPUB
	PageBased bool
	// AbsentMinAgeDays what minAgeDays means when the JSON leaves it out
	AbsentMinAgeDays int
	// Defaults fills parameters that are zero on save (before Validate)
	Defaults func(t *Trigger)
	// Validate the parameters; nil when there is nothing to check
	Validate func(t *Trigger) error
	// Eval the trigger against one document after the common guards passed
	Eval func(t *Trigger, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation
}

func settleNotNegative(t *Trigger) error {
	if t.SettleHours < 0 {
		return errors.New("settle hours cannot be negative")
	}
	return nil
}

func daysAtLeastOne(t *Trigger) error {
	if t.Days < 1 {
		return errors.New("days must be at least 1")
	}
	return nil
}

func defaultDays(n int) func(t *Trigger) {
	return func(t *Trigger) {
		if t.Days == 0 {
			t.Days = n
		}
	}
}

var triggerDefs = map[string]*triggerDef{
	TriggerFinishedReading: {
		PageBased: true,
		Defaults: func(t *Trigger) {
			if t.MinPages == 0 {
				t.MinPages = 3
			}
		},
		Validate: func(t *Trigger) error {
			if t.MinPages < 1 {
				return errors.New("minimum pages must be at least 1")
			}
			return settleNotNegative(t)
		},
		Eval: evalReading,
	},
	TriggerReadProgress: {
		PageBased: true,
		Defaults: func(t *Trigger) {
			if t.Percent == 0 {
				t.Percent = 80
			}
		},
		Validate: func(t *Trigger) error {
			if t.Percent < 1 || t.Percent > 100 {
				return errors.New("percent must be between 1 and 100")
			}
			return settleNotNegative(t)
		},
		Eval: evalReading,
	},
	TriggerNotOpened: {
		AbsentMinAgeDays: 7,
		Defaults:         defaultDays(30),
		Validate: func(t *Trigger) error {
			if err := daysAtLeastOne(t); err != nil {
				return err
			}
			if t.MinAgeEnabled && t.MinAgeDays < 0 {
				return errors.New("minimum age cannot be negative")
			}
			return nil
		},
		Eval: evalNotOpened,
	},
	TriggerNotModified: {
		Defaults: defaultDays(90),
		Validate: daysAtLeastOne,
		Eval:     evalNotModified,
	},
	TriggerNameMatches: {
		Validate: func(t *Trigger) error {
			_, err := CompilePattern(t.Pattern)
			return err
		},
		Eval: evalNameMatches,
	},
	TriggerTagIs: {
		Validate: func(t *Trigger) error {
			if NormalizeTag(t.Tag) == "" {
				return errors.New("tag cannot be empty")
			}
			if IsNoAutomationTag(t.Tag) {
				return errors.New("documents tagged “no automation” are always skipped; pick another tag")
			}
			return nil
		},
		Eval: evalTagIs,
	},
	TriggerAgeSinceCreated: {
		Defaults: defaultDays(14),
		Validate: daysAtLeastOne,
		Eval:     evalAgeSinceCreated,
	},
	TriggerPinnedIs: {
		Eval: evalPinnedIs,
	},
}

// TriggerTypes every known trigger type, sorted
func TriggerTypes() []string {
	out := make([]string, 0, len(triggerDefs))
	for k := range triggerDefs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
