package automation

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Version the automation.json format this package reads and writes
const Version = 2

// Actions
const (
	// ActionMove moves a matching document to a folder (document rules)
	ActionMove = "move"
	// ActionExtractTasks sends changed pages to rm-pages, which saves the
	// note and extracts the new tasks (page rules)
	ActionExtractTasks = "extract_tasks"
	// ActionSaveNotes sends changed pages to rm-pages, which saves the note
	// and extracts no tasks (page rules)
	ActionSaveNotes = "save_notes"
)

// Place kinds
const (
	PlaceFolder   = "folder"
	PlaceDocument = "document"
)

// RootID the id of the root folder ("My files"); documents at the root have an empty parent
const RootID = ""

// ParentSource createIfMissing parent meaning "the rule's source folder"
const ParentSource = "source"

// trashID the literal parent of trashed items
const trashID = "trash"

// Modes
const (
	ModeLive = "live"
	ModeDry  = "dry"
)

// AllowedIntervals sweep interval choices, minutes
var AllowedIntervals = []int{5, 10, 15, 30, 60}

// DefaultInterval minutes
const DefaultInterval = 15

// DefaultPipelineModel the OpenRouter model a page action uses when it names none
const DefaultPipelineModel = "deepseek/deepseek-v4.1-flash"

// DefaultPipelinePrompt the extraction intent a page action carries to rm-pages
// when it names none. rm-pages adds the fixed output contract on its side.
const DefaultPipelinePrompt = `Compare the BEFORE and AFTER images of one handwritten notebook page.
List only the to-do items that are new in AFTER: lines Mike wrote as a checkbox ("- [ ]", "- []", "[ ]", "☐"). Do not turn prose, notes, questions without a checkbox, margin comments or diagrams into tasks. Ignore anything already present in BEFORE.
Keep each task close to Mike's own words; fix only obvious handwriting misreads and read carefully (e.g. "measure" is not "find out"). Drop the checkbox and labels like "TODO:".
Skip anything marked "-inbox": on a line, skip that line; on a heading, skip the block under it until the next heading or a clear gap; at the top of the page, skip the whole page. Put skipped items in "skipped".`

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

// Place a folder or document a rule looks at, by UUID only. Name is a
// display cache. An empty folder id is the root.
type Place struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // PlaceFolder | PlaceDocument
	Name string `json:"name,omitempty"`
}

// Trigger what starts a rule; only the Params named by the type's Spec.Fields
// are stored
type Trigger struct {
	Type string
	Params
}

// Condition a further test a rule's subject must pass; only the Params named
// by the type's Spec.Fields are stored
type Condition struct {
	Type string
	Params
}

