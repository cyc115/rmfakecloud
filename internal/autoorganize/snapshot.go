package autoorganize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// RootName display name of the root folder
const RootName = "My files"

// DocSnap an immutable copy of the fields the evaluator needs
type DocSnap struct {
	ID             string
	Parent         string
	Name           string
	Type           common.EntryType
	CreatedTime    string
	LastModified   string
	LastOpened     string
	LastOpenedPage int
	Pinned         bool
	Version        int
	Hash           string
	ContentHash    string
	Fingerprint    string
	// PDFHash hash of the <doc>.pdf entry, empty when there is none
	PDFHash string
	// Pages the live cPages pages in idx order. Only meaningful when
	// Snapshot.PagesAttached is true and PagesErr is empty: otherwise nil
	// means "unknown", not "no pages". A doc without .content (folders,
	// ContentHash == "") also has nil Pages and empty PagesErr: it has no
	// page list to compare, so consumers must not read it as "all pages gone".
	Pages []PageSnap
	// PagesErr why Pages is unknown (unreadable .content); empty when known
	PagesErr string

	// pageHashes page id to the hash of its <doc>/<id>.rm entry
	pageHashes map[string]string
}

// PageSnap one page of a document
type PageSnap struct {
	ID string
	// Hash of the page's .rm blob; empty for a blank page
	Hash string
	// Index position in cPages order, from 0
	Index int
}

// IsFolder a collection
func (d *DocSnap) IsFolder() bool {
	return d.Type == common.CollectionType
}

// Snapshot the tree as it was at sweep start (R3)
type Snapshot struct {
	Docs map[string]*DocSnap
	// PagesAttached AttachPages ran; when false every Pages is unfilled
	PagesAttached bool
	// children by parent id, sorted by name then id
	children map[string][]*DocSnap
}

// NewSnapshot deep copies the relevant fields of the tree. Pages are NOT
// filled: call AttachPages (planFor does) before reading DocSnap.Pages.
func NewSnapshot(tree *models.HashTree) *Snapshot {
	s := &Snapshot{Docs: map[string]*DocSnap{}, children: map[string][]*DocSnap{}}
	for _, d := range tree.Docs {
		if d.Deleted {
			continue
		}
		snap := snapDoc(d)
		s.Docs[snap.ID] = snap
	}
	for _, d := range s.Docs {
		s.children[d.Parent] = append(s.children[d.Parent], d)
	}
	for _, list := range s.children {
		sortDocs(list)
	}
	return s
}

func sortDocs(list []*DocSnap) {
	sort.Slice(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].ID < list[j].ID
	})
}

func snapDoc(d *models.HashDoc) *DocSnap {
	snap := &DocSnap{
		ID:             d.EntryName,
		Parent:         d.Parent,
		Name:           d.DocumentName,
		Type:           d.CollectionType,
		CreatedTime:    d.CreatedTime,
		LastModified:   d.LastModified,
		LastOpened:     d.LastOpened,
		LastOpenedPage: d.LastOpenedPage,
		Pinned:         d.Pinned,
		Version:        d.Version,
		Hash:           d.Hash,
	}
	if e := contentEntry(d); e != nil {
		snap.ContentHash = e.Hash
	}
	snap.Fingerprint = fingerprint(d)
	for _, f := range d.Files {
		switch {
		case f.EntryName == d.EntryName+".pdf":
			snap.PDFHash = f.Hash
		case strings.HasPrefix(f.EntryName, d.EntryName+"/") && strings.HasSuffix(f.EntryName, ".rm"):
			id := strings.TrimSuffix(strings.TrimPrefix(f.EntryName, d.EntryName+"/"), ".rm")
			if snap.pageHashes == nil {
				snap.pageHashes = map[string]string{}
			}
			snap.pageHashes[id] = f.Hash
		}
	}
	return snap
}

