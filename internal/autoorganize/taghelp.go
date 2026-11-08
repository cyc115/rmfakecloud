package autoorganize

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// Tag help: what each tag a rule reacts to does, as an HTML fragment for the
// tablet's "#" pane (floating-toolbar ask.qmd). It is drawn by a QML Text in
// Text.RichText mode, so it uses only what Qt's rich text draws: tables with
// border/cellpadding/width, bgcolor, <b>, <i>, <br>, <font color> and a
// font-size style. No CSS classes, no flexbox, no images.

// tagHelpGroups the sections, in order
var tagHelpGroups = []string{"MOVE OR DELETE THE DOCUMENT", "CHANGE TAGS", "SEND TO THE VAULT", "STOP RULES"}

// TagHelpRow one line of the table
type TagHelpRow struct {
	Group string `json:"group"`
	Tag   string `json:"tag"`
	On    string `json:"on"`
	// Where folder or document names (merged across rules with the same effect)
	Where []string `json:"where"`
	// Limits a second "Where" line: PDFs & books, notebooks, not pinned
	Limits string `json:"limits"`
	What   string `json:"what"`
}

// TagHelp the rows and the "automatic" notes of the enabled rules, in rule
// order, plus the fixed rows (-inbox, no automation)
func TagHelp(cfg *automation.Config) (rows []TagHelpRow, auto []string) {
	index := map[string]int{}
	add := func(r TagHelpRow) {
		k := r.Group + "\x00" + r.Tag + "\x00" + r.On + "\x00" + r.Limits + "\x00" + r.What
		if i, ok := index[k]; ok {
			for _, w := range r.Where {
				if !hasStr(rows[i].Where, w) {
					rows[i].Where = append(rows[i].Where, w)
				}
			}
			return
		}
		index[k] = len(rows)
		rows = append(rows, r)
	}
	extracts := false
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		if !r.Enabled || len(r.Actions) == 0 {
			continue
		}
		a := r.Actions[0]
		if a.Type == automation.ActionExtractTasks {
			extracts = true
		}
		if r.IsPage() {
			t := r.When.Trigger
			where := placeNames(r)
			switch {
			case t.Type == automation.TriggerPageTagged && automation.NormalizeTag(t.Tag) != "":
				add(TagHelpRow{Group: tagHelpGroups[2], Tag: "#" + strings.TrimSpace(t.Tag), On: "a page or the doc",
					Where: where, Limits: kindLimit(r), What: pageActionText(a.Type)})
			case t.Type == automation.TriggerPageChanged:
				auto = append(auto, fmt.Sprintf("Changed pages in %s%s go to the vault%s.",
					joinNames(where), kindSuffix(r), map[bool]string{true: " with their to-dos", false: ""}[a.Type == automation.ActionExtractTasks]))
			}
			continue
		}
		var tags []string
		other := ""
		for _, t := range r.When.AllTriggers() {
			if t.Type == automation.TriggerTagIs {
				tags = append(tags, strings.TrimSpace(t.Tag))
			} else if other == "" {
				other = triggerWhen(t)
			}
		}
		if len(tags) == 0 {
			if a.Type == automation.ActionAddTag {
				auto = append(auto, fmt.Sprintf("<b>%s</b> is added when %s.", esc(a.Tag), other))
			}
			continue
		}
		group, what := tagHelpGroups[0], ""
		switch a.Type {
		case automation.ActionMove:
			what = moveText(a.To)
		case automation.ActionTrash:
			what = "Moves it to the <b>Trash</b>. Restore it from the Trash, or press Undo."
		case automation.ActionAddTag:
			group, what = tagHelpGroups[1], fmt.Sprintf("Adds the tag <b>%s</b>.", esc(a.Tag))
		case automation.ActionRemoveTag:
			group, what = tagHelpGroups[1], fmt.Sprintf("Removes the tag <b>%s</b>.", esc(a.Tag))
		default:
			continue
		}
		if other != "" {
			what += " Also happens by itself when " + other + "."
		}
		if extraConditions(r) {
			what += " Other conditions apply."
		}
		where := placeNames(r)
		if len(where) == 1 && where[0] != "anywhere" && (r.TagAction() != nil || r.Trash() != nil) {
			where[0] += " and folders inside"
		}
		limits := kindLimit(r)
		if r.SkipsPinned() {
			limits = joinLimit(limits, "not pinned")
		}
		for _, t := range tags {
			add(TagHelpRow{Group: group, Tag: "#" + t, On: "doc or any page", Where: where, Limits: limits, What: what})
		}
	}
	if extracts {
		add(TagHelpRow{Group: tagHelpGroups[2], Tag: "-inbox", On: "a line, heading or page top",
			Where: []string{"pages that send to-dos"}, What: "That line, block or page is not added to the inbox."})
	}
	add(TagHelpRow{Group: tagHelpGroups[3], Tag: "no automation", On: "doc (tag menu)",
		Where: []string{"anywhere"}, What: "Every rule skips this document."})
	sort.SliceStable(rows, func(i, j int) bool { return groupRank(rows[i].Group) < groupRank(rows[j].Group) })
	return rows, auto
}

