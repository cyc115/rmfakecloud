// Package automation the automation rule vocabulary shared by the document
// sweep (internal/autoorganize) and the page watch (internal/pagewatch): the
// trigger and condition catalogue (parameters, subjects, defaults,
// validation) and the tag and name-pattern helpers. It is a leaf package; the
// evaluators live with the engines that run them.
package automation

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Subjects: what a trigger or condition looks at
const (
	SubjectDocument = "document"
	SubjectPage     = "page"
)

// Trigger types (the v1 strings)
const (
	TriggerFinishedReading = "finished_reading"
	TriggerNotOpened       = "not_opened_for_days"
	TriggerNotModified     = "not_modified_for_days"
	TriggerNameMatches     = "name_matches"
	TriggerTagIs           = "tag_is"
	TriggerReadProgress    = "read_progress"
	TriggerAgeSinceCreated = "age_since_created"
	TriggerPinnedIs        = "pinned_is"
	// TriggerPageChanged pages that changed since the last look (page watch)
	TriggerPageChanged = "page_changed"
	// TriggerPageTagged pages carrying Params.Tag (page watch)
	TriggerPageTagged = "page_tagged"
	// TriggerCreatedTime created in the last / more than Amount Unit ago
	TriggerCreatedTime = "created_time"
	// TriggerModifiedTime changed in the last / more than Amount Unit ago
	TriggerModifiedTime = "modified_time"
)

// Condition types
const (
	CondKindIs       = "kind_is"
	CondTagIs        = "tag_is"
	CondTagIsNot     = "tag_is_not"
	CondNotOpened    = "not_opened_for_days"
	CondMinAgeDays   = "min_age_days"
	CondNameMatches  = "name_matches"
	CondPinnedIs     = "pinned_is"
	CondPageTagIs    = "page_tag_is"
	CondCreatedTime  = "created_time"
	CondModifiedTime = "modified_time"
)

// Time comparisons of created_time and modified_time
const (
	// CompareWithin in the last Amount Unit
	CompareWithin = "within"
	// CompareOlder more than Amount Unit ago
	CompareOlder = "older"
)

// Time units of created_time and modified_time
const (
	UnitHours = "hours"
	UnitDays  = "days"
	UnitWeeks = "weeks"
)

// unitDurations the length of one Unit
var unitDurations = map[string]time.Duration{
	UnitHours: time.Hour,
	UnitDays:  24 * time.Hour,
	UnitWeeks: 7 * 24 * time.Hour,
}

// Kinds, read from the .content fileType
const (
	KindNotebooks = "notebooks"
	KindPdfEpub   = "pdf_epub"
	KindAll       = "all"
)

// Params the parameters of a trigger or condition; Spec.Fields names the ones
// a type uses (by JSON tag)
type Params struct {
	MinPages    int    `json:"minPages"`
	SettleHours int    `json:"settleHours"`
	Percent     int    `json:"percent"`
	Days        int    `json:"days"`
	Pattern     string `json:"pattern"`
	Tag         string `json:"tag"`
	Kind        string `json:"kind"`
	Pinned      bool   `json:"pinned"`
	// Compare, Amount and Unit: created_time and modified_time
	Compare string `json:"compare"`
	Amount  int    `json:"amount"`
	Unit    string `json:"unit"`
}

// Window Amount Unit as a duration (created_time, modified_time); 0 for an
// unknown unit
func (p *Params) Window() time.Duration {
	return time.Duration(p.Amount) * unitDurations[p.Unit]
}

// Spec everything known about one trigger or condition type
type Spec struct {
	// Subjects what it applies to: SubjectDocument and/or SubjectPage
	Subjects []string
	// Fields the Params JSON tags it uses
	Fields []string
	// PageBased needs a pageCount, which locks the rule to PDF & EPUB
	PageBased bool
	// BeforeTrigger evaluated before the trigger, as a guard (kind_is)
	BeforeTrigger bool
	// Repeatable a rule may carry it more than once
	Repeatable bool
	// Defaults fills parameters that are zero on save (before Validate); nil = none
	Defaults func(*Params)
	// Validate the parameters; nil when there is nothing to check
	Validate func(*Params) error
}

func settleNotNegative(p *Params) error {
	if p.SettleHours < 0 {
		return errors.New("settle hours cannot be negative")
	}
	return nil
}

func daysAtLeastOne(p *Params) error {
	if p.Days < 1 {
		return errors.New("days must be at least 1")
	}
	return nil
}

func defaultDays(n int) func(*Params) {
	return func(p *Params) {
		if p.Days == 0 {
			p.Days = n
		}
	}
}

func validPattern(p *Params) error {
	_, err := CompilePattern(p.Pattern)
	return err
}

func validDocTag(p *Params) error {
	if NormalizeTag(p.Tag) == "" {
		return errors.New("tag cannot be empty")
	}
	if IsNoAutomationTag(p.Tag) {
		return errors.New("documents tagged “no automation” are always skipped; pick another tag")
	}
	return nil
}

func trimTag(p *Params) {
	p.Tag = strings.TrimSpace(p.Tag)
}

func validPageTag(p *Params) error {
	if NormalizePageTag(p.Tag) == "" {
		return errors.New("tag cannot be empty")
	}
	return nil
}

// timeFields the parameters of created_time and modified_time
var timeFields = []string{"compare", "amount", "unit"}

