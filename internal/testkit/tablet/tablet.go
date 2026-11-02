// Package tablet is a fake reMarkable tablet for tests and e2e seeding: it
// writes blobs and commits roots exactly like a sync15 device (metadata,
// content, page and payload blobs, a doc index per doc, a root index, then the
// root with the generation it last read).
package tablet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/model"
	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// Doc a document as the tablet holds it: raw metadata and content maps
type Doc struct {
	ID      string
	Meta    map[string]any
	Content map[string]any
	// NoContent the doc has no .content file at all
	NoContent bool
	// RawContent overrides Content with literal bytes (e.g. corrupt)
	RawContent []byte
	// RM the page blobs by page id, written as <doc>/<page>.rm
	RM map[string][]byte
}

// Tablet the fake device
type Tablet struct {
	t    testing.TB
	FS   *fs.FileSystemStorage
	UID  string
	Docs map[string]*Doc
}

// New a tablet for a user already registered on st. t may be nil (CLI use):
// errors are then returned instead of failing the test.
func New(t testing.TB, st *fs.FileSystemStorage, uid string) *Tablet {
	return &Tablet{t: t, FS: st, UID: uid, Docs: map[string]*Doc{}}
}

// NewStorage opens (creating if needed) the storage rooted at dir
func NewStorage(dir string) *fs.FileSystemStorage {
	return fs.NewStorage(&config.Config{DataDir: dir})
}

// RegisterUser creates the user on st
func RegisterUser(st *fs.FileSystemStorage, uid string, sync15 bool) error {
	user, err := model.NewUser(uid, "password")
	if err != nil {
		return err
	}
	user.ID = uid
	user.Sync15 = sync15
	return st.RegisterUser(user)
}

// fail records err; with a test it fails the test, always returns err
func (ft *Tablet) fail(err error) error {
	if err != nil && ft.t != nil {
		ft.t.Helper()
		ft.t.Fatal(err)
	}
	return err
}

