package autoorganize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// ErrUnknownTag a page tag that is not a known tag (KnownTags)
var ErrUnknownTag = errors.New("not a known tag")

// ErrNoPage the page is not a live page of the document
var ErrNoPage = errors.New("page not found")

// PatchContentPageTags appends {"name", "pageId", "timestamp": nowMs} to the
// "pageTags" list of a raw .content blob for every tag the page does not
// carry yet (NormalizePageTag), as the tablet writes it; every other key is
// kept exactly. added lists the tags written; out is nil when it is empty.
func PatchContentPageTags(raw []byte, pageID string, tags []string, nowMs int64) (out []byte, added []string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return nil, nil, fmt.Errorf("cannot parse content: %w", err)
	}
	var list []any
	if v, present := m["pageTags"]; present && v != nil {
		l, ok := v.([]any)
		if !ok {
			return nil, nil, errors.New("content pageTags is not a list")
		}
		list = l
	}
	has := map[string]bool{}
	for _, t := range list {
		e, _ := t.(map[string]any)
		name, _ := e["name"].(string)
		if p, _ := e["pageId"].(string); p == pageID {
			has[NormalizePageTag(name)] = true
		}
	}
	added = []string{}
	for _, tag := range tags {
		k := NormalizePageTag(tag)
		if k == "" || has[k] {
			continue
		}
		has[k] = true
		list = append(list, map[string]any{"name": tag, "pageId": pageID, "timestamp": nowMs})
		added = append(added, tag)
	}
	if len(added) == 0 {
		return nil, added, nil
	}
	m["pageTags"] = list
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), added, nil
}

// PatchContentPageTagsRemove drops every pageTags entry of pageID whose name
// matches one of tags (NormalizePageTag); every other key is kept exactly.
// removed lists the tags that were there; out is nil when it is empty.
func PatchContentPageTagsRemove(raw []byte, pageID string, tags []string) (out []byte, removed []string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return nil, nil, fmt.Errorf("cannot parse content: %w", err)
	}
	list, _ := m["pageTags"].([]any)
	drop := map[string]string{}
	for _, t := range tags {
		if k := NormalizePageTag(t); k != "" {
			drop[k] = t
		}
	}
	removed = []string{}
	hit := map[string]bool{}
	kept := make([]any, 0, len(list))
	for _, t := range list {
		e, _ := t.(map[string]any)
		name, _ := e["name"].(string)
		k := NormalizePageTag(name)
		if p, _ := e["pageId"].(string); p == pageID && drop[k] != "" {
			if !hit[k] {
				hit[k] = true
				removed = append(removed, drop[k])
			}
			continue
		}
		kept = append(kept, t)
	}
	if len(removed) == 0 {
		return nil, removed, nil
	}
	m["pageTags"] = kept
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), removed, nil
}

// patchContent rewrites the .content of docID with fs.UpdateTree: the closure
// re-reads the blob on every generation conflict and calls patch on it; patch
// returns nil to change nothing. The metadata version is bumped, lastModified
// is left alone (a tag change is not an edit).
func patchContent(tree *models.HashTree, bs *fs.LocalBlobStorage, docID string, patch func(raw []byte) ([]byte, error)) (changed bool, err error) {
	err = fs.UpdateTree(tree, bs, func(t *models.HashTree) error {
		doc, err := t.FindDoc(docID)
		if err != nil || doc.Deleted {
			return ErrVanished
		}
		var meta, content *models.HashEntry
		for _, f := range doc.Files {
			switch {
			case f.IsMetadata():
				meta = f
			case f.IsContent():
				content = f
			}
		}
		if meta == nil || content == nil {
			return errors.New("document has no metadata or content entry")
		}
		raw, err := readBlob(bs, content.Hash)
		if err != nil {
			return fmt.Errorf("cannot read content: %w", err)
		}
		patched, err := patch(raw)
		if err != nil {
			return err
		}
		if patched == nil {
			return errTagUnchanged
		}
		rawMeta, err := readBlob(bs, meta.Hash)
		if err != nil {
			return fmt.Errorf("cannot read metadata: %w", err)
		}
		version := doc.Version + 1
		patchedMeta, err := PatchMetadata(rawMeta, doc.Parent, version)
		if err != nil {
			return err
		}
		contentHash := sha256Hex(patched)
		if err := bs.Write(contentHash, bytes.NewReader(patched)); err != nil {
			return err
		}
		content.Hash = contentHash
		content.Size = int64(len(patched))
		doc.Version = version
		meta.Size = int64(len(patchedMeta))
		return fs.CommitDocMetadata(t, doc, bs, sha256Hex(patchedMeta), bytes.NewReader(patchedMeta))
	})
	if errors.Is(err, errTagUnchanged) {
		return false, nil
	}
	return err == nil, err
}

