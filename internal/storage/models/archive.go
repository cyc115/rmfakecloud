package models

import (
	"encoding/json"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
	log "github.com/sirupsen/logrus"
)

// ArchiveFromHashDoc reads an archive
func ArchiveFromHashDoc(doc *HashDoc, rs RemoteStorage) (*exporter.MyArchive, error) {
	uuid := doc.EntryName
	a := exporter.MyArchive{
		Zip: archive.Zip{
			UUID: uuid,
		},
	}

	pageMap := make(map[string]string)
	for _, f := range doc.Files {
		filext := path.Ext(f.EntryName)
		name := strings.TrimSuffix(path.Base(f.EntryName), filext)
		switch filext {
		case storage.ContentFileExt:
			blob, err := rs.GetReader(f.Hash)
			if err != nil {
				return nil, err
			}
			defer blob.Close()
			contentBytes, err := io.ReadAll(blob)
			if err != nil {
				return nil, err
			}
			err = json.Unmarshal(contentBytes, &a.Content)
			if err != nil {
				return nil, err
			}
		case storage.EpubFileExt:
			fallthrough
		case storage.PdfFileExt:
			blob, err := rs.GetReader(f.Hash)
			if err != nil {
				return nil, err
			}
			// defer blob.Close()
			// contentBytes, err := ioutil.ReadAll(blob)
			// if err != nil {
			// 	return nil, err
			// }
			// a.Payload = contentBytes
			//HACK:
			a.PayloadReader = blob.(io.ReadSeekCloser)

		case ".json":
			//metadata
		case storage.RmFileExt:
			log.Debug("adding page ", name)
			pageMap[name] = f.Hash
		}
	}

	// Firmware 3.x (formatVersion 2) notebooks carry no flat "pages" list;
	// their pages live in cPages.pages[].id, ordered by idx.value.
	pageIDs := a.Content.Pages
	redir := map[string]int{}
	if len(pageIDs) == 0 {
		pageIDs, redir = cpagePageOrder(a.Content.CPages)
	}

	for _, p := range pageIDs {
		if hash, ok := pageMap[p]; ok {
			log.Debug("page ", hash)
			reader, err := rs.GetReader(hash)
			if err != nil {
				return nil, err
			}
			pageBin, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			rmpage := rm.New()
			err = rmpage.UnmarshalBinary(pageBin)
			if err != nil {
				return nil, err
			}

			page := archive.Page{
				Data:     rmpage,
				Pagedata: "Blank",
			}
			if docPage, ok := redir[p]; ok {
				page.DocPage = docPage
			}
			a.Pages = append(a.Pages, page)
		}
	}

	return &a, nil
}

// cpagePageOrder the live cPages page ids in idx order, with the PDF page
// each shows (redir.value, -1 when the page has no redir, i.e. an inserted
// page). Deleted pages, entries with no id, and duplicate ids are skipped.
// Mirrors autoorganize.pageOrder/redirMap for the export path.
func cpagePageOrder(cp archive.CPages) ([]string, map[string]int) {
	type pg struct {
		id    string
		idx   string
		redir int
	}
	var pages []pg
	for _, p := range cp.Pages {
		if p.ID == "" || bool(p.Deleted) {
			continue
		}
		r := -1
		if p.Redir != nil {
			r = p.Redir.Value
		}
		pages = append(pages, pg{p.ID, p.Idx.Value, r})
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].idx < pages[j].idx })
	order := make([]string, 0, len(pages))
	redir := make(map[string]int, len(pages))
	for _, p := range pages {
		if _, dup := redir[p.id]; dup {
			continue
		}
		redir[p.id] = p.redir
		order = append(order, p.id)
	}
	return order, redir
}
