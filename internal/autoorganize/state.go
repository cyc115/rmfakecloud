package autoorganize

import (
	"time"

	"github.com/google/uuid"
)

// CycleWindow how long the cross-sweep cycle guard (R4) remembers a move
const CycleWindow = 24 * time.Hour

// logLimit size of the moves and runs ring buffers
const logLimit = 50

// Log entry modes
const (
	EntryLive = "LIVE"
	EntryDry  = "DRY"
)

// Hold move-back memory for (rule, doc), keyed on the trigger inputs (§6.4)
type Hold struct {
	ID        string            `json:"id"`
	RuleID    string            `json:"ruleId"`
	DocID     string            `json:"docId"`
	Inputs    map[string]string `json:"inputs"`
	Source    string            `json:"source"`
	MovedTo   string            `json:"movedTo"`
	CreatedAt int64             `json:"createdAt"`
	// Reason "moved" (set by every sweeper move) or "undo"
	Reason string `json:"reason"`
}

// HistoryEntry a sweeper move remembered for the cycle guard (R4)
type HistoryEntry struct {
	DocID      string `json:"docId"`
	RuleID     string `json:"ruleId"`
	RuleNumber int    `json:"ruleNumber"`
	From       string `json:"from"`
	To         string `json:"to"`
	At         int64  `json:"at"`
	// Fingerprint of the tablet-side state at move time; a different
	// fingerprint later means new information and resets the window
	Fingerprint string `json:"fingerprint"`
}

// FirstSeen when the sweeper first saw a document, and in its current folder
type FirstSeen struct {
	At       int64  `json:"at"`
	Folder   string `json:"folder"`
	FolderAt int64  `json:"folderAt"`
}

// MoveEntry one line of the Moves tab
type MoveEntry struct {
	ID         string `json:"id"`
	At         int64  `json:"at"`
	Mode       string `json:"mode"`
	DocID      string `json:"docId"`
	DocName    string `json:"docName"`
	RuleID     string `json:"ruleId"`
	RuleName   string `json:"ruleName"`
	RuleNumber int    `json:"ruleNumber"`
	From       string `json:"from"`
	FromName   string `json:"fromName"`
	To         string `json:"to"`
	ToName     string `json:"toName"`
	Detail     string `json:"detail,omitempty"`
	// Kind "rule", "reapply" or "undo"
	Kind     string `json:"kind"`
	UndoneAt int64  `json:"undoneAt,omitempty"`
}

// RunEntry one line of the Runs tab
type RunEntry struct {
	ID           string   `json:"id"`
	At           int64    `json:"at"`
	Mode         string   `json:"mode"`
	Trigger      string   `json:"trigger"`
	Scanned      int      `json:"scanned"`
	Moved        int      `json:"moved"`
	Planned      int      `json:"planned"`
	Skipped      int      `json:"skipped"`
	Errors       int      `json:"errors"`
	DurationMs   int64    `json:"durationMs"`
	ErrorDetails []string `json:"errorDetails,omitempty"`
}

// HeldNow a document found held during the last sweep
type HeldNow struct {
	HoldID string `json:"holdId"`
	RuleID string `json:"ruleId"`
	DocID  string `json:"docId"`
}

// State the sweeper owned state, auto-organize.state.json
type State struct {
	Holds          []*Hold                `json:"holds"`
	History        []HistoryEntry         `json:"history"`
	FirstSeen      map[string]*FirstSeen  `json:"firstSeen"`
	CreatedFolders map[string]string      `json:"createdFolders"`
	ContentCache   map[string]ContentInfo `json:"contentCache"`
	LastSweep      *RunEntry              `json:"lastSweep,omitempty"`
	HeldNow        []HeldNow              `json:"heldNow"`
	Moves          []MoveEntry            `json:"moves"`
	Runs           []RunEntry             `json:"runs"`
}

// NewState an empty state
func NewState() *State {
	st := &State{}
	st.init()
	return st
}