// Ms the metadata time format: unix milliseconds as a string
func Ms(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func baseMeta(name, parent string, typ common.EntryType, now time.Time) map[string]any {
	return map[string]any{
		"visibleName":      name,
		"type":             string(typ),
		"parent":           parent,
		"createdTime":      Ms(now.Add(-30 * 24 * time.Hour)),
		"lastModified":     Ms(now.Add(-30 * 24 * time.Hour)),
		"lastOpened":       "",
		"lastOpenedPage":   0,
		"version":          1,
		"pinned":           false,
		"synced":           true,
		"modified":         false,
		"deleted":          false,
		"metadatamodified": false,
		// a field the server's MetadataFile struct does not know
		"lastOpenedTool": "fineliner",
	}
}

// Folder adds a folder
func (ft *Tablet) Folder(id, name, parent string) *Doc {
	d := &Doc{ID: id, Meta: baseMeta(name, parent, common.CollectionType, time.Now()), Content: map[string]any{"tags": []any{}}}
	ft.Docs[id] = d
	return d
}

// PDF adds a PDF document
func (ft *Tablet) PDF(id, name, parent string, pageCount, lastOpenedPage int, lastOpened time.Time) *Doc {
	d := &Doc{ID: id, Meta: baseMeta(name, parent, common.DocumentType, time.Now())}
	d.Meta["lastOpenedPage"] = lastOpenedPage
	if !lastOpened.IsZero() {
		d.Meta["lastOpened"] = Ms(lastOpened)
	}
	d.Content = map[string]any{
		"fileType":       "pdf",
		"pageCount":      pageCount,
		"lastOpenedPage": 0,
		"textScale":      0.8,
		"tags":           []any{},
	}
	ft.Docs[id] = d
	return d
}

// PDFv1 adds a PDF with a formatVersion 1 .content: a plain "pages" array of
// page ids (array order = page order) and no cPages. Page blobs go in d.RM.
func (ft *Tablet) PDFv1(id, name, parent string, pageIDs []string) *Doc {
	d := ft.PDF(id, name, parent, len(pageIDs), 0, time.Time{})
	ids := make([]any, len(pageIDs))
	for i, p := range pageIDs {
		ids[i] = p
	}
	d.Content["formatVersion"] = 1
	d.Content["pages"] = ids
	d.Content["redirectionPageMap"] = make([]any, len(pageIDs))
	return d
}

// Notebook adds a v6 notebook with no pages yet
func (ft *Tablet) Notebook(id, name, parent string) *Doc {
	d := &Doc{ID: id, Meta: baseMeta(name, parent, common.DocumentType, time.Now())}
	d.Content = map[string]any{
		"fileType":      "notebook",
		"formatVersion": 2,
		"pageCount":     0,
		"tags":          []any{},
		"cPages":        map[string]any{"pages": []any{}},
	}
	ft.Docs[id] = d
	return d
}

func (ft *Tablet) doc(id string) (*Doc, error) {
	d, ok := ft.Docs[id]
	if !ok {
		return nil, fmt.Errorf("tablet: unknown doc %q", id)
	}
	return d, nil
}

// pages the cPages.pages items of a doc (created if absent)
func (d *Doc) pages() []map[string]any {
	cp, _ := d.Content["cPages"].(map[string]any)
	if cp == nil {
		cp = map[string]any{}
		d.Content["cPages"] = cp
	}
	var out []map[string]any
	switch v := cp["pages"].(type) {
	case []any:
		for _, it := range v {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = v
	}
	return out
}

func (d *Doc) setPages(p []map[string]any) {
	items := make([]any, len(p))
	live := 0
	for i, m := range p {
		items[i] = m
		if _, del := m["deleted"]; !del {
			live++
		}
	}
	d.Content["cPages"].(map[string]any)["pages"] = items
	d.Content["pageCount"] = live
}

func idxOf(m map[string]any) string {
	ix, _ := m["idx"].(map[string]any)
	s, _ := ix["value"].(string)
	return s
}

func newIdx(v string) map[string]any {
	return map[string]any{"timestamp": "1:2", "value": v}
}

// sortPages orders the items by idx.value
func sortPages(p []map[string]any) {
	sort.SliceStable(p, func(i, j int) bool { return idxOf(p[i]) < idxOf(p[j]) })
}

// SetPage stores the page blob; a new page id is appended to cPages with an
// idx after the last page
func (ft *Tablet) SetPage(docID, pageID string, rm []byte) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	if d.RM == nil {
		d.RM = map[string][]byte{}
	}
	d.RM[pageID] = rm
	p := d.pages()
	for _, m := range p {
		if m["id"] == pageID {
			delete(m, "deleted")
			d.setPages(p)
			return nil
		}
	}
	last := ""
	for _, m := range p {
		if v := idxOf(m); v > last {
			last = v
		}
	}
	p = append(p, map[string]any{"id": pageID, "idx": newIdx(Between(last, ""))})
	d.setPages(p)
	return nil
}

// DeletePage marks the page deleted in cPages and drops its .rm entry
func (ft *Tablet) DeletePage(docID, pageID string) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	p := d.pages()
	for _, m := range p {
		if m["id"] == pageID {
			m["deleted"] = map[string]any{"timestamp": "1:2", "value": 1}
			delete(d.RM, pageID)
			d.setPages(p)
			return nil
		}
	}
	return ft.fail(fmt.Errorf("tablet: doc %s has no page %s", docID, pageID))
}

// MovePageBefore gives the page an idx sorting just before beforeID
func (ft *Tablet) MovePageBefore(docID, pageID, beforeID string) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	p := d.pages()
	sortPages(p)
	var moved map[string]any
	prev, next := "", ""
	found := false
	for _, m := range p {
		switch m["id"] {
		case pageID:
			moved = m
			continue
		case beforeID:
			next = idxOf(m)
			found = true
		}
		if !found {
			prev = idxOf(m)
		}
	}
	if moved == nil || !found {
		return ft.fail(fmt.Errorf("tablet: doc %s: cannot move %s before %s", docID, pageID, beforeID))
	}
	moved["idx"] = newIdx(Between(prev, next))
	sortPages(p)
	d.setPages(p)
	return nil
}

// Open records the last-opened time and page
func (ft *Tablet) Open(docID string, page int, at time.Time) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	d.Meta["lastOpened"] = Ms(at)
	d.Meta["lastOpenedPage"] = page
	return nil
}

