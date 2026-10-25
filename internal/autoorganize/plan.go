package autoorganize

import (
	"fmt"
	"strings"
	"time"
)

// PlannedMove one document, one rule, one move
type PlannedMove struct {
	RuleID     string `json:"ruleId"`
	RuleNumber int    `json:"ruleNumber"`
	RuleName   string `json:"ruleName"`
	DocID      string `json:"docId"`
	DocName    string `json:"docName"`
	From       string `json:"from"`
	FromName   string `json:"fromName"`
	// To empty with CreateFolder set: the folder is created on the first live move
	To           string            `json:"to"`
	ToName       string            `json:"toName"`
	CreateFolder *CreateIfMissing  `json:"createFolder,omitempty"`
	Detail       string            `json:"detail"`
	Inputs       map[string]string `json:"inputs"`
	Fingerprint  string            `json:"-"`
}

// Skip a document a rule did not act on, with the reason
type Skip struct {
	RuleID     string `json:"ruleId"`
	RuleNumber int    `json:"ruleNumber"`
	DocID      string `json:"docId"`
	DocName    string `json:"docName"`
	Folder     string `json:"folder"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
	HoldID     string `json:"holdId,omitempty"`
}

// RuleResult per rule outcome, also the source of the row status chips
type RuleResult struct {
	RuleID            string   `json:"ruleId"`
	Number            int      `json:"number"`
	Name              string   `json:"name"`
	Enabled           bool     `json:"enabled"`
	SourceID          string   `json:"sourceId"`
	SourceName        string   `json:"sourceName"`
	SourceRenamedFrom string   `json:"sourceRenamedFrom,omitempty"`
	DestID            string   `json:"destId"`
	DestName          string   `json:"destName"`
	DestRenamedFrom   string   `json:"destRenamedFrom,omitempty"`
	DestWillCreate    bool     `json:"destWillCreate"`
	Problems          []string `json:"problems"`
	Checked           int      `json:"checked"`
	Matches           int      `json:"matches"`
	NotMatching       int      `json:"notMatching"`
	OptedOut          int      `json:"optedOut"`
	Held              int      `json:"held"`
}

// HoldRef (rule, doc)
type HoldRef struct {
	RuleID string
	DocID  string
}

// Plan the outcome of evaluating every rule against one snapshot
type Plan struct {
	At      int64         `json:"at"`
	Scanned int           `json:"scanned"`
	Rules   []RuleResult  `json:"rules"`
	Moves   []PlannedMove `json:"moves"`
	Skipped []Skip        `json:"skipped"`
	// ClearHolds holds whose trigger inputs changed (cleared automatically)
	ClearHolds []HoldRef `json:"-"`
}

// PlanInput everything the pure planner reads
type PlanInput struct {
	Snap    *Snapshot
	Config  *Config
	State   *State
	Content func(d *DocSnap) ContentInfo
	Now     time.Time
}

// destination resolved for one rule
type resolvedDest struct {
	id      string
	name    string
	create  *CreateIfMissing
	problem string
}

// ResolveDestination resolves a rule's destination by UUID, or for
// createIfMissing via the state's created folder, then by name under the parent
func ResolveDestination(r *Rule, snap *Snapshot, st *State) resolvedDest {
	cm := r.Destination.CreateIfMissing
	if cm == nil {
		id := r.Destination.ID
		if !snap.FolderExists(id) {
			return resolvedDest{id: id, name: r.Destination.Name, problem: "Destination folder missing"}
		}
		if snap.Locate(id) == LocInTrash {
			return resolvedDest{id: id, name: snap.PathName(id), problem: "Destination in Trash"}
		}
		return resolvedDest{id: id, name: snap.PathName(id)}
	}
	parent := cm.Parent
	if parent == ParentSource {
		parent = r.Source.ID
	}
	if !snap.FolderExists(parent) {
		return resolvedDest{name: cm.Name, problem: "Destination parent folder missing"}
	}
	if snap.Locate(parent) == LocInTrash {
		return resolvedDest{name: cm.Name, problem: "Destination in Trash"}
	}
	if created, ok := st.CreatedFolders[r.ID]; ok && snap.FolderExists(created) {
		if snap.Locate(created) == LocInTrash {
			return resolvedDest{id: created, name: snap.PathName(created), problem: "Destination in Trash"}
		}
		return resolvedDest{id: created, name: snap.PathName(created)}
	}
	var matches []*DocSnap
	for _, c := range snap.Children(parent) {
		if c.IsFolder() && strings.EqualFold(strings.TrimSpace(c.Name), cm.Name) {
			matches = append(matches, c)
		}
	}
	parentName := snap.PathName(parent)
	switch len(matches) {
	case 0:
		name := cm.Name
		if parent != RootID {
			name = parentName + " / " + cm.Name
		}
		return resolvedDest{name: name, create: cm}
	case 1:
		return resolvedDest{id: matches[0].ID, name: snap.PathName(matches[0].ID)}
	}
	return resolvedDest{name: cm.Name, problem: fmt.Sprintf("%d folders named “%s” in %s", len(matches), cm.Name, parentName)}
}

func ruleLabel(r *Rule, srcName, destName string) string {
	if r.Name != "" {
		return r.Name
	}
	return srcName + " → " + destName
}

// BuildPlan evaluates the enabled rules top to bottom in one pass (R1, R2)
// against the snapshot (R3). The first rule that matches a document claims
// it; a document appears in at most one planned move.
func BuildPlan(in PlanInput) *Plan {
	p := &Plan{At: in.Now.UnixMilli(), Rules: []RuleResult{}, Moves: []PlannedMove{}, Skipped: []Skip{}}
	claimedBy := map[string]int{}
	scanned := map[string]bool{}

	for i := range in.Config.Rules {
		r := &in.Config.Rules[i]
		num := i + 1
		rr := RuleResult{RuleID: r.ID, Number: num, Name: r.Name, Enabled: r.Enabled, SourceID: r.Source.ID, Problems: []string{}}

		// source, by UUID only
		srcOK := true
		if !in.Snap.FolderExists(r.Source.ID) {
			rr.SourceName = r.Source.Name
			rr.Problems = append(rr.Problems, "Source folder missing")
			srcOK = false
		} else {
			rr.SourceName = in.Snap.PathName(r.Source.ID)
			if cur := lastName(rr.SourceName); r.Source.Name != "" && r.Source.Name != cur && r.Source.ID != RootID {
				rr.SourceRenamedFrom = r.Source.Name
			}
			if in.Snap.Locate(r.Source.ID) == LocInTrash {
				rr.Problems = append(rr.Problems, "Source folder in Trash")
				srcOK = false
			}
		}
		dest := ResolveDestination(r, in.Snap, in.State)
		rr.DestID, rr.DestName, rr.DestWillCreate = dest.id, dest.name, dest.create != nil
		if r.Destination.CreateIfMissing == nil && dest.problem == "" && r.Destination.Name != "" && r.Destination.Name != lastName(dest.name) && dest.id != RootID {
			rr.DestRenamedFrom = r.Destination.Name
		}
		if dest.problem != "" {
			rr.Problems = append(rr.Problems, dest.problem)
		}
		if err := r.Validate(); err != nil {
			rr.Problems = append(rr.Problems, "Invalid rule: "+err.Error())
		}
		if srcOK && dest.problem == "" && dest.id != "" && dest.id == r.Source.ID {
			rr.Problems = append(rr.Problems, "Destination is the same as the source")
		}
		label := ruleLabel(r, rr.SourceName, rr.DestName)
		if !r.Enabled || len(rr.Problems) > 0 {
			p.Rules = append(p.Rules, rr)
			continue
		}

		for _, d := range in.Snap.Children(r.Source.ID) {
			if d.IsFolder() {
				// guard 1: only documents are ever candidates
				continue
			}
			scanned[d.ID] = true
			rr.Checked++
			skip := func(reason, detail, holdID string) {
				p.Skipped = append(p.Skipped, Skip{RuleID: r.ID, RuleNumber: num, DocID: d.ID, DocName: d.Name,
					Folder: rr.SourceName, Reason: reason, Detail: detail, HoldID: holdID})
			}
			if by, ok := claimedBy[d.ID]; ok {
				skip(SkipClaimed, fmt.Sprintf("claimed by rule %d", by), "")
				continue
			}
			ci := in.Content(d)
			chk := checkDoc(r, dest.id, d, &ci, in.State.FirstSeen[d.ID], in.Snap.Locate, in.Now)
			if chk.skip != "" {
				if chk.skip == SkipNoAutomation {
					rr.OptedOut++
				}
				skip(chk.skip, chk.detail, "")
				continue
			}
			hold := in.State.FindHold(r.ID, d.ID)
			holdApplies := hold != nil && hold.Source == r.Source.ID && sameInputs(hold.Inputs, chk.ev.inputs)
			if hold != nil && !holdApplies {
				p.ClearHolds = append(p.ClearHolds, HoldRef{RuleID: r.ID, DocID: d.ID})
			}
			if !chk.ev.match {
				rr.NotMatching++
				continue
			}
			if holdApplies {
				rr.Held++
				what := "returned to source after move"
				if hold.Reason == "undo" {
					what = "move undone"
				}
				skip(SkipHeld, fmt.Sprintf("%s on %s, inputs unchanged", what, time.UnixMilli(hold.CreatedAt).Format("Jan 2 15:04")), hold.ID)
				continue
			}
			// a folder still to be created has no history; the root ("") does
			if dest.create == nil {
				if h := in.State.CycleBlock(d.ID, dest.id, d.Fingerprint, in.Now); h != nil {
					skip(SkipCycleGuard, fmt.Sprintf("would return to %s; moved out of it by rule %d at %s",
						rr.DestName, h.RuleNumber, time.UnixMilli(h.At).Format("Jan 2 15:04")), "")
					continue
				}
			}
			claimedBy[d.ID] = num
			rr.Matches++
			p.Moves = append(p.Moves, PlannedMove{
				RuleID: r.ID, RuleNumber: num, RuleName: label,
				DocID: d.ID, DocName: d.Name,
				From: d.Parent, FromName: rr.SourceName,
				To: dest.id, ToName: rr.DestName, CreateFolder: dest.create,
				Detail: chk.ev.detail, Inputs: chk.ev.inputs, Fingerprint: d.Fingerprint,
			})
		}
		p.Rules = append(p.Rules, rr)
	}
	p.Scanned = len(scanned)
	return p
}

func lastName(path string) string {
	if i := strings.LastIndex(path, " / "); i >= 0 {
		return path[i+3:]
	}
	return path
}

// SkippedCount skips worth counting in Runs (claims are not skips of the document)
func (p *Plan) SkippedCount() int {
	n := 0
	for _, s := range p.Skipped {
		if s.Reason != SkipClaimed {
			n++
		}
	}
	return n
}