func (st *State) init() {
	if st.FirstSeen == nil {
		st.FirstSeen = map[string]*FirstSeen{}
	}
	if st.CreatedFolders == nil {
		st.CreatedFolders = map[string]string{}
	}
	if st.ContentCache == nil {
		st.ContentCache = map[string]ContentInfo{}
	}
}

// FindHold the hold for (rule, doc)
func (st *State) FindHold(ruleID, docID string) *Hold {
	for _, h := range st.Holds {
		if h.RuleID == ruleID && h.DocID == docID {
			return h
		}
	}
	return nil
}

// HoldByID a hold by its id
func (st *State) HoldByID(id string) *Hold {
	for _, h := range st.Holds {
		if h.ID == id {
			return h
		}
	}
	return nil
}

// SetHold replaces the hold for (rule, doc)
func (st *State) SetHold(h Hold) *Hold {
	st.ClearHold(h.RuleID, h.DocID)
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	st.Holds = append(st.Holds, &h)
	return &h
}

// ClearHold removes the hold for (rule, doc)
func (st *State) ClearHold(ruleID, docID string) {
	kept := st.Holds[:0]
	for _, h := range st.Holds {
		if !(h.RuleID == ruleID && h.DocID == docID) {
			kept = append(kept, h)
		}
	}
	st.Holds = kept
}

// PruneHistory drops cycle guard entries older than the window
func (st *State) PruneHistory(now time.Time) {
	cutoff := now.Add(-CycleWindow).UnixMilli()
	kept := st.History[:0]
	for _, h := range st.History {
		if h.At >= cutoff {
			kept = append(kept, h)
		}
	}
	st.History = kept
}

// CycleBlock returns the remembered move that forbids moving doc into folder,
// i.e. the sweeper moved the doc out of folder within the window and the
// doc's tablet-side fingerprint has not changed since.
func (st *State) CycleBlock(docID, folder, fingerprint string, now time.Time) *HistoryEntry {
	cutoff := now.Add(-CycleWindow).UnixMilli()
	for i := len(st.History) - 1; i >= 0; i-- {
		h := &st.History[i]
		if h.DocID != docID || h.At < cutoff || h.Fingerprint != fingerprint {
			continue
		}
		if h.From == folder {
			return h
		}
	}
	return nil
}

// AppendMove adds to the moves ring buffer, newest first
func (st *State) AppendMove(m MoveEntry) {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	st.Moves = append([]MoveEntry{m}, st.Moves...)
	if len(st.Moves) > logLimit {
		st.Moves = st.Moves[:logLimit]
	}
}

// MoveByID a moves log entry by id
func (st *State) MoveByID(id string) *MoveEntry {
	for i := range st.Moves {
		if st.Moves[i].ID == id {
			return &st.Moves[i]
		}
	}
	return nil
}

// AppendRun adds to the runs ring buffer, newest first
func (st *State) AppendRun(r RunEntry) {
	st.Runs = append([]RunEntry{r}, st.Runs...)
	if len(st.Runs) > logLimit {
		st.Runs = st.Runs[:logLimit]
	}
}

// UpdateFirstSeen records first-seen timestamps for every document in the
// snapshot and forgets documents that are gone
func (st *State) UpdateFirstSeen(snap *Snapshot, now time.Time) {
	ms := now.UnixMilli()
	for id, d := range snap.Docs {
		fs, ok := st.FirstSeen[id]
		if !ok {
			st.FirstSeen[id] = &FirstSeen{At: ms, Folder: d.Parent, FolderAt: ms}
			continue
		}
		if fs.Folder != d.Parent {
			fs.Folder = d.Parent
			fs.FolderAt = ms
		}
	}
	for id := range st.FirstSeen {
		if _, ok := snap.Docs[id]; !ok {
			delete(st.FirstSeen, id)
		}
	}
}

// PruneContentCache keeps only the entries for the given blob hashes
func (st *State) PruneContentCache(keep map[string]bool) {
	for h := range st.ContentCache {
		if !keep[h] {
			delete(st.ContentCache, h)
		}
	}
}
