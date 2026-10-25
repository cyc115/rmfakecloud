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
}

// IsFolder a collection
func (d *DocSnap) IsFolder() bool {
	return d.Type == common.CollectionType
}

// Snapshot the tree as it was at sweep start (R3)
type Snapshot struct {
	Docs map[string]*DocSnap
	// children by parent id, sorted by name then id
	children map[string][]*DocSnap
}

// NewSnapshot deep copies the relevant fields of the tree
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
	return snap
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
}

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
	info := ContentInfo{}
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
	return info
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
	if info, ok := c.cache[d.ContentHash]; ok {
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
