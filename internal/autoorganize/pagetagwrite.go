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

// tagPage adds page tags to pageID of docID with fs.UpdateTree, like tagDoc:
// the closure re-reads the .content on every generation conflict, only
// "pageTags" changes and the metadata version is bumped
func tagPage(tree *models.HashTree, bs *fs.LocalBlobStorage, docID, pageID string, tags []string, nowMs int64) (added []string, err error) {
	added = []string{}
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
		live := false
		for _, id := range ParseContent(raw).PageOrder {
			live = live || id == pageID
		}
		if !live {
			return ErrNoPage
		}
		patched, a, err := PatchContentPageTags(raw, pageID, tags, nowMs)
		if err != nil {
			return err
		}
		if len(a) == 0 {
			added = a
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
		added = a
		return fs.CommitDocMetadata(t, doc, bs, sha256Hex(patchedMeta), bytes.NewReader(patchedMeta))
	})
	if errors.Is(err, errTagUnchanged) {
		return added, nil
	}
	if err != nil {
		return []string{}, err
	}
	return added, nil
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