// AttachPages fills Pages of every document from its .content PageOrder and
// its .rm entries. Entries that are not in cPages (toc.rm) and deleted pages
// are left out; a page without an .rm entry (blank) has no Hash.
func (s *Snapshot) AttachPages(content func(*DocSnap) ContentInfo) {
	s.PagesAttached = true
	for _, d := range s.Docs {
		if d.IsFolder() || d.ContentHash == "" {
			continue
		}
		ci := content(d)
		d.Pages = nil
		d.PagesErr = ""
		if !ci.Readable() {
			d.PagesErr = ci.Error
			continue
		}
		if ci.PagesError != "" {
			d.PagesErr = ci.PagesError
			continue
		}
		for i, id := range ci.PageOrder {
			d.Pages = append(d.Pages, PageSnap{ID: id, Hash: d.pageHashes[id], Index: i})
		}
	}
}

func contentEntry(d *models.HashDoc) *models.HashEntry {
	for _, f := range d.Files {
		if f.IsContent() {
			return f
		}
	}
	return nil
}

// fingerprint of the tablet-side state that is new information for the
// cycle guard: every file except the metadata (content, pages, annotations),
// plus lastOpened and lastModified. Parent, version and pinned are left out
// so the sweeper's own moves and a pin do not count as a change.
func fingerprint(d *models.HashDoc) string {
	var parts []string
	for _, f := range d.Files {
		if f.IsMetadata() {
			continue
		}
		parts = append(parts, f.EntryName+":"+f.Hash)
	}
	sort.Strings(parts)
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{'\n'})
	}
	fmt.Fprintf(h, "opened=%s\nmodified=%s\n", d.LastOpened, d.LastModified)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Children of a folder, in deterministic order
func (s *Snapshot) Children(folderID string) []*DocSnap {
	return s.children[folderID]
}

// Location of an item, found by walking parents up to the root
type Location int

// Locations
const (
	LocAtRoot Location = iota
	LocInTrash
	// LocUnknown the walk hit an unknown parent or a cycle; never treated as root
	LocUnknown
)

// Locate walks folderID's ancestors
func (s *Snapshot) Locate(folderID string) Location {
	visited := map[string]bool{}
	id := folderID
	for {
		if id == RootID {
			return LocAtRoot
		}
		if id == trashID {
			return LocInTrash
		}
		if visited[id] {
			return LocUnknown
		}
		visited[id] = true
		d, ok := s.Docs[id]
		if !ok {
			return LocUnknown
		}
		id = d.Parent
	}
}

// FolderExists the id is the root or a folder in the tree
func (s *Snapshot) FolderExists(id string) bool {
	if id == RootID {
		return true
	}
	d, ok := s.Docs[id]
	return ok && d.IsFolder()
}

// PathName "Research Reports / Read"; the root is "My files"
func (s *Snapshot) PathName(id string) string {
	if id == RootID {
		return RootName
	}
	if id == trashID {
		return "Trash"
	}
	var names []string
	visited := map[string]bool{}
	for id != RootID && id != trashID {
		if visited[id] {
			break
		}
		visited[id] = true
		d, ok := s.Docs[id]
		if !ok {
			names = append(names, "?")
			break
		}
		names = append(names, d.Name)
		id = d.Parent
	}
	if id == trashID {
		names = append(names, "Trash")
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return strings.Join(names, " / ")
}

// FolderInfo a folder for the UI pickers
type FolderInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	Path    string `json:"path"`
	InTrash bool   `json:"inTrash"`
	Depth   int    `json:"depth"`
}

// Folders every folder outside the trash in tree order, root first
func (s *Snapshot) Folders() []FolderInfo {
	out := []FolderInfo{{ID: RootID, Name: RootName, Path: RootName}}
	var walk func(parent string, depth int)
	visited := map[string]bool{}
	walk = func(parent string, depth int) {
		for _, d := range s.children[parent] {
			if !d.IsFolder() || visited[d.ID] {
				continue
			}
			visited[d.ID] = true
			out = append(out, FolderInfo{ID: d.ID, Name: d.Name, Parent: d.Parent, Path: s.PathName(d.ID), Depth: depth})
			walk(d.ID, depth+1)
		}
	}
	walk(RootID, 1)
	return out
}

