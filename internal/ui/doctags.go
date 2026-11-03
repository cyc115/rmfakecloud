package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/ui/viewmodel"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// DocTagsPage one live page in display order (the order rm-pages renders) and
// its tags as the tablet spells them
type DocTagsPage struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
}

// DocTags the document's own tags and every live page with its tags. Pages
// is null when the page list cannot be read.
type DocTags struct {
	Tags  []string      `json:"tags"`
	Pages []DocTagsPage `json:"pages"`
}

var errNotADocument = errors.New("not a document")

// rawTags the tag names of a .content blob in their original spelling, one per
// matched tag (NormalizeTag), first spelling wins. Page tags by page id.
func rawTags(b []byte) (doc []string, pages map[string][]string, err error) {
	var raw struct {
		Tags     []json.RawMessage `json:"tags"`
		PageTags []struct {
			Name   string `json:"name"`
			PageID string `json:"pageId"`
		} `json:"pageTags"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, err
	}
	doc = []string{}
	seen := map[string]bool{}
	for _, t := range raw.Tags {
		// firmware 3.x writes {name, timestamp}; older ones a plain string
		var name string
		if json.Unmarshal(t, &name) != nil {
			var obj struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(t, &obj) != nil {
				continue
			}
			name = obj.Name
		}
		if k := autoorganize.NormalizeTag(name); k != "" && !seen[k] {
			seen[k] = true
			doc = append(doc, name)
		}
	}
	pages = map[string][]string{}
	pageSeen := map[string]map[string]bool{}
	for _, t := range raw.PageTags {
		k := autoorganize.NormalizePageTag(t.Name)
		if k == "" || t.PageID == "" {
			continue
		}
		if pageSeen[t.PageID] == nil {
			pageSeen[t.PageID] = map[string]bool{}
		}
		if !pageSeen[t.PageID][k] {
			pageSeen[t.PageID][k] = true
			pages[t.PageID] = append(pages[t.PageID], t.Name)
		}
	}
	return doc, pages, nil
}

// docTags the tags of one document. Read-only.
func docTags(src renderSource, uid, docID string) (*DocTags, error) {
	tree, err := cachedTree(src, uid)
	if err != nil {
		return nil, err
	}
	if _, err := tree.FindDoc(docID); err != nil {
		return nil, errNotADocument
	}
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(map[string]autoorganize.ContentInfo{}, src.BlobStorage(uid).GetReader)
	// read the .content of this doc only, not of the whole tree
	snap.AttachPages(func(d *autoorganize.DocSnap) autoorganize.ContentInfo {
		if d.ID != docID {
			return autoorganize.ContentInfo{Error: "not requested"}
		}
		return cc.Get(d)
	})
	d := snap.Docs[docID]
	if d == nil || d.IsFolder() {
		return nil, errNotADocument
	}
	out := &DocTags{Tags: []string{}}
	if d.ContentHash == "" {
		return out, nil
	}
	r, err := src.BlobStorage(uid).GetReader(d.ContentHash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	tags, pageTags, err := rawTags(b)
	if err != nil {
		return nil, err
	}
	out.Tags = tags
	if d.PagesErr != "" {
		return out, nil
	}
	out.Pages = make([]DocTagsPage, 0, len(d.Pages))
	for _, p := range d.Pages {
		t := pageTags[p.ID]
		if t == nil {
			t = []string{}
		}
		out.Pages = append(out.Pages, DocTagsPage{ID: p.ID, Tags: t})
	}
	return out, nil
}

func (app *ReactAppWrapper) registerDocTagsRoute(auth *gin.RouterGroup) {
	auth.GET("documents/:docid/tags", app.getDocumentTags)
	auth.POST("documents/:docid/tags", app.editDocumentTags)
}

type editTagsReq struct {
	// PageID the page whose tags change; empty: the document's tags
	PageID string   `json:"page_id"`
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

// editDocumentTags POST documents/:docid/tags: add and remove document or
// page tags by hand; answers with the document's tags as GET does
func (app *ReactAppWrapper) editDocumentTags(c *gin.Context) {
	var req editTagsReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Add)+len(req.Remove) == 0 {
		badReq(c, "body must be {page_id?, add: [..], remove: [..]}")
		return
	}
	clean := func(tags []string) ([]string, bool) {
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			t = strings.TrimSpace(t)
			if autoorganize.NormalizeTag(t) == "" {
				return nil, false
			}
			out = append(out, t)
		}
		return out, true
	}
	add, ok1 := clean(req.Add)
	remove, ok2 := clean(req.Remove)
	if !ok1 || !ok2 {
		badReq(c, "a tag cannot be blank")
		return
	}
	docID, uid := common.ParamS(docIDParam, c), userID(c)
	err := app.organizer.EditTags(uid, docID, req.PageID, add, remove)
	switch {
	case errors.Is(err, autoorganize.ErrVanished), errors.Is(err, autoorganize.ErrNoPage):
		c.AbortWithStatusJSON(http.StatusNotFound, viewmodel.NewErrorResponse(err.Error()))
		return
	case err != nil:
		app.autoOrganizeError(c, err)
		return
	}
	app.getDocumentTags(c)
}

// getDocumentTags GET documents/:docid/tags: the viewer's document and page tags
func (app *ReactAppWrapper) getDocumentTags(c *gin.Context) {
	if app.blobs == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, viewmodel.NewErrorResponse("tags are not available"))
		return
	}
	tags, err := docTags(app.blobs, userID(c), common.ParamS(docIDParam, c))
	if err != nil {
		if errors.Is(err, errNotADocument) {
			c.AbortWithStatusJSON(http.StatusNotFound, viewmodel.NewErrorResponse("document not found"))
			return
		}
		log.WithField("stage", "doctags").Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, viewmodel.NewErrorResponse("cannot read tags"))
		return
	}
	c.JSON(http.StatusOK, tags)
}
