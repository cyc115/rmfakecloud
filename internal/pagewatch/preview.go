package pagewatch

import (
	"sort"
	"time"

	"github.com/ddvk/rmfakecloud/internal/automation"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
)

// PagePreview what a page rule covers now (P5): the documents it watches and
// their pages. It does not show pending events.
type PagePreview struct {
	// Subject always "page"
	Subject string `json:"subject"`

	Docs []PreviewDoc `json:"docs"`

	// Pages the sum of the docs' pages
	Pages int `json:"pages"`

	// MissingWatches places of the rule that are gone or in the trash
	MissingWatches []MissingWatch `json:"missingWatches"`
}

// PreviewDoc one document a page rule covers. Pages counts all the doc's live
// pages, or, for a rule with a page-tag set, only the pages that carry it.
type PreviewDoc struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Pages int    `json:"pages"`
}

// PreviewRule lists the documents and pages the page rule r covers in snap
// (pages attached). It is pure and runs the watcher's own document filter
// (inRule) and page-tag test (pageTags, pageTagged), so the preview cannot
// disagree with what a sweep does. With a page-tag set, a doc with no page
// carrying it is not listed. The rule needs no enabled flag and page watch
// can be off.
func PreviewRule(snap *autoorganize.Snapshot, content func(*autoorganize.DocSnap) autoorganize.ContentInfo, r *automation.Rule) PagePreview {
	out := PagePreview{Subject: automation.SubjectPage, Docs: []PreviewDoc{}, MissingWatches: []MissingWatch{}}
	if snap == nil || !snap.PagesAttached || r == nil || content == nil {
		return out
	}
	d := &detector{
		in:       Input{Snap: snap, Content: content, Now: time.Now()},
		seenSkip: map[SkipNote]bool{},
	}
	d.collectDocs()
	d.missingWatches(r)
	out.MissingWatches = append(out.MissingWatches, d.missing...)
	tags, tagged := pageTags(r)
	for _, v := range d.docs {
		if in, skip := d.inRule(r, v); !in || skip != "" {
			continue
		}
		n := len(v.snap.Pages)
		if tagged {
			n = 0
			for _, p := range v.snap.Pages {
				if _, ok := pageTagged(&v.ci, p.ID, tags); ok {
					n++
				}
			}
			if n == 0 {
				continue
			}
		}
		out.Docs = append(out.Docs, PreviewDoc{ID: v.snap.ID, Name: v.snap.Name, Path: v.path, Pages: n})
		out.Pages += n
	}
	sort.Slice(out.Docs, func(i, j int) bool {
		if out.Docs[i].Path != out.Docs[j].Path {
			return out.Docs[i].Path < out.Docs[j].Path
		}
		return out.Docs[i].ID < out.Docs[j].ID
	})
	return out
}
