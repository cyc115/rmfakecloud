package ui

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/danjacques/gofslock/fslock"
	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/gin-gonic/gin"
)

// treeRetries a concurrent read of the user's root blob holds a non-blocking
// file lock (fs.LoadBlob); the editor's parallel reads retry briefly instead
// of failing with a 500
const treeRetries = 10

func cachedTree(src renderSource, uid string) (*models.HashTree, error) {
	for i := 0; ; i++ {
		tree, err := src.GetCachedTree(uid)
		if err == nil || !errors.Is(err, fslock.ErrLockHeld) || i >= treeRetries {
			return tree, err
		}
		time.Sleep(time.Duration(20*(i+1)) * time.Millisecond)
	}
}

// PageTagDoc one document whose pages carry a tag
type PageTagDoc struct {
	ID string `json:"id"`
	// Folders the doc's ancestors, nearest first, ending with the root ("")
	Folders []string `json:"folders"`
	// Kind "pdf_epub", "notebooks" or "other" (ContentInfo.Kind)
	Kind  string `json:"kind"`
	Pages int    `json:"pages"`
}

// PageTag a distinct page tag (as matched: trimmed, lowercased) and where it is
type PageTag struct {
	Name  string       `json:"name"`
	Pages int          `json:"pages"`
	Docs  []PageTagDoc `json:"docs"`
}

// listPageTags every distinct page tag on the live pages of the user's
// documents outside the Trash (documents tagged "no automation" are skipped,
// as every rule skips them), sorted by name. Read-only: the .content reads
// use a throwaway cache. A tag on a page no longer in the page list is ignored.
func listPageTags(src renderSource, uid string) ([]PageTag, error) {
	tree, err := cachedTree(src, uid)
	if err != nil {
		return nil, err
	}
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(map[string]autoorganize.ContentInfo{}, src.BlobStorage(uid).GetReader)
	infos := map[string]autoorganize.ContentInfo{}
	snap.AttachPages(func(d *autoorganize.DocSnap) autoorganize.ContentInfo {
		ci := cc.Get(d)
		infos[d.ID] = ci
		return ci
	})
	byName := map[string]*PageTag{}
	for _, d := range snap.Docs {
		if d.IsFolder() || snap.Locate(d.Parent) != autoorganize.LocAtRoot {
			continue
		}
		ci := infos[d.ID]
		if len(ci.PageTags) == 0 || ci.HasNoAutomationTag() {
			continue
		}
		live := map[string]bool{}
		for _, p := range d.Pages {
			live[p.ID] = true
		}
		counts := map[string]int{}
		for pageID, tags := range ci.PageTags {
			if d.PagesErr == "" && d.Pages != nil && !live[pageID] {
				continue
			}
			for name := range tags {
				counts[name]++
			}
		}
		for name, n := range counts {
			t := byName[name]
			if t == nil {
				t = &PageTag{Name: name, Docs: []PageTagDoc{}}
				byName[name] = t
			}
			t.Pages += n
			t.Docs = append(t.Docs, PageTagDoc{ID: d.ID, Folders: ancestors(snap, d.Parent), Kind: ci.Kind(), Pages: n})
		}
	}
	out := make([]PageTag, 0, len(byName))
	for _, t := range byName {
		sort.Slice(t.Docs, func(i, j int) bool { return t.Docs[i].ID < t.Docs[j].ID })
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ancestors folder ids from parent up to and including the root
func ancestors(snap *autoorganize.Snapshot, parent string) []string {
	out := []string{}
	seen := map[string]bool{}
	for id := parent; id != autoorganize.RootID && !seen[id]; {
		seen[id] = true
		out = append(out, id)
		d := snap.Docs[id]
		if d == nil {
			break
		}
		id = d.Parent
	}
	return append(out, autoorganize.RootID)
}

// autoOrganizePageTags GET page-tags: the tag picker's suggestions and the
// page_tagged preview count
func (app *ReactAppWrapper) autoOrganizePageTags(c *gin.Context) {
	if app.blobs == nil {
		c.JSON(http.StatusOK, gin.H{"tags": []PageTag{}})
		return
	}
	tags, err := listPageTags(app.blobs, userID(c))
	if err != nil {
		app.autoOrganizeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}