// ContentInfo the fields read from a .content blob
type ContentInfo struct {
	// Error why the blob could not be read; empty when it was
	Error          string   `json:"error,omitempty"`
	FileType       string   `json:"fileType"`
	PageCount      int      `json:"pageCount"`
	LastOpenedPage int      `json:"lastOpenedPage"`
	Tags           []string `json:"tags,omitempty"`
	// PageOrder ids of the live cPages pages, sorted by idx.value
	PageOrder []string `json:"pageOrder,omitempty"`
	// ParseVersion the parser that wrote the entry; older entries are re-read
	ParseVersion int `json:"parseVersion,omitempty"`
	// PagesError the page list is missing although pageCount > 0: the rest of
	// the blob is fine (Readable) but PageOrder must not be read as "no pages"
	PagesError string `json:"pagesError,omitempty"`
}

// contentParseVersion bump when ParseContent starts reading more fields
const contentParseVersion = 8

// Readable the blob was decoded
func (c *ContentInfo) Readable() bool {
	return c.Error == ""
}

// HasNoAutomationTag any document level tag normalises to "no automation"
func (c *ContentInfo) HasNoAutomationTag() bool {
	for _, t := range c.Tags {
		if IsNoAutomationTag(t) {
			return true
		}
	}
	return false
}

// HasTag a document level tag equal to tag after normalisation
func (c *ContentInfo) HasTag(tag string) bool {
	want := NormalizeTag(tag)
	for _, t := range c.Tags {
		if NormalizeTag(t) == want {
			return true
		}
	}
	return false
}

// Kind "pdf_epub", "notebooks" or "other", from fileType
func (c *ContentInfo) Kind() string {
	switch strings.ToLower(c.FileType) {
	case "pdf", "epub":
		return ScopePdfEpub
	case "", "notebook":
		return ScopeNotebooks
	}
	return "other"
}

var errNoContent = errors.New("document has no .content file")

// ParseContent decodes a .content blob into a raw map (never ContentFile,
// whose fixed fields break on firmware changes such as a float textScale)
func ParseContent(b []byte) ContentInfo {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	raw := map[string]any{}
	if err := dec.Decode(&raw); err != nil {
		return ContentInfo{Error: "cannot parse .content: " + err.Error()}
	}
	info := ContentInfo{ParseVersion: contentParseVersion}
	if s, ok := raw["fileType"].(string); ok {
		info.FileType = s
	}
	info.PageCount = intOf(raw["pageCount"])
	info.LastOpenedPage = intOf(raw["lastOpenedPage"])
	if tags, ok := raw["tags"].([]any); ok {
		for _, t := range tags {
			switch v := t.(type) {
			case string:
				info.Tags = append(info.Tags, v)
			case map[string]any:
				if name, ok := v["name"].(string); ok {
					info.Tags = append(info.Tags, name)
				}
			}
		}
	}
	order, unusable, err := pageOrder(raw)
	if err == nil && len(order) == 0 && info.PageCount > 0 && (!hasPagesList(raw) || unusable) {
		// a page count with no list would read as "every page deleted"; the
		// move rules still use the blob, so only the page list is flagged
		info.PagesError = "pageCount > 0 but no usable cPages or pages list"
	}
	if err != nil {
		info.Error = "cannot read pages: " + err.Error()
		return info
	}
	info.PageOrder = order
	return info
}

