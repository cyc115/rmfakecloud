// Package autoorganize applies user defined organisation rules to a sync15
// document tree: source folder -> scope -> trigger -> action (move).
package autoorganize

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Modes
const (
	ModeLive = "live"
	ModeDry  = "dry"
)

// Scopes, read from the .content fileType
const (
	ScopePdfEpub   = "pdf_epub"
	ScopeNotebooks = "notebooks"
	ScopeAll       = "all"
)

// v1 triggers
const (
	TriggerFinishedReading = "finished_reading"
	TriggerNotOpened       = "not_opened_for_days"
	TriggerNotModified     = "not_modified_for_days"
	TriggerNameMatches     = "name_matches"
	TriggerTagIs           = "tag_is"
	TriggerReadProgress    = "read_progress"
	TriggerAgeSinceCreated = "age_since_created"
	TriggerPinnedIs        = "pinned_is"
	// TriggerPageChanged belongs to pipeline rules only; the move planner never evaluates it
	TriggerPageChanged = "page_changed"
	// TriggerPageTagged belongs to pipeline rules only: pages carrying Trigger.Tag
	TriggerPageTagged = "page_tagged"
)

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
const DefaultPipelineModel = "deepseek/deepseek-v4.1-flash"

// DefaultPipelinePrompt the extraction intent a pipeline rule carries to rm-pages
// when it names none. rm-pages adds the fixed output contract on its side.
const DefaultPipelinePrompt = `Compare the BEFORE and AFTER images of one handwritten notebook page.
List only the to-do items that are new in AFTER: lines Mike wrote as a checkbox ("- [ ]", "- []", "[ ]", "☐"). Do not turn prose, notes, questions without a checkbox, margin comments or diagrams into tasks. Ignore anything already present in BEFORE.
Keep each task close to Mike's own words; fix only obvious handwriting misreads and read carefully (e.g. "measure" is not "find out"). Drop the checkbox and labels like "TODO:".
Skip anything marked "-inbox": on a line, skip that line; on a heading, skip the block under it until the next heading or a clear gap; at the top of the page, skip the whole page. Put skipped items in "skipped".`

// ParentSource createIfMissing parent meaning "the rule's source folder"
const ParentSource = "source"

// RootID the id of the root folder ("My files"); documents at the root have an empty parent
const RootID = ""

// trashID the literal parent of trashed items
const trashID = "trash"

// AllowedIntervals sweep interval choices, minutes
var AllowedIntervals = []int{5, 10, 15, 30, 60}

// DefaultInterval minutes
const DefaultInterval = 15

// Settings the per-user sweeper settings
type Settings struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	IntervalMinutes int    `json:"intervalMinutes"`
}

// FolderRef a folder picked from the tree, by UUID only. Name is a display cache.
type FolderRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// CreateIfMissing names a subfolder to create under Parent ("source" or a folder UUID)
type CreateIfMissing struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
}

// Destination of a move
type Destination struct {
	FolderRef
	CreateIfMissing *CreateIfMissing `json:"createIfMissing,omitempty"`
}

// Trigger parameters; only the fields of Type are used
type Trigger struct {
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
func (t *Trigger) UnmarshalJSON(b []byte) error {
	type plain Trigger
	// -1 marks an absent minAgeDays, so an explicit 0 ("at least 0 days old") is kept
	p := plain{SettleHours: 6, MinAgeEnabled: true, Pinned: true, MinAgeDays: -1}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.MinAgeDays == -1 {
		p.MinAgeDays = 0
		if def, ok := triggerDefs[p.Type]; ok {
			p.MinAgeDays = def.AbsentMinAgeDays
		}
	}
	*t = Trigger(p)
	return nil
}

// WatchRef a folder or document a pipeline rule watches, by UUID only.
// Name is a display cache. An empty folder id is the root.
type WatchRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // WatchFolder | WatchDocument
	Name string `json:"name,omitempty"`
}