// marshalTyped writes {"type": typ} plus the fields of spec, in spec order.
// An unknown type (spec nil) writes the type only.
func marshalTyped(typ string, p Params, spec *Spec) ([]byte, error) {
	var buf bytes.Buffer
	tb, err := json.Marshal(typ)
	if err != nil {
		return nil, err
	}
	buf.WriteString(`{"type":`)
	buf.Write(tb)
	if spec != nil && len(spec.Fields) > 0 {
		pb, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		var all map[string]json.RawMessage
		if err := json.Unmarshal(pb, &all); err != nil {
			return nil, err
		}
		for _, f := range spec.Fields {
			kb, _ := json.Marshal(f)
			buf.WriteByte(',')
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(all[f])
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// MarshalJSON writes the type and only the fields of that type
func (t Trigger) MarshalJSON() ([]byte, error) {
	return marshalTyped(t.Type, t.Params, Triggers[t.Type])
}

// UnmarshalJSON defaults an absent settleHours to 6 and an absent pinned to true
func (t *Trigger) UnmarshalJSON(b []byte) error {
	type plain struct {
		Type string `json:"type"`
		Params
	}
	p := plain{Params: Params{SettleHours: 6, Pinned: true}}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*t = Trigger{Type: p.Type, Params: p.Params}
	return nil
}

// MarshalJSON writes the type and only the fields of that type
func (c Condition) MarshalJSON() ([]byte, error) {
	return marshalTyped(c.Type, c.Params, Conditions[c.Type])
}

// UnmarshalJSON reads the type and its parameters
func (c *Condition) UnmarshalJSON(b []byte) error {
	type plain struct {
		Type string `json:"type"`
		Params
	}
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = Condition{Type: p.Type, Params: p.Params}
	return nil
}

// Action what a rule does; To is used by move, Prompt and Model by
// extract_tasks and save_notes
type Action struct {
	Type   string       `json:"type"`
	To     *Destination `json:"to,omitempty"`
	Prompt string       `json:"prompt,omitempty"`
	Model  string       `json:"model,omitempty"`
}

// When the rule's subject, where it looks and what starts it
type When struct {
	Subject string  `json:"subject"`
	In      []Place `json:"in"`
	Trigger Trigger `json:"trigger"`
}

// Rule a single automation rule
type Rule struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	When       When        `json:"when"`
	Conditions []Condition `json:"conditions"`
	Actions    []Action    `json:"actions"`
}

// Config the UI owned config, automation.json
type Config struct {
	Version  int      `json:"version"`
	Revision int      `json:"revision"`
	Settings Settings `json:"settings"`
	Rules    []Rule   `json:"rules"`
}

func validInterval(m int) bool {
	for _, a := range AllowedIntervals {
		if a == m {
			return true
		}
	}
	return false
}

// DefaultConfig used when the user has no config file yet
func DefaultConfig(interval int) *Config {
	if !validInterval(interval) {
		interval = DefaultInterval
	}
	return &Config{
		Version:  Version,
		Settings: Settings{Enabled: true, Mode: ModeDry, IntervalMinutes: interval},
		Rules:    []Rule{},
	}
}

// IsDocument the rule looks at whole documents (the sweep)
func (r *Rule) IsDocument() bool {
	return r.When.Subject == SubjectDocument
}

// IsPage the rule looks at pages (the page watch)
func (r *Rule) IsPage() bool {
	return r.When.Subject == SubjectPage
}

// Source the folder a document rule watches (In[0]); zero when In is empty
func (r *Rule) Source() FolderRef {
	if len(r.When.In) == 0 {
		return FolderRef{}
	}
	return FolderRef{ID: r.When.In[0].ID, Name: r.When.In[0].Name}
}

func (r *Rule) firstAction(types ...string) *Action {
	for i := range r.Actions {
		for _, t := range types {
			if r.Actions[i].Type == t {
				return &r.Actions[i]
			}
		}
	}
	return nil
}

// Move the rule's move action, nil when it has none
func (r *Rule) Move() *Action {
	return r.firstAction(ActionMove)
}

// PageAction the rule's extract_tasks or save_notes action, nil when it has none
func (r *Rule) PageAction() *Action {
	return r.firstAction(ActionExtractTasks, ActionSaveNotes)
}

// Kind the kind_is condition's kind, else KindAll
func (r *Rule) Kind() string {
	for _, c := range r.Conditions {
		if c.Type == CondKindIs {
			return c.Kind
		}
	}
	return KindAll
}

// PageTags the page tags a page must carry: the page_tagged trigger's tag
// plus every page_tag_is condition, normalized, deduped and sorted (P4)
func (r *Rule) PageTags() []string {
	seen := map[string]bool{}
	var out []string
	add := func(tag string) {
		n := NormalizePageTag(tag)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if r.When.Trigger.Type == TriggerPageTagged {
		add(r.When.Trigger.Tag)
	}
	for _, c := range r.Conditions {
		if c.Type == CondPageTagIs {
			add(c.Tag)
		}
	}
	sort.Strings(out)
	return out
}

// SkipsPinned the default pinned guard applies unless the trigger or a
// condition is about pinning
func (r *Rule) SkipsPinned() bool {
	if r.When.Trigger.Type == TriggerPinnedIs {
		return false
	}
	for _, c := range r.Conditions {
		if c.Type == CondPinnedIs {
			return false
		}
	}
	return true
}

// DocConditions the conditions a document evaluator runs, in stored order:
// the known conditions with a document subject, without kind_is (a guard
// the engines run before the trigger)
func (r *Rule) DocConditions() []Condition {
	var out []Condition
	for _, c := range r.Conditions {
		s, ok := Conditions[c.Type]
		if !ok || s.BeforeTrigger || !hasSubject(s, SubjectDocument) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasSubject(s *Spec, subject string) bool {
	for _, sub := range s.Subjects {
		if sub == subject {
			return true
		}
	}
	return false
}

// Normalize fills ids and per-type defaults. Called before validation on save.
func (c *Config) Normalize() {
	c.Version = Version
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
		c.Rules[i].normalize()
	}
}

func (r *Rule) normalize() {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.When.In == nil {
		r.When.In = []Place{}
	}
	if r.Actions == nil {
		r.Actions = []Action{}
	}
	t := &r.When.Trigger
	if s, ok := Triggers[t.Type]; ok && s.Defaults != nil {
		s.Defaults(&t.Params)
	}
	conds := make([]Condition, 0, len(r.Conditions))
	hasKind := false
	for _, c := range r.Conditions {
		if c.Type == CondKindIs {
			if c.Kind == KindAll {
				continue
			}
			hasKind = true
		}
		if s, ok := Conditions[c.Type]; ok && s.Defaults != nil {
			s.Defaults(&c.Params)
		}
		conds = append(conds, c)
	}
	if s, ok := Triggers[t.Type]; ok && s.PageBased && !hasKind {
		conds = append([]Condition{{Type: CondKindIs, Params: Params{Kind: KindPdfEpub}}}, conds...)
	}
	r.Conditions = conds
	for i := range r.Actions {
		a := &r.Actions[i]
		switch a.Type {
		case ActionExtractTasks, ActionSaveNotes:
			// a whitespace-only prompt or model would reach the LLM as is
			a.Prompt = strings.TrimSpace(a.Prompt)
			a.Model = strings.TrimSpace(a.Model)
			if a.Prompt == "" {
				a.Prompt = DefaultPipelinePrompt
			}
			if a.Model == "" {
				a.Model = DefaultPipelineModel
			}
		}
		if a.To != nil && a.To.CreateIfMissing != nil {
			cm := a.To.CreateIfMissing
			cm.Name = strings.TrimSpace(cm.Name)
			if cm.Parent == "" {
				cm.Parent = ParentSource
			}
		}
	}
}
