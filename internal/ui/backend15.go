package ui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/ddvk/rmfakecloud/internal/app/hub"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/ui/viewmodel"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type backend15 struct {
	blobHandler blobHandler
	h           *hub.Hub
	// rmPagesURL base URL of rm-pages; empty keeps the old exporter only
	rmPagesURL string
	httpClient *http.Client
}

func (b *backend15) GetDocumentTree(uid string) (tree *viewmodel.DocumentTree, err error) {
	hashTree, err := b.blobHandler.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}

	return viewmodel.DocTreeFromHashTree(hashTree), nil
}
func (b *backend15) Export(uid, docid, exporttype string, opt storage.ExportOption) (r io.ReadCloser, err error) {
	if exporttype == "rmdoc" {
		return b.blobHandler.ExportRmDoc(uid, docid)
	}
	if exporttype == "pdf" {
		return b.exportPDF(uid, docid)
	}
	r, err = b.blobHandler.Export(uid, docid)
	return
}

func (b *backend15) CreateDocument(uid, filename, parent string, stream io.Reader) (doc *storage.Document, err error) {
	doc, err = b.blobHandler.CreateBlobDocument(uid, filename, parent, stream)
	return
}

func (b *backend15) UpdateDocument(uid, docID, name, parent string) (err error) {
	return b.blobHandler.UpdateBlobDocument(uid, docID, name, parent)
}
func (b *backend15) CreateFolder(uid, name, parent string) (doc *storage.Document, err error) {
	return b.blobHandler.CreateBlobFolder(uid, name, parent)
}

func (b *backend15) DeleteDocument(uid, docID string) (err error) {
	return b.blobHandler.DeleteBlobDocument(uid, docID)
}

func (b *backend15) Sync(uid string) {
	b.h.NotifySync(uid, uuid.NewString())
}

// exportPDF renders through rm-pages when configured, then the old exporter,
// then the document's raw PDF/EPUB payload.
func (b *backend15) exportPDF(uid, docid string) (io.ReadCloser, error) {
	if b.rmPagesURL != "" {
		r, err := renderDoc(context.Background(), b.rmPagesURL, b.httpClient, b.blobHandler, uid, docid)
		if err == nil {
			return r, nil
		}
		if errors.Is(err, errUnreadablePages) {
			log.WithFields(log.Fields{"stage": "render", "doc_id": docid}).Warn("viewer: skip rm-pages: ", err)
		} else {
			log.WithFields(log.Fields{"stage": "render", "doc_id": docid}).Error("viewer: rm-pages render failed, using the old exporter: ", err)
		}
	}
	r, err := b.blobHandler.Export(uid, docid)
	if err == nil {
		// the old exporter reports failures through the stream: peek so that an
		// empty or failed export still reaches the raw payload fallback
		br := bufio.NewReader(r)
		if _, perr := br.Peek(1); perr == nil {
			return struct {
				io.Reader
				io.Closer
			}{br, r}, nil
		} else {
			r.Close()
			err = perr
		}
	}
	if raw, rerr := rawPayload(b.blobHandler, uid, docid); rerr == nil {
		log.WithFields(log.Fields{"doc_id": docid}).Warn("viewer: old exporter failed, sending the raw payload: ", err)
		return raw, nil
	}
	return nil, err
}
