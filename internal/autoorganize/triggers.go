package autoorganize

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// Skip reasons, shown as chips in Preview and counted in Runs
const (
	SkipInTrash           = "in_trash"
	SkipContentUnreadable = "content_unreadable"
	SkipNoAutomation      = "no_automation"
	SkipPinned            = "pinned"
	SkipAlreadyInDest     = "already_in_destination"
	SkipOutOfScope        = "out_of_scope"
	SkipPageCountUnknown  = "page_count_unknown"
	SkipTimeUnknown       = "time_unknown"
	SkipHeld              = "held"
	SkipCycleGuard        = "cycle_guard"
	SkipClaimed           = "claimed"
	SkipInvalidRule       = "invalid_rule"
)

// evaluation of one trigger against one document
type evaluation struct {
	match bool
	// skip set when the document cannot be evaluated (never guessed)
	skip       string
	skipDetail string
	// inputs the trigger specific values a hold is keyed on (§6.4)
	inputs map[string]string
	// detail human readable trigger values for Preview and the log
	detail string
}

// parseMillis "" and "0" both mean never
func parseMillis(s string) (time.Time, bool) {
	if s == "" || s == "0" {
		return time.Time{}, false
	}
	t, err := models.ToTime(s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ageAnchor createdTime, else lastModified, else first seen by the sweeper
func ageAnchor(d *DocSnap, fs *FirstSeen, now time.Time) (time.Time, string) {
	if t, ok := parseMillis(d.CreatedTime); ok {
		return t, "createdTime"
	}
	if t, ok := parseMillis(d.LastModified); ok {
		return t, "lastModified"
	}
	if fs != nil {
		return time.UnixMilli(fs.At), "firstSeen"
	}
	return now, "firstSeen"
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func daysSince(t, now time.Time) float64 {
	return now.Sub(t).Hours() / 24
}

// mergeEval combines a trigger and its conditions, in order (ruling a′):
// inputs are the union over every evaluation, so a hold keeps the condition
// inputs whatever matched. match is the AND, skip/skipDetail the first skip,
// detail the sentences joined with " · "; these stop at the first skip or
// non-match, so a later skip never outranks an earlier non-match.
func mergeEval(evs ...evaluation) evaluation {
	if len(evs) == 1 {
		return evs[0]
	}
	out := evaluation{match: true}
	var parts []string
	done := false
	for _, ev := range evs {
		if ev.inputs != nil {
			if out.inputs == nil {
				out.inputs = map[string]string{}
			}
			for k, v := range ev.inputs {
				out.inputs[k] = v
			}
		}
		if done {
			continue
		}
		if ev.detail != "" {
			parts = append(parts, ev.detail)
		}
		if ev.skip != "" {
			out.match = false
			out.skip, out.skipDetail = ev.skip, ev.skipDetail
			done = true
		} else if !ev.match {
			out.match = false
			done = true
		}
	}
	out.detail = strings.Join(parts, " · ")
	return out
}

// evalRule evaluates r's trigger, then every document condition in stored
// order (Rule.DocConditions; kind_is is checkDoc's guard), and merges them
// (mergeEval): the inputs of all, the outcome of the first skip or
// non-match. It does not apply the common guards (checkDoc does).
func evalRule(r *automation.Rule, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	t := &r.When.Trigger
	eval, ok := evaluators[t.Type]
	if !ok {
		return evaluation{skip: SkipInvalidRule, skipDetail: "unknown trigger " + t.Type}
	}
	tp := t.Params
	evs := []evaluation{eval(&tp, d, ci, fs, now)}
	for _, c := range r.DocConditions() {
		ceval, ok := conditionEvaluators[c.Type]
		if !ok {
			evs = append(evs, evaluation{skip: SkipInvalidRule, skipDetail: "unknown condition " + c.Type})
			continue
		}
		p := c.Params
		evs = append(evs, ceval(&p, d, ci, fs, now))
	}
	return mergeEval(evs...)
}

// DocConditionsMatch the document conditions of a page rule, for the page
// watch: kind_is, then the conditions with both a document and a page subject
// (tag_is, tag_is_not, name_matches) in stored order. page_tag_is is not one:
// the page watch reads it through Rule.PageTags. in false: the doc is outside
// the rule. skip non-empty: a condition cannot be evaluated (an unknown type,
// a document-only condition, a bad pattern), and the doc must be left alone,
// never guessed in or out.
func DocConditionsMatch(r *automation.Rule, d *DocSnap, ci *ContentInfo, now time.Time) (in bool, skip string) {
	for _, c := range r.Conditions {
		// X4: a blank kind (a hand edit) is all, never "kind must be blank"
		if c.Type == automation.CondKindIs && c.Kind != "" {
			if out, _ := evalKind(c.Kind, ci); out {
				return false, ""
			}
		}
	}
	for _, c := range r.Conditions {
		if c.Type == automation.CondKindIs || c.Type == automation.CondPageTagIs {
			continue
		}
		spec, known := automation.Conditions[c.Type]
		ceval, ok := conditionEvaluators[c.Type]
		if !known || !ok || !hasSubjects(spec, automation.SubjectDocument, automation.SubjectPage) {
			return false, "condition " + c.Type + " cannot be evaluated for pages"
		}
		p := c.Params
		ev := ceval(&p, d, ci, nil, now)
		if ev.skip != "" {
			return false, ev.skip + ": " + ev.skipDetail
		}
		if !ev.match {
			return false, ""
		}
	}
	return true, ""
}

func hasSubjects(s *automation.Spec, want ...string) bool {
	for _, w := range want {
		found := false
		for _, sub := range s.Subjects {
			found = found || sub == w
		}
		if !found {
			return false
		}
	}
	return true
}

// evalKind the kind_is guard (v1 scope): skip with the out_of_scope detail
// when the document is not of kind. All and "" (unset, as X4 treats it for
// pages) cover every document; any other value must equal the document's kind.
func evalKind(kind string, ci *ContentInfo) (skip bool, detail string) {
	got := ci.Kind()
	if kind == automation.KindAll || kind == "" || got == kind {
		return false, ""
	}
	what := "notebook"
	if got == automation.KindPdfEpub {
		what = strings.ToLower(ci.FileType)
	} else if got == "other" {
		what = "file type “" + ci.FileType + "”"
	}
	applies := "PDF & EPUB"
	if kind == automation.KindNotebooks {
		applies = "notebooks"
	}
	return true, what + ", rule applies to " + applies
}

// evalNotOpened not_opened_for_days: never opened, or last opened at least
// Days ago. The age anchor stays an input (holds are keyed on it, with or
// without the min-age condition).
func evalNotOpened(p *automation.Params, d *DocSnap, _ *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	anchor, _ := ageAnchor(d, fs, now)
	ev := evaluation{inputs: map[string]string{"lastOpened": d.LastOpened, "ageAnchor": strconv.FormatInt(anchor.UnixMilli(), 10)}}
	opened, ok := parseMillis(d.LastOpened)
	if ok {
		ev.detail = fmt.Sprintf("last opened %s ago", humanDuration(now.Sub(opened)))
		ev.match = daysSince(opened, now) >= float64(p.Days)
	} else {
		ev.detail = "never opened"
		ev.match = true
	}
	return ev
}

// evalMinAge min_age_days: the document is at least Days old (age anchor)
func evalMinAge(p *automation.Params, d *DocSnap, _ *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	anchor, src := ageAnchor(d, fs, now)
	return evaluation{
		inputs: map[string]string{"ageAnchor": strconv.FormatInt(anchor.UnixMilli(), 10)},
		detail: fmt.Sprintf("%s old (%s)", humanDuration(now.Sub(anchor)), src),
		match:  daysSince(anchor, now) >= float64(p.Days),
	}
}

// evalNotModified not_modified_for_days; a missing lastModified is a skip, never a guess
func evalNotModified(p *automation.Params, d *DocSnap, _ *ContentInfo, _ *FirstSeen, now time.Time) evaluation {
	ev := evaluation{inputs: map[string]string{"lastModified": d.LastModified}}
	mod, ok := parseMillis(d.LastModified)
	if !ok {
		ev.skip = SkipTimeUnknown
		ev.skipDetail = "lastModified missing or invalid"
		return ev
	}
	ev.detail = fmt.Sprintf("last modified %s ago", humanDuration(now.Sub(mod)))
	ev.match = daysSince(mod, now) >= float64(p.Days)
	return ev
}

// evalNameMatches name_matches: glob or /regex/, case-insensitive (CompilePattern)
func evalNameMatches(p *automation.Params, d *DocSnap, _ *ContentInfo, _ *FirstSeen, _ time.Time) evaluation {
	ev := evaluation{inputs: map[string]string{"name": d.Name}}
	re, err := CompilePattern(p.Pattern)
	if err != nil {
		ev.skip = SkipInvalidRule
		ev.skipDetail = err.Error()
		return ev
	}
	ev.match = re.MatchString(d.Name)
	ev.detail = fmt.Sprintf("name matches “%s”", p.Pattern)
	return ev
}

// evalTagIs tag_is: a document-level tag equal to Tag after normalisation
func evalTagIs(p *automation.Params, _ *DocSnap, ci *ContentInfo, _ *FirstSeen, _ time.Time) evaluation {
	norm := make([]string, 0, len(ci.Tags))
	for _, tag := range ci.Tags {
		norm = append(norm, NormalizeTag(tag))
	}
	sort.Strings(norm)
	ev := evaluation{inputs: map[string]string{"tags": strings.Join(norm, ",")}}
	ev.match = ci.HasTag(p.Tag)
	ev.detail = fmt.Sprintf("tagged “%s”", NormalizeTag(p.Tag))
	return ev
}

// evalTagIsNot tag_is_not: the negation of tag_is, on the same inputs
func evalTagIsNot(p *automation.Params, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	ev := evalTagIs(p, d, ci, fs, now)
	ev.match = !ev.match
	ev.detail = fmt.Sprintf("not tagged “%s”", NormalizeTag(p.Tag))
	return ev
}

// evalAgeSinceCreated age_since_created, from the age anchor
func evalAgeSinceCreated(p *automation.Params, d *DocSnap, _ *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	anchor, src := ageAnchor(d, fs, now)
	ev := evaluation{inputs: map[string]string{"ageAnchor": strconv.FormatInt(anchor.UnixMilli(), 10), "ageSource": src}}
	ev.detail = fmt.Sprintf("arrived %s ago (%s)", humanDuration(now.Sub(anchor)), src)
	ev.match = daysSince(anchor, now) >= float64(p.Days)
	return ev
}

// evalTime created_time and modified_time: the time field is in the last
// Amount Unit (compare within) or more than Amount Unit ago (older). A
// missing or invalid time is a skip, never a guess: created_time does not
// fall back to lastModified or first-seen the way the age anchor does, since
// either would make an old document look new.
func evalTime(p *automation.Params, raw, key, verb string, now time.Time) evaluation {
	ev := evaluation{inputs: map[string]string{key: raw}}
	at, ok := parseMillis(raw)
	if !ok {
		ev.skip = SkipTimeUnknown
		ev.skipDetail = key + " missing or invalid"
		return ev
	}
	win := p.Window()
	if win <= 0 {
		ev.skip = SkipInvalidRule
		ev.skipDetail = "unknown time unit “" + p.Unit + "”"
		return ev
	}
	age := now.Sub(at)
	ev.detail = fmt.Sprintf("%s %s ago", verb, humanDuration(age))
	if p.Compare == automation.CompareOlder {
		ev.match = age > win
	} else {
		ev.match = age <= win
	}
	return ev
}

// evalCreatedTime created_time, from createdTime
func evalCreatedTime(p *automation.Params, d *DocSnap, _ *ContentInfo, _ *FirstSeen, now time.Time) evaluation {
	return evalTime(p, d.CreatedTime, "createdTime", "created", now)
}

// evalModifiedTime modified_time, from lastModified
func evalModifiedTime(p *automation.Params, d *DocSnap, _ *ContentInfo, _ *FirstSeen, now time.Time) evaluation {
	return evalTime(p, d.LastModified, "lastModified", "changed", now)
}

// evalNotAMoveTrigger the evaluator of triggers the move planner does not own
// (page_changed, page_tagged): a skip, never a match
func evalNotAMoveTrigger(typ string) evalFunc {
	return func(_ *automation.Params, _ *DocSnap, _ *ContentInfo, _ *FirstSeen, _ time.Time) evaluation {
		return evaluation{skip: SkipOutOfScope, skipDetail: typ + " is evaluated by the page watch, not the move planner"}
	}
}

// evalPinnedIs pinned_is
func evalPinnedIs(p *automation.Params, d *DocSnap, _ *ContentInfo, _ *FirstSeen, _ time.Time) evaluation {
	ev := evaluation{inputs: map[string]string{"pinned": strconv.FormatBool(d.Pinned)}}
	ev.match = d.Pinned == p.Pinned
	if d.Pinned {
		ev.detail = "pinned"
	} else {
		ev.detail = "not pinned"
	}
	return ev
}

// readingState the part finished_reading and read_progress share. Metadata
// lastOpenedPage is authoritative (0-indexed); the .content value is logged
// for drift. ok false: ev is final (a skip, or never opened).
func readingState(p *automation.Params, d *DocSnap, ci *ContentInfo, now time.Time) (ev evaluation, settled, ok bool) {
	ev = evaluation{inputs: map[string]string{
		"pageCount":      strconv.Itoa(ci.PageCount),
		"lastOpenedPage": strconv.Itoa(d.LastOpenedPage),
		"lastOpened":     d.LastOpened,
	}}
	if ci.PageCount <= 0 {
		ev.skip = SkipPageCountUnknown
		ev.skipDetail = "pageCount 0, the tablet has not synced the content yet"
		return ev, false, false
	}
	ev.detail = fmt.Sprintf("%d pp · last page %d of %d (content: %d)", ci.PageCount, d.LastOpenedPage+1, ci.PageCount, ci.LastOpenedPage+1)
	opened, ok := parseMillis(d.LastOpened)
	if !ok {
		ev.detail += " · never opened"
		return ev, false, false
	}
	since := now.Sub(opened)
	ev.detail += " · settled " + humanDuration(since)
	return ev, since >= time.Duration(p.SettleHours)*time.Hour, true
}

// evalFinished finished_reading: at least MinPages, on the last page, settled
func evalFinished(p *automation.Params, d *DocSnap, ci *ContentInfo, _ *FirstSeen, now time.Time) evaluation {
	ev, settled, ok := readingState(p, d, ci, now)
	if ok {
		ev.match = ci.PageCount >= p.MinPages && d.LastOpenedPage == ci.PageCount-1 && settled
	}
	return ev
}

// evalProgress read_progress: at least Percent read, settled
func evalProgress(p *automation.Params, d *DocSnap, ci *ContentInfo, _ *FirstSeen, now time.Time) evaluation {
	ev, settled, ok := readingState(p, d, ci, now)
	if !ok {
		return ev
	}
	progress := float64(d.LastOpenedPage+1) / float64(ci.PageCount)
	if progress > 1 {
		progress = 1
	}
	ev.detail = fmt.Sprintf("%d%% read · ", int(math.Floor(progress*100))) + ev.detail
	ev.match = progress*100 >= float64(p.Percent) && settled
	return ev
}

// docCheck the outcome of the common guards plus the rule
type docCheck struct {
	skip   string
	detail string
	ev     evaluation
}

// checkDoc applies the §4.0 guards that depend only on the document and the
// rule (trash, unreadable, no-automation, pinned, already in the destination,
// kind), then the rule (evalRule). Hold, cycle guard and claims are the
// planner's. The same function re-checks a document inside the move closure.
func checkDoc(r *automation.Rule, destID string, d *DocSnap, ci *ContentInfo, fs *FirstSeen, locate func(string) Location, now time.Time) docCheck {
	if locate(d.ID) == LocInTrash {
		return docCheck{skip: SkipInTrash, detail: "in Trash"}
	}
	if !ci.Readable() {
		return docCheck{skip: SkipContentUnreadable, detail: ci.Error + "; cannot check the no-automation tag"}
	}
	if ci.HasNoAutomationTag() {
		return docCheck{skip: SkipNoAutomation, detail: "tagged “no automation”"}
	}
	if d.Pinned && r.SkipsPinned() {
		return docCheck{skip: SkipPinned, detail: "pinned"}
	}
	if destID != "" && d.Parent == destID {
		return docCheck{skip: SkipAlreadyInDest, detail: "already in the destination"}
	}
	if skip, detail := evalKind(r.Kind(), ci); skip {
		return docCheck{skip: SkipOutOfScope, detail: detail}
	}
	ev := evalRule(r, d, ci, fs, now)
	if ev.skip != "" {
		return docCheck{skip: ev.skip, detail: ev.skipDetail, ev: ev}
	}
	return docCheck{ev: ev}
}

func sameInputs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