// defaultTime in the last 24 hours
func defaultTime(p *Params) {
	if p.Compare == "" {
		p.Compare = CompareWithin
	}
	if p.Amount == 0 {
		p.Amount = 24
	}
	if p.Unit == "" {
		p.Unit = UnitHours
	}
}

func validTime(p *Params) error {
	if p.Compare != CompareWithin && p.Compare != CompareOlder {
		return fmt.Errorf("compare must be %s or %s", CompareWithin, CompareOlder)
	}
	if p.Amount < 1 {
		return errors.New("the amount of time must be at least 1")
	}
	if _, ok := unitDurations[p.Unit]; !ok {
		return fmt.Errorf("unit must be %s, %s or %s", UnitHours, UnitDays, UnitWeeks)
	}
	return nil
}

// Triggers the trigger catalogue, by type
var Triggers = map[string]*Spec{
	TriggerFinishedReading: {
		Subjects:  []string{SubjectDocument},
		Fields:    []string{"minPages", "settleHours"},
		PageBased: true,
		Defaults: func(p *Params) {
			if p.MinPages == 0 {
				p.MinPages = 3
			}
		},
		Validate: func(p *Params) error {
			if p.MinPages < 1 {
				return errors.New("minimum pages must be at least 1")
			}
			return settleNotNegative(p)
		},
	},
	TriggerReadProgress: {
		Subjects:  []string{SubjectDocument},
		Fields:    []string{"percent", "settleHours"},
		PageBased: true,
		Defaults: func(p *Params) {
			if p.Percent == 0 {
				p.Percent = 80
			}
		},
		Validate: func(p *Params) error {
			if p.Percent < 1 || p.Percent > 100 {
				return errors.New("percent must be between 1 and 100")
			}
			return settleNotNegative(p)
		},
	},
	TriggerNotOpened: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"days"},
		Defaults: defaultDays(30),
		Validate: daysAtLeastOne,
	},
	TriggerNotModified: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"days"},
		Defaults: defaultDays(90),
		Validate: daysAtLeastOne,
	},
	TriggerNameMatches: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"pattern"},
		Validate: validPattern,
	},
	TriggerTagIs: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"tag"},
		Validate: validDocTag,
	},
	TriggerAgeSinceCreated: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"days"},
		Defaults: defaultDays(14),
		Validate: daysAtLeastOne,
	},
	TriggerPinnedIs: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"pinned"},
	},
	TriggerPageChanged: {
		Subjects: []string{SubjectPage},
		Fields:   []string{},
	},
	TriggerPageTagged: {
		Subjects: []string{SubjectPage},
		Fields:   []string{"tag"},
		Defaults: trimTag,
		Validate: validPageTag,
	},
	TriggerCreatedTime: {
		Subjects: []string{SubjectDocument},
		Fields:   timeFields,
		Defaults: defaultTime,
		Validate: validTime,
	},
	TriggerModifiedTime: {
		Subjects: []string{SubjectDocument},
		Fields:   timeFields,
		Defaults: defaultTime,
		Validate: validTime,
	},
}

// Conditions the condition catalogue, by type. A condition that shares a
// trigger's name shares its parameters, defaults and validation.
var Conditions = map[string]*Spec{
	CondKindIs: {
		Subjects:      []string{SubjectDocument, SubjectPage},
		Fields:        []string{"kind"},
		BeforeTrigger: true,
		Validate: func(p *Params) error {
			switch p.Kind {
			case KindNotebooks, KindPdfEpub:
				return nil
			}
			return fmt.Errorf("unknown kind %q", p.Kind)
		},
	},
	CondTagIs: {
		Subjects:   []string{SubjectDocument, SubjectPage},
		Fields:     []string{"tag"},
		Repeatable: true,
		Validate:   validDocTag,
	},
	CondTagIsNot: {
		Subjects:   []string{SubjectDocument, SubjectPage},
		Fields:     []string{"tag"},
		Repeatable: true,
		Validate:   validDocTag,
	},
	CondNotOpened: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"days"},
		Defaults: defaultDays(30),
		Validate: daysAtLeastOne,
	},
	// CondMinAgeDays has no default: 0 ("at least 0 days old") is meaningful
	CondMinAgeDays: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"days"},
		Validate: func(p *Params) error {
			if p.Days < 0 {
				return errors.New("minimum age cannot be negative")
			}
			return nil
		},
	},
	CondNameMatches: {
		Subjects: []string{SubjectDocument, SubjectPage},
		Fields:   []string{"pattern"},
		Validate: validPattern,
	},
	CondPinnedIs: {
		Subjects: []string{SubjectDocument},
		Fields:   []string{"pinned"},
	},
	CondPageTagIs: {
		Subjects:   []string{SubjectPage},
		Fields:     []string{"tag"},
		Repeatable: true,
		Defaults:   trimTag,
		Validate:   validPageTag,
	},
	// the document's times; on a page rule, the times of the page's document.
	// Repeatable, so a rule can hold a window (older than 1 day AND within 7).
	CondCreatedTime: {
		Subjects:   []string{SubjectDocument, SubjectPage},
		Fields:     timeFields,
		Repeatable: true,
		Defaults:   defaultTime,
		Validate:   validTime,
	},
	CondModifiedTime: {
		Subjects:   []string{SubjectDocument, SubjectPage},
		Fields:     timeFields,
		Repeatable: true,
		Defaults:   defaultTime,
		Validate:   validTime,
	},
}
