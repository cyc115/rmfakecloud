package autoorganize

import (
	"sort"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// KnownTags the tags a handwritten #tag may become (NormalizeTag, sorted,
// unique): the tags of enabled rules (page_tagged, page_tag_is, tag_is) and
// every page and document tag on the documents outside the Trash
func KnownTags(cfg *automation.Config, snap *Snapshot, content func(*DocSnap) ContentInfo) []string {
	set := map[string]bool{}
	add := func(tag string) {
		if k := NormalizeTag(tag); k != "" {
			set[k] = true
		}
	}
	if cfg != nil {
		for _, r := range cfg.Rules {
			if !r.Enabled {
				continue
			}
			if r.When.Trigger.Type == automation.TriggerPageTagged {
				add(r.When.Trigger.Tag)
			}
			for _, c := range r.Conditions {
				switch c.Type {
				case automation.CondPageTagIs, automation.CondTagIs:
					add(c.Tag)
				}
			}
		}
	}
	if snap != nil && content != nil {
		for _, d := range snap.Docs {
			if d.IsFolder() || snap.Locate(d.Parent) != LocAtRoot {
				continue
			}
			ci := content(d)
			for tag := range ci.DocTags {
				add(tag)
			}
			for _, tags := range ci.PageTags {
				for tag := range tags {
					add(tag)
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
