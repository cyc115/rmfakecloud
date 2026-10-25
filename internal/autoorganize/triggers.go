package autoorganize

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

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

// evalTrigger evaluates r's trigger. It does not apply the common guards.
func evalTrigger(r *Rule, d *DocSnap, ci *ContentInfo, fs *FirstSeen, now time.Time) evaluation {
	t := &r.Trigger
	switch t.Type {
	case TriggerFinishedReading, TriggerReadProgress:
		return evalReading(t, d, ci, now)

	case TriggerNotOpened:
		anchor, src := ageAnchor(d, fs, now)
		ev := evaluation{inputs: map[string]string{"lastOpened": d.LastOpened, "ageAnchor": strconv.FormatInt(anchor.UnixMilli(), 10)}}
		age := daysSince(anchor, now)
		opened, ok := parseMillis(d.LastOpened)
		if ok {
			ev.detail = fmt.Sprintf("last opened %s ago", humanDuration(now.Sub(opened)))
			ev.match = daysSince(opened, now) >= float64(t.Days)
		} else {
			ev.detail = "never opened"
			ev.match = true
		}
		if t.MinAgeEnabled {
			ev.detail += fmt.Sprintf(" · %s old (%s)", humanDuration(now.Sub(anchor)), src)
			if age < float64(t.MinAgeDays) {
				ev.match = false
			}
		}
		return ev

	case TriggerNotModified:
		ev := evaluation{inputs: map[string]string{"lastModified": d.LastModified}}
		mod, ok := parseMillis(d.LastModified)
		if !ok {
			ev.skip = SkipTimeUnknown
			ev.skipDetail = "lastModified missing or invalid"
			return ev
		}
		ev.detail = fmt.Sprintf("last modified %s ago", humanDuration(now.Sub(mod)))
		ev.match = daysSince(mod, now) >= float64(t.Days)
		return ev

	case TriggerNameMatches:
		ev := evaluation{inputs: map[string]string{"name": d.Name}}
		re, err := CompilePattern(t.Pattern)
		if err != nil {
			ev.skip = SkipInvalidRule
			ev.skipDetail = err.Error()
			return ev
		}
		ev.match = re.MatchString(d.Name)
		ev.detail = fmt.Sprintf("name matches “%s”", t.Pattern)
		return ev

	case TriggerTagIs:
		norm := make([]string, 0, len(ci.Tags))
		for _, tag := range ci.Tags {
			norm = append(norm, NormalizeTag(tag))
		}
		sort.Strings(norm)
		ev := evaluation{inputs: map[string]string{"tags": strings.Join(norm, ",")}}
		ev.match = ci.HasTag(t.Tag)
		ev.detail = fmt.Sprintf("tagged “%s”", NormalizeTag(t.Tag))
		return ev

	case TriggerAgeSinceCreated:
		anchor, src := ageAnchor(d, fs, now)
		ev := evaluation{inputs: map[string]string{"ageAnchor": strconv.FormatInt(anchor.UnixMilli(), 10), "ageSource": src}}
		ev.detail = fmt.Sprintf("arrived %s ago (%s)", humanDuration(now.Sub(anchor)), src)
		ev.match = daysSince(anchor, now) >= float64(t.Days)
		return ev

	case TriggerPinnedIs:
		ev := evaluation{inputs: map[string]string{"pinned": strconv.FormatBool(d.Pinned)}}
		ev.match = d.Pinned == t.Pinned
		if d.Pinned {
			ev.detail = "pinned"
		} else {
			ev.detail = "not pinned"
		}
		return ev
	}
	return evaluation{skip: SkipInvalidRule, skipDetail: "unknown trigger " + t.Type}
}

// evalReading finished_reading and read_progress. Metadata lastOpenedPage is
// authoritative (0-indexed); the .content value is logged for drift.
func evalReading(t *Trigger, d *DocSnap, ci *ContentInfo, now time.Time) evaluation {
	ev := evaluation{inputs: map[string]string{
		"pageCount":      strconv.Itoa(ci.PageCount),
		"lastOpenedPage": strconv.Itoa(d.LastOpenedPage),
		"lastOpened":     d.LastOpened,
	}}
	if ci.PageCount <= 0 {
		ev.skip = SkipPageCountUnknown
		ev.skipDetail = "pageCount 0, the tablet has not synced the content yet"
		return ev
	}
	ev.detail = fmt.Sprintf("%d pp · last page %d of %d (content: %d)", ci.PageCount, d.LastOpenedPage+1, ci.PageCount, ci.LastOpenedPage+1)
	opened, ok := parseMillis(d.LastOpened)
	if !ok {
		ev.detail += " · never opened"
		return ev
	}
	since := now.Sub(opened)
	ev.detail += " · settled " + humanDuration(since)
	settled := since >= time.Duration(t.SettleHours)*time.Hour

	if t.Type == TriggerFinishedReading {
		ev.match = ci.PageCount >= t.MinPages && d.LastOpenedPage == ci.PageCount-1 && settled
		return ev
	}
	progress := float64(d.LastOpenedPage+1) / float64(ci.PageCount)
	if progress > 1 {
		progress = 1
	}
	ev.detail = fmt.Sprintf("%d%% read · ", int(math.Floor(progress*100))) + ev.detail
	ev.match = progress*100 >= float64(t.Percent) && settled
	return ev
}

// docCheck the outcome of the common guards plus the trigger
type docCheck struct {
	skip   string
	detail string
	ev     evaluation
}

// checkDoc applies the §4.0 guards that depend only on the document and the
// rule, then the trigger. Hold, cycle guard and claims are the planner's.
// The same function re-checks a document inside the move closure.
func checkDoc(r *Rule, destID string, d *DocSnap, ci *ContentInfo, fs *FirstSeen, locate func(string) Location, now time.Time) docCheck {
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
	kind := ci.Kind()
	if r.Scope != ScopeAll && kind != r.Scope {
		what := "notebook"
		if kind == ScopePdfEpub {
			what = strings.ToLower(ci.FileType)
		} else if kind == "other" {
			what = "file type “" + ci.FileType + "”"
		}
		applies := "PDF & EPUB"
		if r.Scope == ScopeNotebooks {
			applies = "notebooks"
		}
		return docCheck{skip: SkipOutOfScope, detail: what + ", rule applies to " + applies}
	}
	ev := evalTrigger(r, d, ci, fs, now)
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
