// The v1 rule model (auto-organize.json: V1Config, V1Rule, V1Trigger,
// V1Action). The engines run the v2 model (internal/automation); the v1 types
// are kept for the migration on load (MigrateV1) and for rollback (ToV1).

package autoorganize

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/google/uuid"
)

// Modes
const (
	ModeLive = automation.ModeLive
	ModeDry  = automation.ModeDry
)

// Scopes, read from the .content fileType (the automation kinds)
const (
	ScopePdfEpub   = automation.KindPdfEpub
	ScopeNotebooks = automation.KindNotebooks
	ScopeAll       = automation.KindAll
)

// v1 triggers (the automation trigger types)
const (
	TriggerFinishedReading = automation.TriggerFinishedReading
	TriggerNotOpened       = automation.TriggerNotOpened
	TriggerNotModified     = automation.TriggerNotModified
	TriggerNameMatches     = automation.TriggerNameMatches
	TriggerTagIs           = automation.TriggerTagIs
	TriggerReadProgress    = automation.TriggerReadProgress
	TriggerAgeSinceCreated = automation.TriggerAgeSinceCreated
	TriggerPinnedIs        = automation.TriggerPinnedIs
	TriggerCreatedTime     = automation.TriggerCreatedTime
	TriggerModifiedTime    = automation.TriggerModifiedTime
	// TriggerPageChanged belongs to pipeline rules only; the move planner never evaluates it
	TriggerPageChanged = automation.TriggerPageChanged
	// TriggerPageTagged belongs to pipeline rules only: pages carrying Trigger.Tag
	TriggerPageTagged = automation.TriggerPageTagged
)

// absentMinAgeDays what a v1 minAgeDays means when the JSON leaves it out,
// per trigger type (0 for the rest)
var absentMinAgeDays = map[string]int{TriggerNotOpened: 7}

// Actions
const (
	// ActionMove moves a matching document to a folder (the sweeper)
	ActionMove = "move"
	// ActionPipeline sends changed pages of watched notebooks to rm-pages (internal/pagewatch)
	ActionPipeline = "pipeline"
)

// Watch kinds
const (
	WatchFolder   = "folder"
	WatchDocument = "document"
)

// DefaultPipelineModel the OpenRouter model a pipeline rule uses when it names none
const DefaultPipelineModel = automation.DefaultPipelineModel

// DefaultPipelinePrompt the extraction intent a pipeline rule carries to rm-pages
// when it names none (automation.DefaultPipelinePrompt)
const DefaultPipelinePrompt = automation.DefaultPipelinePrompt

// ParentSource createIfMissing parent meaning "the rule's source folder"
const ParentSource = automation.ParentSource

// RootID the id of the root folder ("My files"); documents at the root have an empty parent
const RootID = automation.RootID

// trashID the literal parent of trashed items
const trashID = "trash"

// AllowedIntervals sweep interval choices, minutes
var AllowedIntervals = automation.AllowedIntervals

// DefaultInterval minutes
const DefaultInterval = automation.DefaultInterval

// Settings the per-user sweeper settings (the v1 and v2 files share the shape)
type Settings = automation.Settings

// FolderRef a folder picked from the tree, by UUID only. Name is a display cache.
type FolderRef = automation.FolderRef

// CreateIfMissing names a subfolder to create under Parent ("source" or a folder UUID)
type CreateIfMissing = automation.CreateIfMissing

// Destination of a move
type Destination = automation.Destination

// V1Trigger parameters; only the fields of Type are used
type V1Trigger struct {
	Type string `json:"type"`
	// finished_reading
	MinPages int `json:"minPages,omitempty"`
	// finished_reading, read_progress
	SettleHours int `json:"settleHours"`
	// read_progress
	Percent int `json:"percent,omitempty"`
	// not_opened_for_days, not_modified_for_days, age_since_created
	Days int `json:"days,omitempty"`
	// not_opened_for_days: "and is at least N days old"
	MinAgeEnabled bool `json:"minAgeEnabled"`
	MinAgeDays    int  `json:"minAgeDays"`
	// name_matches: glob, or /regex/
	Pattern string `json:"pattern,omitempty"`
	// tag_is (a document tag), page_tagged (a page tag)
	Tag string `json:"tag,omitempty"`
	// pinned_is
	Pinned bool `json:"pinned"`
}

// UnmarshalJSON fills defaults for parameters that are absent but where zero is meaningful
func (t *V1Trigger) UnmarshalJSON(b []byte) error {
	type plain V1Trigger
	// -1 marks an absent minAgeDays, so an explicit 0 ("at least 0 days old") is kept
	p := plain{SettleHours: 6, MinAgeEnabled: true, Pinned: true, MinAgeDays: -1}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.MinAgeDays == -1 {
		p.MinAgeDays = absentMinAgeDays[p.Type]
	}
	*t = V1Trigger(p)
	return nil
}