func livePage(raw []byte, pageID string) bool {
	for _, id := range ParseContent(raw).PageOrder {
		if id == pageID {
			return true
		}
	}
	return false
}

// tagPage adds page tags to pageID of docID (patchContent)
func tagPage(tree *models.HashTree, bs *fs.LocalBlobStorage, docID, pageID string, tags []string, nowMs int64) (added []string, err error) {
	added = []string{}
	_, err = patchContent(tree, bs, docID, func(raw []byte) ([]byte, error) {
		if !livePage(raw, pageID) {
			return nil, ErrNoPage
		}
		out, a, err := PatchContentPageTags(raw, pageID, tags, nowMs)
		added = a
		return out, err
	})
	if err != nil {
		return []string{}, err
	}
	return added, nil
}

// editTags adds and removes tags in one write: document tags when pageID is
// empty, else the page's tags
func editTags(tree *models.HashTree, bs *fs.LocalBlobStorage, docID, pageID string, add, remove []string, nowMs int64) (bool, error) {
	return patchContent(tree, bs, docID, func(raw []byte) ([]byte, error) {
		cur, changed := raw, false
		if pageID != "" {
			if !livePage(raw, pageID) {
				return nil, ErrNoPage
			}
			if out, _, err := PatchContentPageTags(cur, pageID, add, nowMs); err != nil {
				return nil, err
			} else if out != nil {
				cur, changed = out, true
			}
			if out, _, err := PatchContentPageTagsRemove(cur, pageID, remove); err != nil {
				return nil, err
			} else if out != nil {
				cur, changed = out, true
			}
		} else {
			for _, op := range []struct {
				tags []string
				add  bool
			}{{add, true}, {remove, false}} {
				for _, tag := range op.tags {
					out, ok, err := PatchContentTags(cur, tag, op.add, nowMs)
					if err != nil {
						return nil, err
					}
					if ok {
						cur, changed = out, true
					}
				}
			}
		}
		if !changed {
			return nil, nil
		}
		return cur, nil
	})
}

// EditTags adds and removes document tags (pageID "") or page tags by hand
// (the viewer), then tells the tablet to sync. Waits for a running sweep.
func (s *Service) EditTags(uid, docID, pageID string, add, remove []string) error {
	if err := s.checkSync15(uid); err != nil {
		return err
	}
	ul := s.loop(uid)
	ul.sweepMu.Lock()
	defer ul.sweepMu.Unlock()
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return err
	}
	changed, err := editTags(tree, s.storage.BlobStorage(uid), docID, pageID, add, remove, s.Now().UnixMilli())
	if err != nil {
		return err
	}
	if changed {
		s.notifier.NotifySync(uid, SourceID)
	}
	return nil
}

// ApplyPageTags adds known tags to a page (a handwritten #tag, found by
// rm-pages) and tells the tablet to sync. Waits for a running sweep.
func (s *Service) ApplyPageTags(uid, docID, pageID string, tags []string) ([]string, error) {
	if err := s.checkSync15(uid); err != nil {
		return nil, err
	}
	ul := s.loop(uid)
	ul.sweepMu.Lock()
	defer ul.sweepMu.Unlock()

	cfg, err := s.store.LoadConfig(uid)
	if err != nil {
		return nil, err
	}
	tree, err := s.storage.GetCachedTree(uid)
	if err != nil {
		return nil, err
	}
	bs := s.storage.BlobStorage(uid)
	snap := NewSnapshot(tree)
	cache := NewContentCache(map[string]ContentInfo{}, bs.GetReader)
	known := map[string]bool{}
	for _, k := range KnownTags(cfg, snap, cache.Get) {
		known[k] = true
	}
	for _, tag := range tags {
		if !known[NormalizeTag(tag)] {
			return nil, fmt.Errorf("%w: %q", ErrUnknownTag, tag)
		}
	}
	added, err := tagPage(tree, bs, docID, pageID, tags, s.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if len(added) > 0 {
		s.notifier.NotifySync(uid, SourceID)
	}
	return added, nil
}
