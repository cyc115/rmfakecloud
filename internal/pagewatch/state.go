package pagewatch

import "time"

// State the page watch's persisted baselines. Feed and pipeline baselines are
// independent: each moves only when its own event is accepted.
type State struct {
	// Feed "doc/page" → the hash last delivered on the timeline feed
	Feed map[string]string `json:"feed"`
	// Opened doc → the lastOpened (unix ms string) last delivered
	Opened map[string]string `json:"opened"`
	// Rules pipeline rule id → its baselines and members
	Rules map[string]*RuleState `json:"rules"`
	// FeedInitialised the first feed run recorded baselines
	FeedInitialised bool `json:"feedInitialised"`
	// Seen doc → the Now of the first sweep that saw it (any non-folder doc,
	// trashed or skipped too); dropped only when the doc leaves the tree
	Seen map[string]time.Time `json:"seen"`
}

// RuleState one pipeline rule's view of the tree
type RuleState struct {
	// Baselines "doc/page" → the hash last delivered for this rule
	Baselines map[string]string `json:"baselines"`
	// Members docs whose join (transcribe_only of every pre-existing page) completed
	Members map[string]bool `json:"members"`
	// LastActive the Now of the last sweep that evaluated the rule (rule and
	// account enabled); zero before its first activation
	LastActive time.Time `json:"lastActive"`
	// JoinStartedAt doc → the Now of the first sweep that saw it joining; a
	// page whose blob is newer is new writing (an edit), not pre-existing
	// content. Dropped when the join completes or the doc leaves.
	JoinStartedAt map[string]time.Time `json:"joinStartedAt,omitempty"`
	// Trigger what the state was built for: "" for page_changed (every state
	// written before page_tagged existed), "page_tagged:<tag>" otherwise. A
	// rule whose trigger no longer matches starts afresh.
	Trigger string `json:"trigger,omitempty"`
	// TagWatched page_tagged: docs that were in the rule at an earlier sweep;
	// a page tagged in one of them since is newly tagged (an edit)
	TagWatched map[string]bool `json:"tagWatched,omitempty"`
	// TagJoinAt page_tagged: "doc/page" → the threshold of a page that
	// already carried the tag when the rule first looked at its doc: it is
	// transcribe_only unless its blob or its tag is newer. Pinned at first
	// sight so a re-send keeps its kind; dropped once delivered or untagged.
	TagJoinAt map[string]time.Time `json:"tagJoinAt,omitempty"`
}

// NewState an empty state
func NewState() *State {
	return &State{Feed: map[string]string{}, Opened: map[string]string{}, Rules: map[string]*RuleState{}, Seen: map[string]time.Time{}}
}

func newRuleState() *RuleState {
	return &RuleState{Baselines: map[string]string{}, Members: map[string]bool{}, JoinStartedAt: map[string]time.Time{},
		TagWatched: map[string]bool{}, TagJoinAt: map[string]time.Time{}}
}

// Clone a deep copy; nil gives an empty state
func (s *State) Clone() *State {
	out := NewState()
	if s == nil {
		return out
	}
	out.FeedInitialised = s.FeedInitialised
	for k, v := range s.Feed {
		out.Feed[k] = v
	}
	for k, v := range s.Opened {
		out.Opened[k] = v
	}
	for k, v := range s.Seen {
		out.Seen[k] = v
	}
	for id, rs := range s.Rules {
		c := newRuleState()
		if rs != nil {
			c.LastActive = rs.LastActive
			c.Trigger = rs.Trigger
			for k, v := range rs.Baselines {
				c.Baselines[k] = v
			}
			for k, v := range rs.Members {
				c.Members[k] = v
			}
			for k, v := range rs.JoinStartedAt {
				c.JoinStartedAt[k] = v
			}
			for k, v := range rs.TagWatched {
				c.TagWatched[k] = v
			}
			for k, v := range rs.TagJoinAt {
				c.TagJoinAt[k] = v
			}
		}
		out.Rules[id] = c
	}
	return out
}

// rule the rule's state, created when absent
func (s *State) rule(id string) *RuleState {
	rs := s.Rules[id]
	if rs == nil {
		rs = newRuleState()
		s.Rules[id] = rs
	}
	return rs
}

// pageKey the baseline key of a page
func pageKey(docID, pageID string) string {
	return docID + "/" + pageID
}
