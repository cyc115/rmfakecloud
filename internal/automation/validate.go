package automation

import (
	"errors"
	"fmt"
)

// MsgOneAction the R-B2 rule: exactly one action per rule
const MsgOneAction = "a rule has exactly one action for now: extract tasks already saves the note; use one rule per document action (move, add tag, remove tag)"

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

// Validate checks settings and every rule. Run Normalize first: Validate
// does not fill defaults.
func (c *Config) Validate() error {
	if c.Version != Version {
		return &ValidationError{RuleIndex: -1, Message: fmt.Sprintf("unsupported automation format version %d; this server reads version %d", c.Version, Version)}
	}
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

// Validate a single rule: subject, trigger, places, conditions, actions
func (r *Rule) Validate() error {
	subject := r.When.Subject
	if subject != SubjectDocument && subject != SubjectPage {
		return errors.New("subject must be document or page")
	}
	if subject == SubjectPage && len(r.When.OrTriggers) > 0 {
		return errors.New("a page rule has one trigger; only document rules take “or” triggers")
	}
	for _, t := range r.When.AllTriggers() {
		if err := validateTrigger(&t, subject); err != nil {
			return err
		}
	}
	if err := r.validatePlaces(); err != nil {
		return err
	}
	if err := r.validateConditions(); err != nil {
		return err
	}
	if r.When.AllPageBased() && r.Kind() != KindPdfEpub {
		if len(r.When.OrTriggers) == 0 {
			return errors.New("this trigger only applies to PDF & EPUB")
		}
		return errors.New("these triggers only apply to PDF & EPUB")
	}
	if r.Kind() == KindNotebooks {
		for _, t := range r.When.AllTriggers() {
			if Triggers[t.Type].PageBased {
				return fmt.Errorf("trigger %q only applies to PDF & EPUB, and this rule is for notebooks", t.Type)
			}
		}
	}
	return r.validateActions()
}

func validateTrigger(t *Trigger, subject string) error {
	spec, ok := Triggers[t.Type]
	if !ok {
		return fmt.Errorf("unknown trigger %q", t.Type)
	}
	if !hasSubject(spec, subject) {
		if subject == SubjectDocument {
			return fmt.Errorf("trigger %q works on pages; it needs a page rule", t.Type)
		}
		return fmt.Errorf("trigger %q works on documents; it needs a document rule", t.Type)
	}
	if spec.Validate != nil {
		p := t.Params
		if err := spec.Validate(&p); err != nil {
			return err
		}
	}
	return nil
}

func (r *Rule) validatePlaces() error {
	in := r.When.In
	if r.IsDocument() {
		if len(in) != 1 || in[0].Kind != PlaceFolder {
			return errors.New("a document rule watches exactly one folder (a move: the documents directly inside it; a tag rule: every folder inside it too)")
		}
		if in[0].ID == trashID {
			return errors.New("source cannot be the trash")
		}
		return nil
	}
	if len(in) == 0 && r.When.Trigger.Type == TriggerPageChanged {
		return errors.New("a page rule needs at least one place; send the root folder for anywhere")
	}
	for _, p := range in {
		switch p.Kind {
		case PlaceFolder:
		case PlaceDocument:
			if p.ID == "" {
				return errors.New("a watched document needs an id")
			}
		default:
			return fmt.Errorf("unknown place kind %q", p.Kind)
		}
		if p.ID == trashID {
			return errors.New("cannot watch the trash")
		}
	}
	return nil
}

func (r *Rule) validateConditions() error {
	seen := map[string]bool{}
	for _, c := range r.Conditions {
		spec, ok := Conditions[c.Type]
		if !ok {
			return fmt.Errorf("unknown condition %q", c.Type)
		}
		if !hasSubject(spec, r.When.Subject) {
			return fmt.Errorf("condition %q does not apply to %s rules", c.Type, r.When.Subject)
		}
		if seen[c.Type] && !spec.Repeatable {
			return fmt.Errorf("condition %q appears twice", c.Type)
		}
		seen[c.Type] = true
		if spec.Validate != nil {
			p := c.Params
			if err := spec.Validate(&p); err != nil {
				return err
			}
		}
	}
	return nil
}

// actionLabels the page actions as the UI names them
var actionLabels = map[string]string{ActionExtractTasks: "extract tasks", ActionSaveNotes: "save notes",
	ActionAddTag: "add tag", ActionRemoveTag: "remove tag", ActionTrash: "trash"}

func (r *Rule) validateActions() error {
	if len(r.Actions) != 1 {
		return errors.New(MsgOneAction)
	}
	a := &r.Actions[0]
	switch a.Type {
	case ActionMove:
		if r.IsPage() {
			return errors.New("move works on whole documents, so a page rule cannot move; use a document trigger")
		}
		return r.validateMove(a)
	case ActionExtractTasks, ActionSaveNotes:
		if r.IsDocument() {
			return fmt.Errorf("%s works on pages, which a document rule does not see; use a page trigger", actionLabels[a.Type])
		}
		return nil
	case ActionAddTag, ActionRemoveTag:
		if r.IsPage() {
			return fmt.Errorf("%s works on whole documents, so a page rule cannot %s; use a document trigger", actionLabels[a.Type], actionLabels[a.Type])
		}
		return validDocTag(&Params{Tag: a.Tag})
	case ActionTrash:
		if r.IsPage() {
			return errors.New("trash works on whole documents, so a page rule cannot trash; use a document trigger")
		}
		if a.To != nil || a.Tag != "" {
			return errors.New("trash takes no destination and no tag")
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", a.Type)
	}
}

// validateMove the v1 destination checks
func (r *Rule) validateMove(a *Action) error {
	to := a.To
	if to == nil {
		return errors.New("move needs a destination")
	}
	if to.ID == trashID {
		return errors.New("destination cannot be the trash")
	}
	if cm := to.CreateIfMissing; cm != nil {
		if cm.Name == "" {
			return errors.New("new subfolder needs a name")
		}
		if cm.Parent == trashID {
			return errors.New("new subfolder cannot be created in the trash")
		}
	} else if to.ID == r.Source().ID {
		return errors.New("destination is the same as the source")
	}
	return nil
}
