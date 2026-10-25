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
)

// ActionMove the only v1 action
const ActionMove = "move"

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
	MinAgeDays    int  `json:"minAgeDays,omitempty"`
	// name_matches: glob, or /regex/
	Pattern string `json:"pattern,omitempty"`
	// tag_is
	Tag string `json:"tag,omitempty"`
	// pinned_is
	Pinned bool `json:"pinned"`
}

// UnmarshalJSON fills defaults for parameters that are absent but where zero is meaningful
func (t *Trigger) UnmarshalJSON(b []byte) error {
	type plain Trigger
	p := plain{SettleHours: 6, MinAgeEnabled: true, Pinned: true}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*t = Trigger(p)
	return nil
}

// Action of a rule
type Action struct {
	Type string `json:"type"`
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

// SkipsPinned the default pinned guard applies unless the trigger is about pinning
func (r *Rule) SkipsPinned() bool {
	return r.Trigger.Type != TriggerPinnedIs
}

// PageBased triggers that need a pageCount and are locked to PDF & EPUB
func (t *Trigger) PageBased() bool {
	return t.Type == TriggerFinishedReading || t.Type == TriggerReadProgress
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
		if r.Scope == "" {
			r.Scope = ScopePdfEpub
		}
		if r.Action.Type == "" {
			r.Action.Type = ActionMove
		}
		t := &r.Trigger
		switch t.Type {
		case TriggerFinishedReading:
			if t.MinPages == 0 {
				t.MinPages = 3
			}
		case TriggerReadProgress:
			if t.Percent == 0 {
				t.Percent = 80
			}
		case TriggerNotOpened:
			if t.Days == 0 {
				t.Days = 30
			}
			if t.MinAgeEnabled && t.MinAgeDays == 0 {
				t.MinAgeDays = 7
			}
		case TriggerNotModified:
			if t.Days == 0 {
				t.Days = 90
			}
		case TriggerAgeSinceCreated:
			if t.Days == 0 {
				t.Days = 14
			}
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
	if r.Action.Type != ActionMove {
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
	if t.PageBased() && r.Scope != ScopePdfEpub {
		return errors.New("this trigger only applies to PDF & EPUB")
	}
	switch t.Type {
	case TriggerFinishedReading:
		if t.MinPages < 1 {
			return errors.New("minimum pages must be at least 1")
		}
		if t.SettleHours < 0 {
			return errors.New("settle hours cannot be negative")
		}
	case TriggerReadProgress:
		if t.Percent < 1 || t.Percent > 100 {
			return errors.New("percent must be between 1 and 100")
		}
		if t.SettleHours < 0 {
			return errors.New("settle hours cannot be negative")
		}
	case TriggerNotOpened:
		if t.Days < 1 {
			return errors.New("days must be at least 1")
		}
		if t.MinAgeEnabled && t.MinAgeDays < 0 {
			return errors.New("minimum age cannot be negative")
		}
	case TriggerNotModified, TriggerAgeSinceCreated:
		if t.Days < 1 {
			return errors.New("days must be at least 1")
		}
	case TriggerNameMatches:
		if _, err := CompilePattern(t.Pattern); err != nil {
			return err
		}
	case TriggerTagIs:
		if NormalizeTag(t.Tag) == "" {
			return errors.New("tag cannot be empty")
		}
	case TriggerPinnedIs:
	default:
		return fmt.Errorf("unknown trigger %q", t.Type)
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
