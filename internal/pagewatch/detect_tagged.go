package pagewatch

import "github.com/ddvk/rmfakecloud/internal/autoorganize"

// taggedDoc a doc of a page_tagged rule: only its pages that carry the tag
// (Trigger.Tag, ContentInfo.PageTag) count.
//
//   - Newly tagged (no baseline): one edit with an empty before, the whole
//     page, when the rule already had the doc at an earlier sweep
//     (TagWatched) or the doc is new since the rule last looked (R15). If not,
//     the tag pre-existed the rule's look at the doc (the rule's first
//     activation, a doc moved in, a widened watch): the page is pinned at that
//     look (TagJoinAt: LastActive, or now at the first activation) and sent
//     as transcribe_only unless its blob or its tag is newer than the pin (R12).
//   - Tagged with a baseline: an edit per change, before = the baseline.
//   - Untagged: no events; baseline and pin are dropped, so a re-tag is new.
//   - Gone from the doc with a baseline: page_deleted, as for page_changed.
//
// Trace ids use the page_changed key (R17); rule ids keep rules apart.
func (d *detector) taggedDoc(r *autoorganize.Rule, base *RuleState, held []string, v *docView) {
	docID, ruleID := v.snap.ID, r.ID
	watched := base.TagWatched[docID] || (!base.LastActive.IsZero() && d.seenAt(docID).After(base.LastActive))
	if !base.TagWatched[docID] {
		d.always(func(s *State) { s.rule(ruleID).TagWatched[docID] = true })
	}
	joinAt := d.in.Now
	if !base.LastActive.IsZero() {
		joinAt = base.LastActive
	}
	current := map[string]bool{}
	var drop []string
	for _, p := range v.snap.Pages {
		current[p.ID] = true
		key := pageKey(docID, p.ID)
		prev := base.Baselines[key]
		pin, pinned := base.TagJoinAt[key]
		tagAt, tagged := v.ci.PageTag(p.ID, r.Trigger.Tag)
		if !tagged {
			if prev != "" || pinned {
				drop = append(drop, key)
			}
			continue
		}
		if prev == "" && !pinned && !watched {
			pin, pinned = joinAt, true
			d.always(func(s *State) { s.rule(ruleID).TagJoinAt[key] = joinAt })
		}
		if p.Hash == "" || p.Hash == prev {
			continue
		}
		at, ok := d.age(docID, ruleID, p)
		if !ok {
			continue
		}
		kind := KindEdit
		if prev == "" && pinned && !at.After(pin) && !tagAt.After(pin) {
			kind = KindTranscribeOnly
		}
		d.pageChange(kind, r, v, p, prev, at)
	}
	d.pagesDeleted(r, base, v, held, current)
	for _, page := range pagesOf(base.TagJoinAt)[docID] {
		if !current[page] {
			drop = append(drop, pageKey(docID, page))
		}
	}
	if len(drop) > 0 {
		d.always(func(s *State) {
			rs := s.rule(ruleID)
			for _, key := range drop {
				delete(rs.Baselines, key)
				delete(rs.TagJoinAt, key)
			}
		})
	}
}
