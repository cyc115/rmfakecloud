package autoorganize

import (
	"sort"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/automation"
)

// TagSpellings normalized tag -> the spelling to write: as an enabled rule names it, else as a
// document carries it (a handwritten "#extract" written as "Extract" on the tablet stays one tag)
func TagSpellings(cfg *automation.Config, snap *Snapshot, content func(*DocSnap) ContentInfo) map[string]string {
	out := map[string]string{}
	put := func(tag string) {
		tag = strings.TrimSpace(tag)
		if k := NormalizeTag(tag); k != "" {
			if _, ok := out[k]; !ok {
				out[k] = tag
			}
		}
	}
	if cfg != nil {
		for _, r := range cfg.Rules {
			if !r.Enabled {
				continue
			}
			for _, t := range r.When.AllTriggers() {
				if t.Type == automation.TriggerPageTagged || t.Type == automation.TriggerTagIs {
					put(t.Tag)
				}
			}
			for _, c := range r.Conditions {
				if c.Type == automation.CondPageTagIs || c.Type == automation.CondTagIs {
					put(c.Tag)
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
			for _, t := range ci.Tags {
				put(t)
			}
		}
	}
	return out
}

// KnownTags the tags a handwritten #tag may become (NormalizeTag, sorted,
// unique): the tags of enabled rules (page_tagged and tag_is triggers, page_tag_is and tag_is conditions) and
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
			for _, t := range r.When.AllTriggers() {
				if t.Type == automation.TriggerPageTagged || t.Type == automation.TriggerTagIs {
					add(t.Tag)
				}
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