// TagPage adds a page tag to .content pageTags, as the device writes it
// ({name, pageId, timestamp in unix ms})
func (ft *Tablet) TagPage(docID, pageID, tag string, at time.Time) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	tags, _ := d.Content["pageTags"].([]any)
	d.Content["pageTags"] = append(tags, map[string]any{"name": tag, "pageId": pageID, "timestamp": at.UnixMilli()})
	return nil
}

// UntagPage removes every pageTags entry of the page with that exact name
func (ft *Tablet) UntagPage(docID, pageID, tag string) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	tags, _ := d.Content["pageTags"].([]any)
	kept := []any{}
	for _, t := range tags {
		if m, ok := t.(map[string]any); ok && m["pageId"] == pageID && m["name"] == tag {
			continue
		}
		kept = append(kept, t)
	}
	d.Content["pageTags"] = kept
	return nil
}

// Rename sets the visible name
func (ft *Tablet) Rename(docID, name string) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	d.Meta["visibleName"] = name
	return nil
}

// Move sets the parent
func (ft *Tablet) Move(docID, parent string) error {
	d, err := ft.doc(docID)
	if err != nil {
		return ft.fail(err)
	}
	d.Meta["parent"] = parent
	return nil
}

func (ft *Tablet) write(data []byte) (string, int64, error) {
	hash, size, err := models.Hash(bytes.NewReader(data))
	if err != nil {
		return "", 0, err
	}
	return hash, size, ft.FS.BlobStorage(ft.UID).Write(hash, bytes.NewReader(data))
}

func (ft *Tablet) tree() (*models.HashTree, error) {
	return ft.FS.GetCachedTree(ft.UID)
}

