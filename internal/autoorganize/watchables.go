package autoorganize

import (
	"sort"
	"strings"
)

// Watchable a folder or document a pipeline rule may watch
type Watchable struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // WatchFolder | WatchDocument
	Name string `json:"name"`
	Path string `json:"path"`
	// DocType "pdf", "epub" or "notebook"; empty for folders
	DocType string `json:"docType,omitempty"`
}

// Watchables every folder (the root excluded) and document outside the
// Trash, sorted by path. Nothing is written: the .content reads use a
// throwaway cache.
func (s *Service) Watchables(uid string) ([]Watchable, error) {
	ss, err := s.open(uid)
	if err != nil {
		return nil, err
	}
	snap := NewSnapshot(ss.tree)
	cc := NewContentCache(map[string]ContentInfo{}, s.storage.BlobStorage(uid).GetReader)
	out := []Watchable{}
	for _, d := range snap.Docs {
		if snap.Locate(d.Parent) != LocAtRoot {
			continue
		}
		path := d.Name
		if d.Parent != RootID {
			path = snap.PathName(d.Parent) + " / " + d.Name
		}
		w := Watchable{ID: d.ID, Kind: WatchDocument, Name: d.Name, Path: path}
		if d.IsFolder() {
			w.Kind = WatchFolder
		} else {
			w.DocType = "notebook"
			if ci := cc.Get(d); ci.Readable() {
				if ft := strings.ToLower(ci.FileType); ft == "pdf" || ft == "epub" {
					w.DocType = ft
				}
			}
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Path), strings.ToLower(out[j].Path)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