// Action of a rule; Prompt, Model and Tasks are used by the pipeline action only
type Action struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`
	// Tasks false: "just save notes", rm-pages extracts no tasks from the
	// rule's pages. nil means true; Normalize stores true as nil.
	Tasks *bool `json:"tasks,omitempty"`
}

// ExtractsTasks the pipeline extracts tasks for this action (Tasks nil or true)
func (a *Action) ExtractsTasks() bool {
	return a.Tasks == nil || *a.Tasks
}

// Rule a single organisation rule
type Rule struct {
	ID          string      `json:"id"`
	Name        string      `json:"name,omitempty"`
	Enabled     bool        `json:"enabled"`
	Source      FolderRef   `json:"source"`
	Scope       string      `json:"scope"`
	Trigger     Trigger     `json:"trigger"`
	Destination Destination `json:"destination"`
	Action      Action      `json:"action"`
	// Watch what a pipeline rule watches; unused by move rules
	Watch []WatchRef `json:"watch,omitempty"`
}

// Config the UI owned config, auto-organize.json
type Config struct {
	Revision int      `json:"revision"`
	Settings Settings `json:"settings"`
	Rules    []Rule   `json:"rules"`
}

// DefaultConfig used when the user has no config file yet
func DefaultConfig(interval int) *Config {
	if !validInterval(interval) {
		interval = DefaultInterval
	}
	return &Config{
		Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: interval},
		Rules:    []Rule{},
	}
}

// IsPipeline reports whether the rule runs the pipeline action (not the move planner)
func (r *Rule) IsPipeline() bool {
	return r.Action.Type == ActionPipeline
}

// SkipsPinned the default pinned guard applies unless the trigger is about pinning
func (r *Rule) SkipsPinned() bool {
	return r.Trigger.Type != TriggerPinnedIs
}

// PageBased triggers that need a pageCount and are locked to PDF & EPUB
func (t *Trigger) PageBased() bool {
	def, ok := triggerDefs[t.Type]
	return ok && def.PageBased
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
func (c *Config) Normalize() {
	if c.Settings.Mode == "" {
		c.Settings.Mode = ModeDry
	}
	if c.Settings.IntervalMinutes == 0 {
		c.Settings.IntervalMinutes = DefaultInterval
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
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
		if def, ok := triggerDefs[r.Trigger.Type]; ok && def.Defaults != nil {
			def.Defaults(&r.Trigger)
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
func (c *Config) Validate() error {
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
func (r *Rule) Validate() error {
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
	def, ok := triggerDefs[t.Type]
	if !ok {
		return fmt.Errorf("unknown trigger %q", t.Type)
	}
	if def.PipelineOnly {
		return fmt.Errorf("trigger %q is only for pipeline rules", t.Type)
	}
	if def.PageBased && r.Scope != ScopePdfEpub {
		return errors.New("this trigger only applies to PDF & EPUB")
	}
	if def.Validate != nil {
		return def.Validate(t)
	}
	return nil
}

// validatePipeline the pipeline action: a page trigger (page_changed or
// page_tagged) and valid watch entries. page_changed needs at least one entry;
// for page_tagged none means the whole tree. Source and destination are not used.
func (r *Rule) validatePipeline() error {
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
		if err := triggerDefs[TriggerPageTagged].Validate(&r.Trigger); err != nil {
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

// CompilePattern compiles a name pattern, case-insensitive.
// "/re/" is an RE2 regular expression (unanchored); anything else is a glob
// matched against the whole name, where * is any run and ? one character.
func CompilePattern(p string) (*regexp.Regexp, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil, errors.New("pattern cannot be empty")
	}
	if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
		re, err := regexp.Compile("(?i)" + p[1:len(p)-1])
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %v", err)
		}
		return re, nil
	}
	var sb strings.Builder
	sb.WriteString("(?is)^")
	for _, r := range p {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

// NoAutomationTag the opt-out tag, as displayed
const NoAutomationTag = "no automation"

var tagSeparators = regexp.MustCompile(`[\s_\-]+`)

// NormalizeTag lowercases and collapses whitespace, '-' and '_' runs to one space
func NormalizeTag(tag string) string {
	return strings.TrimSpace(tagSeparators.ReplaceAllString(strings.ToLower(tag), " "))
}

// IsNoAutomationTag reports whether tag is the opt-out tag in any spelling
func IsNoAutomationTag(tag string) bool {
	return NormalizeTag(tag) == NoAutomationTag
}

// LoopWarnings returns, per rule id, the 1-based numbers of the other enabled
// move rules it forms a folder cycle with (R5, a warning, never a rejection).
// destOf resolves a rule's destination folder id; ok false leaves the rule out
// (e.g. a subfolder that has not been created yet cannot lead anywhere).
func LoopWarnings(c *Config, destOf func(r *Rule) (id string, ok bool)) map[string][]int {
	type edge struct {
		from, to string
		idx      int
	}
	var edges []edge
	for i := range c.Rules {
		r := &c.Rules[i]
		if !r.Enabled || r.Action.Type != ActionMove {
			continue
		}
		to, ok := destOf(r)
		if !ok {
			continue
		}
		edges = append(edges, edge{r.Source.ID, to, i})
	}
	result := map[string][]int{}
	for _, e := range edges {
		// rules on any path from e.to back to e.from close a cycle with e
		visited := map[string]bool{}
		var path []int
		var members []int
		var dfs func(node string) bool
		dfs = func(node string) bool {
			if node == e.from {
				members = append(members, path...)
				return true
			}
			if visited[node] {
				return false
			}
			visited[node] = true
			found := false
			for _, n := range edges {
				if n.idx == e.idx || n.from != node {
					continue
				}
				path = append(path, n.idx)
				if dfs(n.to) {
					found = true
				}
				path = path[:len(path)-1]
			}
			return found
		}
		dfs(e.to)
		if len(members) > 0 {
			seen := map[int]bool{}
			var nums []int
			for _, m := range members {
				if !seen[m] {
					seen[m] = true
					nums = append(nums, m+1)
				}
			}
			result[c.Rules[e.idx].ID] = nums
		}
	}
	return result
}