func (ft *Tablet) readBlob(hash string) ([]byte, error) {
	r, err := ft.FS.BlobStorage(ft.UID).GetReader(hash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Pull refreshes every doc's metadata from the server, like a tablet sync
// that downloads the server's changes before uploading its own
func (ft *Tablet) Pull() error {
	tree, err := ft.tree()
	if err != nil {
		return ft.fail(err)
	}
	for _, hd := range tree.Docs {
		d, ok := ft.Docs[hd.EntryName]
		if !ok {
			d = &Doc{ID: hd.EntryName, Content: map[string]any{"tags": []any{}}}
			ft.Docs[hd.EntryName] = d
		}
		for _, f := range hd.Files {
			if !f.IsMetadata() {
				continue
			}
			b, err := ft.readBlob(f.Hash)
			if err != nil {
				return ft.fail(err)
			}
			m := map[string]any{}
			if err := json.Unmarshal(b, &m); err != nil {
				return ft.fail(err)
			}
			d.Meta = m
		}
	}
	return nil
}

// Load replaces the tablet's state with everything on the server: metadata,
// content and page blobs. Used by the CLI, which starts empty on each run.
func (ft *Tablet) Load() error {
	ft.Docs = map[string]*Doc{}
	tree, err := ft.tree()
	if err != nil {
		return ft.fail(err)
	}
	for _, hd := range tree.Docs {
		d := &Doc{ID: hd.EntryName, Content: map[string]any{"tags": []any{}}, NoContent: true}
		for _, f := range hd.Files {
			b, err := ft.readBlob(f.Hash)
			if err != nil {
				return ft.fail(err)
			}
			switch {
			case f.IsMetadata():
				d.Meta = map[string]any{}
				if err := json.Unmarshal(b, &d.Meta); err != nil {
					return ft.fail(err)
				}
			case f.IsContent():
				d.NoContent = false
				d.Content = map[string]any{}
				if err := json.Unmarshal(b, &d.Content); err != nil {
					return ft.fail(err)
				}
			case strings.HasSuffix(f.EntryName, ".rm"):
				if d.RM == nil {
					d.RM = map[string][]byte{}
				}
				d.RM[strings.TrimSuffix(filepath.Base(f.EntryName), ".rm")] = b
			}
		}
		ft.Docs[hd.EntryName] = d
	}
	return nil
}

// Sync uploads every doc and commits a new root
func (ft *Tablet) Sync() error {
	if ft.t != nil {
		ft.t.Helper()
	}
	bs := ft.FS.BlobStorage(ft.UID)
	tree := &models.HashTree{}
	ids := make([]string, 0, len(ft.Docs))
	for id := range ft.Docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := ft.Docs[id]
		hd := models.NewHashDoc("", id, common.DocumentType)
		mb, _ := json.Marshal(d.Meta)
		h, size, err := ft.write(mb)
		if err != nil {
			return ft.fail(err)
		}
		hd.AddFile(models.NewHashEntry(h, id+".metadata", size))
		if !d.NoContent {
			cb := d.RawContent
			if cb == nil {
				cb, _ = json.Marshal(d.Content)
			}
			if h, size, err = ft.write(cb); err != nil {
				return ft.fail(err)
			}
			hd.AddFile(models.NewHashEntry(h, id+".content", size))
		}
		if d.Meta["type"] == string(common.DocumentType) && d.Content["fileType"] != "notebook" {
			if h, size, err = ft.write([]byte("%PDF-1.4 " + id)); err != nil {
				return ft.fail(err)
			}
			hd.AddFile(models.NewHashEntry(h, id+".pdf", size))
		}
		pids := make([]string, 0, len(d.RM))
		for pid := range d.RM {
			pids = append(pids, pid)
		}
		sort.Strings(pids)
		for _, pid := range pids {
			if h, size, err = ft.write(d.RM[pid]); err != nil {
				return ft.fail(err)
			}
			hd.AddFile(models.NewHashEntry(h, id+"/"+pid+".rm", size))
		}
		idx, err := hd.IndexReader()
		if err != nil {
			return ft.fail(err)
		}
		ib, _ := io.ReadAll(idx)
		// indexes are stored under the HashEntries hash, like the device does
		if err := bs.Write(hd.Hash, bytes.NewReader(ib)); err != nil {
			return ft.fail(err)
		}
		tree.Docs = append(tree.Docs, hd)
	}
	tree.Rehash()
	ri, _ := tree.RootIndex()
	rb, _ := io.ReadAll(ri)
	if err := bs.Write(tree.Hash, bytes.NewReader(rb)); err != nil {
		return ft.fail(err)
	}
	_, gen, err := bs.GetRootIndex()
	if err != nil {
		return ft.fail(err)
	}
	if _, err := bs.WriteRootIndex(gen, tree.Hash); err != nil {
		return ft.fail(err)
	}
	return nil
}

// ServerDoc the doc as the server stores it, rebuilt from blobs only
func (ft *Tablet) ServerDoc(id string) *models.HashDoc {
	if ft.t != nil {
		ft.t.Helper()
	}
	os.Remove(filepath.Join(ft.FS.UserDataPath(ft.UID), ".tree"))
	tree, err := ft.tree()
	if err != nil {
		ft.fail(err)
		return nil
	}
	d, err := tree.FindDoc(id)
	if err != nil {
		return nil
	}
	return d
}

// ServerBlob the blob behind a named file of a server doc, nil if absent
func (ft *Tablet) ServerBlob(docID, entryName string) []byte {
	d := ft.ServerDoc(docID)
	if d == nil {
		return nil
	}
	for _, f := range d.Files {
		if f.EntryName == entryName {
			b, err := ft.readBlob(f.Hash)
			if err != nil {
				ft.fail(err)
				return nil
			}
			return b
		}
	}
	return nil
}

// ServerMetaRaw the raw metadata blob of a doc on the server
func (ft *Tablet) ServerMetaRaw(id string) map[string]any {
	if ft.t != nil {
		ft.t.Helper()
	}
	d := ft.ServerDoc(id)
	if d == nil {
		ft.fail(fmt.Errorf("doc %s not on server", id))
		return nil
	}
	for _, f := range d.Files {
		if f.IsMetadata() {
			b, err := ft.readBlob(f.Hash)
			if err != nil {
				ft.fail(err)
				return nil
			}
			m := map[string]any{}
			json.Unmarshal(b, &m)
			return m
		}
	}
	return nil
}

// ParentOf the parent of a doc on the server
func (ft *Tablet) ParentOf(id string) string {
	if ft.t != nil {
		ft.t.Helper()
	}
	d := ft.ServerDoc(id)
	if d == nil {
		ft.fail(fmt.Errorf("doc %s not on server", id))
		return ""
	}
	return d.Parent
}

// ServerContent the parsed .content of a doc on the server
func (ft *Tablet) ServerContent(id string) map[string]any {
	b := ft.ServerBlob(id, id+".content")
	if b == nil {
		return nil
	}
	m := map[string]any{}
	json.Unmarshal(b, &m)
	return m
}