// pageOrder the ids of the cPages pages that are not deleted, by idx.value
// (formatVersion 2), else from the plain "pages" id array of formatVersion 1
// (array order). A "pages" value of another shape is an error, not zero pages.
//
// unusable is true when cPages.pages holds no object with a string id (empty,
// or only junk), or a v1 "pages" array holds no id: with pageCount > 0 that is
// a broken list, not zero pages (R26, R38). A cPages list whose pages are all
// deleted is a real zero.
func pageOrder(raw map[string]any) (out []string, unusable bool, err error) {
	if c := raw["cPages"]; c != nil {
		if _, ok := c.(map[string]any); !ok {
			return nil, false, errors.New("cPages is not an object")
		}
	}
	cp, _ := raw["cPages"].(map[string]any)
	if cp["pages"] == nil {
		// a v1 list cannot say "all deleted": present but empty is unusable
		out, err = v1PageOrder(raw["pages"])
		return out, raw["pages"] != nil && len(out) == 0, err
	}
	items, ok := cp["pages"].([]any)
	if !ok {
		return nil, false, errors.New("cPages.pages is not an array")
	}
	valid := 0
	type pg struct{ id, idx string }
	var pages []pg
	for _, it := range items {
		p, ok := it.(map[string]any)
		if !ok {
			continue
		}
		id, _ := p["id"].(string)
		if id == "" {
			continue
		}
		valid++
		if deletedFlag(p["deleted"]) {
			continue
		}
		ix, _ := p["idx"].(map[string]any)
		v, _ := ix["value"].(string)
		pages = append(pages, pg{id, v})
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].idx < pages[j].idx })
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p.id] {
			continue
		}
		seen[p.id] = true
		out = append(out, p.id)
	}
	return out, valid == 0, nil
}

// hasPagesList .content carries cPages.pages or a pages array
func hasPagesList(raw map[string]any) bool {
	cp, _ := raw["cPages"].(map[string]any)
	if cp["pages"] != nil {
		return true
	}
	return raw["pages"] != nil
}

func v1PageOrder(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, errors.New("pages is not an array")
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		id, ok := it.(string)
		if !ok {
			return nil, errors.New("pages holds a non-string entry")
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// deletedFlag a cPages "deleted" register is set: absent or value 0 means the
// page is present (a restored page can keep the key)
func deletedFlag(v any) bool {
	switch d := v.(type) {
	case nil:
		return false
	case map[string]any:
		return deletedFlag(d["value"])
	case bool:
		return d
	case json.Number:
		f, err := d.Float64()
		return err == nil && f != 0
	case float64:
		return d != 0
	case string:
		return d == "1" || d == "true"
	}
	// unknown shape: keep the page; a false "present" only leaves a stale page
	return false
}

func intOf(v any) int {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	case float64:
		return int(n)
	}
	return 0
}

// ContentReader opens a blob by hash
type ContentReader func(hash string) (io.ReadCloser, error)

// ContentCache reads .content blobs once per blob hash
type ContentCache struct {
	cache map[string]ContentInfo
	read  ContentReader
	used  map[string]bool
}

// NewContentCache backed by the state's persisted cache
func NewContentCache(cache map[string]ContentInfo, read ContentReader) *ContentCache {
	return &ContentCache{cache: cache, read: read, used: map[string]bool{}}
}

// Get the content info for a doc snapshot
func (c *ContentCache) Get(d *DocSnap) ContentInfo {
	if d.ContentHash == "" {
		return ContentInfo{Error: errNoContent.Error()}
	}
	c.used[d.ContentHash] = true
	if info, ok := c.cache[d.ContentHash]; ok && info.ParseVersion >= contentParseVersion {
		return info
	}
	r, err := c.read(d.ContentHash)
	if err != nil {
		// not cached: a missing blob may appear on the next sync
		return ContentInfo{Error: "cannot read .content: " + err.Error()}
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return ContentInfo{Error: "cannot read .content: " + err.Error()}
	}
	info := ParseContent(b)
	c.cache[d.ContentHash] = info
	return info
}

// Used the hashes looked up since creation
func (c *ContentCache) Used() map[string]bool {
	return c.used
}