// TagHelpHTML the table as Qt rich text: one table (Qt ignores <col>, and
// widths only line up within a table) whose section titles are full-width
// rows. No cell fills: Qt paints a cell's background inside its padding only,
// which leaves a white ring.
func TagHelpHTML(cfg *automation.Config) string {
	rows, auto := TagHelp(cfg)
	var b strings.Builder
	b.WriteString(`<table width="100%" border="1" cellspacing="0" cellpadding="10" style="border-collapse:collapse; border-color:#000000;"><tr>`)
	for i, h := range []string{"Write", "On", "Where", "What happens"} {
		fmt.Fprintf(&b, `<td width="%d%%"><span style="font-size:23px;"><b>%s</b></span></td>`, []int{23, 16, 22, 39}[i], strings.ToUpper(h))
	}
	b.WriteString(`</tr>`)
	group := ""
	for _, r := range rows {
		if r.Group != group {
			group = r.Group
			fmt.Fprintf(&b, `<tr><td colspan="4"><b>%s</b></td></tr>`, group)
		}
		where := esc(joinNames(r.Where))
		if r.Limits != "" {
			where += "<br><i>" + esc(r.Limits) + "</i>"
		}
		fmt.Fprintf(&b, `<tr><td><span style="font-size:32px;"><b>%s</b></span></td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			esc(r.Tag), esc(r.On), where, r.What)
	}
	b.WriteString(`</table>`)
	notes := append([]string{}, auto...)
	notes = append(notes, "A written #tag acts about 20 s after the tablet syncs. Tags of 5+ letters may have one letter wrong.")
	fmt.Fprintf(&b, `<p style="margin-top:18px;">%s</p>`, strings.Join(notes, "<br>"))
	return b.String()
}

func esc(s string) string { return html.EscapeString(s) }

func hasStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func groupRank(g string) int {
	for i, x := range tagHelpGroups {
		if x == g {
			return i
		}
	}
	return len(tagHelpGroups)
}

// placeNames the rule's places by their cached names; the root is "anywhere"
func placeNames(r *automation.Rule) []string {
	var out []string
	for _, p := range r.When.In {
		n := strings.TrimSpace(p.Name)
		if p.Kind == automation.PlaceFolder && p.ID == RootID {
			n = "anywhere"
		} else if n == "" {
			n = "?"
		}
		if !hasStr(out, n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 || hasStr(out, "anywhere") {
		return []string{"anywhere"}
	}
	return out
}

func joinNames(xs []string) string { return strings.Join(xs, ", ") }

func kindLimit(r *automation.Rule) string {
	switch r.Kind() {
	case automation.KindPdfEpub:
		return "PDFs & books"
	case automation.KindNotebooks:
		return "notebooks"
	}
	return ""
}

func kindSuffix(r *automation.Rule) string {
	if k := kindLimit(r); k != "" {
		return " (" + k + ")"
	}
	return ""
}

func joinLimit(a, b string) string {
	if a == "" {
		return b
	}
	return a + ", " + b
}

// extraConditions conditions other than kind_is narrow the rule further
func extraConditions(r *automation.Rule) bool {
	for _, c := range r.Conditions {
		if c.Type != automation.CondKindIs {
			return true
		}
	}
	return false
}

func moveText(to *automation.Destination) string {
	if to == nil {
		return "Moves it."
	}
	if cm := to.CreateIfMissing; cm != nil {
		if cm.Parent == ParentSource {
			return fmt.Sprintf("Moves it to <b>%s</b> inside that folder.", esc(cm.Name))
		}
		return fmt.Sprintf("Moves it to <b>%s</b>.", esc(cm.Name))
	}
	name := to.Name
	if to.ID == RootID {
		name = RootName
	}
	return fmt.Sprintf("Moves it to <b>%s</b>.", esc(name))
}

func pageActionText(t string) string {
	if t == automation.ActionSaveNotes {
		return "Saves the page to its vault note (no to-dos)."
	}
	return "Saves the page to its vault note and adds its new to-dos to the inbox."
}

// triggerWhen a non-tag trigger, completing "... when <it>"
func triggerWhen(t automation.Trigger) string {
	switch t.Type {
	case automation.TriggerFinishedReading:
		return "you finish reading"
	case automation.TriggerReadProgress:
		return fmt.Sprintf("you have read %d%%", t.Percent)
	case automation.TriggerNotOpened:
		return fmt.Sprintf("it sits unopened for %d days", t.Days)
	case automation.TriggerNotModified:
		return fmt.Sprintf("it sits unchanged for %d days", t.Days)
	case automation.TriggerAgeSinceCreated:
		return fmt.Sprintf("it is %d days old", t.Days)
	case automation.TriggerCreatedTime:
		if t.Compare == "within" {
			return fmt.Sprintf("a document arrives (first %d %s)", t.Amount, t.Unit)
		}
		return fmt.Sprintf("it is older than %d %s", t.Amount, t.Unit)
	case automation.TriggerNameMatches:
		return "its name matches " + esc(t.Pattern)
	}
	return "its other trigger matches"
}
