package pagewatch

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
}

// RuleState one pipeline rule's view of the tree
type RuleState struct {
	// Baselines "doc/page" → the hash last delivered for this rule
	Baselines map[string]string `json:"baselines"`
	// Members docs whose join (transcribe_only of every page) completed
	Members map[string]bool `json:"members"`
}

// NewState an empty state
func NewState() *State {
	return &State{Feed: map[string]string{}, Opened: map[string]string{}, Rules: map[string]*RuleState{}}
}

func newRuleState() *RuleState {
	return &RuleState{Baselines: map[string]string{}, Members: map[string]bool{}}
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
	for id, rs := range s.Rules {
		c := newRuleState()
		if rs != nil {
			for k, v := range rs.Baselines {
				c.Baselines[k] = v
			}
			for k, v := range rs.Members {
				c.Members[k] = v
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