// WatchRef a folder or document a pipeline rule watches, by UUID only.
// Name is a display cache. An empty folder id is the root.
type WatchRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // WatchFolder | WatchDocument
	Name string `json:"name,omitempty"`
}

// V1Action of a rule; Prompt, Model and Tasks are used by the pipeline action only
type V1Action struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`
	// Tasks false: "just save notes", rm-pages extracts no tasks from the
	// rule's pages. nil means true; Normalize stores true as nil.
	Tasks *bool `json:"tasks,omitempty"`
}

// ExtractsTasks the pipeline extracts tasks for this action (Tasks nil or true)
func (a *V1Action) ExtractsTasks() bool {
	return a.Tasks == nil || *a.Tasks
}

// V1Rule a single organisation rule
type V1Rule struct {
	ID          string      `json:"id"`
	Name        string      `json:"name,omitempty"`
	Enabled     bool        `json:"enabled"`
	Source      FolderRef   `json:"source"`
	Scope       string      `json:"scope"`
	Trigger     V1Trigger   `json:"trigger"`
	Destination Destination `json:"destination"`
	Action      V1Action    `json:"action"`
	// Watch what a pipeline rule watches; unused by move rules
	Watch []WatchRef `json:"watch,omitempty"`
}

// V1Config the v1 config file, auto-organize.json (read for migration only)
type V1Config struct {
	Revision int      `json:"revision"`
	Settings Settings `json:"settings"`
	Rules    []V1Rule `json:"rules"`
}

// IsPipeline reports whether the rule runs the pipeline action (not the move planner)
func (r *V1Rule) IsPipeline() bool {
	return r.Action.Type == ActionPipeline
}

// SkipsPinned the default pinned guard applies unless the trigger is about pinning
func (r *V1Rule) SkipsPinned() bool {
	return r.Trigger.Type != TriggerPinnedIs
}

// PageBased triggers that need a pageCount and are locked to PDF & EPUB
func (t *V1Trigger) PageBased() bool {
	s, ok := automation.Triggers[t.Type]
	return ok && s.PageBased
}

func validInterval(m int) bool {
	for _, a := range AllowedIntervals {
		if a == m {
			return true
		}
	}
	return false
}

// Normalize fills ids and per-type defaults. Called before validation on save.
func (c *V1Config) Normalize() {
	if c.Settings.Mode == "" {
		c.Settings.Mode = ModeDry
	}
	if c.Settings.IntervalMinutes == 0 {
		c.Settings.IntervalMinutes = DefaultInterval
	}
	if c.Rules == nil {
		c.Rules = []V1Rule{}
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if r.Action.Type == "" {
			r.Action.Type = ActionMove
		}
		if r.Action.Tasks != nil && *r.Action.Tasks {
			// true is the default: keep the stored rule as it was before Tasks
			r.Action.Tasks = nil
		}
		if r.Scope == "" {
			r.Scope = ScopePdfEpub
			if r.IsPipeline() {
				// watched documents include annotated PDFs/EPUBs
				r.Scope = ScopeAll
			}
		}
		if r.IsPipeline() {
			// a whitespace-only prompt or model would reach the LLM as is
			r.Action.Model = strings.TrimSpace(r.Action.Model)
			r.Action.Prompt = strings.TrimSpace(r.Action.Prompt)
			if r.Action.Model == "" {
				r.Action.Model = DefaultPipelineModel
			}
			if r.Action.Prompt == "" {
				r.Action.Prompt = DefaultPipelinePrompt
			}
		}
		if s, ok := automation.Triggers[r.Trigger.Type]; ok && s.Defaults != nil {
			p := r.Trigger.params()
			s.Defaults(&p)
			r.Trigger.setParams(p)
		}
		if cm := r.Destination.CreateIfMissing; cm != nil {
			cm.Name = strings.TrimSpace(cm.Name)
			if cm.Parent == "" {
				cm.Parent = ParentSource
			}
		}
	}
}

// ValidationError a config validation failure, per rule when RuleIndex >= 0
type ValidationError struct {
	RuleIndex int    `json:"ruleIndex"`
	RuleID    string `json:"ruleId,omitempty"`
	Message   string `json:"message"`
}

func (e *ValidationError) Error() string {
	if e.RuleIndex < 0 {
		return e.Message
	}
	return fmt.Sprintf("rule %d: %s", e.RuleIndex+1, e.Message)
}

// Validate checks settings and every rule
func (c *V1Config) Validate() error {
	if c.Settings.Mode != ModeLive && c.Settings.Mode != ModeDry {
		return &ValidationError{RuleIndex: -1, Message: "mode must be live or dry"}
	}
	if !validInterval(c.Settings.IntervalMinutes) {
		return &ValidationError{RuleIndex: -1, Message: "interval must be one of 5, 10, 15, 30, 60 minutes"}
	}
	seen := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		if seen[r.ID] {
			return &ValidationError{RuleIndex: i, RuleID: r.ID, Message: "duplicate rule id"}
		}
		seen[r.ID] = true
		if err := r.Validate(); err != nil {
			return &ValidationError{RuleIndex: i, RuleID: r.ID, Message: err.Error()}
		}
	}
	return nil
}

// Validate a single rule
func (r *V1Rule) Validate() error {
	switch r.Action.Type {
	case ActionMove:
		if !r.Action.ExtractsTasks() {
			return errors.New("tasks applies to pipeline rules only")
		}
	case ActionPipeline:
		return r.validatePipeline()
	default:
		return fmt.Errorf("unsupported action %q", r.Action.Type)
	}
	switch r.Scope {
	case ScopePdfEpub, ScopeNotebooks, ScopeAll:
	default:
		return fmt.Errorf("unknown scope %q", r.Scope)
	}
	if r.Source.ID == trashID {
		return errors.New("source cannot be the trash")
	}
	if r.Destination.ID == trashID {
		return errors.New("destination cannot be the trash")
	}
	if cm := r.Destination.CreateIfMissing; cm != nil {
		if cm.Name == "" {
			return errors.New("new subfolder needs a name")
		}
		if cm.Parent == trashID {
			return errors.New("new subfolder cannot be created in the trash")
		}
	} else if r.Destination.ID == r.Source.ID {
		return errors.New("destination is the same as the source")
	}

	t := &r.Trigger
	spec, ok := automation.Triggers[t.Type]
	if !ok {
		return fmt.Errorf("unknown trigger %q", t.Type)
	}
	if isPageTrigger(spec) {
		return fmt.Errorf("trigger %q is only for pipeline rules", t.Type)
	}
	if spec.PageBased && r.Scope != ScopePdfEpub {
		return errors.New("this trigger only applies to PDF & EPUB")
	}
	if spec.Validate != nil {
		p := t.params()
		if err := spec.Validate(&p); err != nil {
			return err
		}
	}
	// v1 only: min age is a trigger field here (a min_age_days condition in v2)
	if t.Type == TriggerNotOpened && t.MinAgeEnabled && t.MinAgeDays < 0 {
		return errors.New("minimum age cannot be negative")
	}
	return nil
}

// validatePipeline the pipeline action: a page trigger (page_changed or
// page_tagged) and valid watch entries. page_changed needs at least one entry;
// for page_tagged none means the whole tree. Source and destination are not used.
func (r *V1Rule) validatePipeline() error {
	switch r.Scope {
	case ScopePdfEpub, ScopeNotebooks, ScopeAll:
	default:
		return fmt.Errorf("unknown scope %q", r.Scope)
	}
	switch r.Trigger.Type {
	case TriggerPageChanged:
		if len(r.Watch) == 0 {
			return errors.New("a pipeline rule needs at least one watch entry")
		}
	case TriggerPageTagged:
		p := r.Trigger.params()
		if err := automation.Triggers[TriggerPageTagged].Validate(&p); err != nil {
			return err
		}
	default:
		return fmt.Errorf("a pipeline rule needs the %s or %s trigger", TriggerPageChanged, TriggerPageTagged)
	}
	for _, w := range r.Watch {
		switch w.Kind {
		case WatchFolder:
		case WatchDocument:
			if w.ID == "" {
				return errors.New("a watched document needs an id")
			}
		default:
			return fmt.Errorf("unknown watch kind %q", w.Kind)
		}
		if w.ID == trashID {
			return errors.New("cannot watch the trash")
		}
	}
	return nil
}

// CompilePattern compiles a name pattern, case-insensitive (automation.CompilePattern)
func CompilePattern(p string) (*regexp.Regexp, error) {
	return automation.CompilePattern(p)
}

// NoAutomationTag the opt-out tag, as displayed
const NoAutomationTag = automation.NoAutomationTag

// NormalizeTag lowercases and collapses whitespace, '-' and '_' runs to one space
func NormalizeTag(tag string) string {
	return automation.NormalizeTag(tag)
}

// IsNoAutomationTag reports whether tag is the opt-out tag in any spelling
func IsNoAutomationTag(tag string) bool {
	return automation.IsNoAutomationTag(tag)
}
