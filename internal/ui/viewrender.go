package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ddvk/rmfakecloud/internal/autoorganize"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// renderTimeout the longest one POST to rm-pages /api/render/doc may take
const renderTimeout = 60 * time.Second

// errUnreadablePages the page list of the doc cannot be read: rm-pages is not asked
var errUnreadablePages = errors.New("page list unreadable")

type renderSource interface {
	GetCachedTree(uid string) (*models.HashTree, error)
	BlobStorage(uid string) *fs.LocalBlobStorage
}

type renderPage struct {
	PageID string `json:"page_id"`
	Hash   string `json:"hash,omitempty"`
	// Redir PDF page this page annotates, -1 = inserted page; nil = not sent
	Redir *int `json:"redir,omitempty"`
}

type renderRequest struct {
	User    string       `json:"user"`
	DocID   string       `json:"doc_id"`
	DocType string       `json:"doc_type"`
	PDFHash string       `json:"pdf_hash,omitempty"`
	Pages   []renderPage `json:"pages"`
}

// buildRenderRequest reads the doc's pages in display order (the snapshot code
// shared with pagewatch) and its payload hash.
func buildRenderRequest(src renderSource, uid, docID string) (*renderRequest, error) {
	tree, err := src.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	if _, err := tree.FindDoc(docID); err != nil {
		return nil, err
	}
	snap := autoorganize.NewSnapshot(tree)
	cc := autoorganize.NewContentCache(map[string]autoorganize.ContentInfo{}, src.BlobStorage(uid).GetReader)
	var ci autoorganize.ContentInfo
	// read the .content of this doc only, not of the whole tree
	snap.AttachPages(func(d *autoorganize.DocSnap) autoorganize.ContentInfo {
		if d.ID != docID {
			return autoorganize.ContentInfo{Error: "not requested"}
		}
		ci = cc.Get(d)
		return ci
	})
	d := snap.Docs[docID]
	if d == nil || d.IsFolder() {
		return nil, fmt.Errorf("%s is not a document", docID)
	}
	if d.PagesErr != "" {
		return nil, fmt.Errorf("%w: %s", errUnreadablePages, d.PagesErr)
	}
	if d.ContentHash == "" {
		return nil, fmt.Errorf("%w: no .content", errUnreadablePages)
	}
	req := &renderRequest{User: uid, DocID: docID, PDFHash: d.PDFHash, Pages: []renderPage{}}
	switch ci.FileType {
	case "pdf", "epub":
		req.DocType = ci.FileType
	default:
		req.DocType = "notebook"
	}
	for _, p := range d.Pages {
		rp := renderPage{PageID: p.ID, Hash: p.Hash}
		if r, ok := ci.Redir[p.ID]; ok && req.DocType != "notebook" {
			rp.Redir = &r
		}
		req.Pages = append(req.Pages, rp)
	}
	return req, nil
}

// renderDoc asks rm-pages for the document as PDF
func renderDoc(ctx context.Context, base string, client *http.Client, src renderSource, uid, docID string) (io.ReadCloser, error) {
	req, err := buildRenderRequest(src, uid, docID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, renderTimeout)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/render/doc", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(hreq)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("rm-pages answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	return &cancelCloser{ReadCloser: resp.Body, cancel: cancel}, nil
}

type cancelCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelCloser) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// rawPayload the doc's own PDF or EPUB blob
func rawPayload(src renderSource, uid, docID string) (io.ReadCloser, error) {
	tree, err := src.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	doc, err := tree.FindDoc(docID)
	if err != nil {
		return nil, err
	}
	for _, ext := range []string{".pdf", ".epub"} {
		for _, f := range doc.Files {
			if f.EntryName == docID+ext {
				return src.BlobStorage(uid).GetReader(f.Hash)
			}
		}
	}
	return nil, errors.New("document has no PDF or EPUB payload")
}
