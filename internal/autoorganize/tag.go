package autoorganize

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ddvk/rmfakecloud/internal/storage/fs"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

// errTagUnchanged the document already has (add) or lacks (remove) the tag;
// returned from the UpdateTree closure so nothing is written
var errTagUnchanged = errors.New("tag already as wanted")

// tagDoc adds or removes a document tag on docID with fs.UpdateTree. Like
// moveDoc, the closure runs again after every generation conflict, so on
// every invocation it re-finds the document, runs recheck and re-reads the
// .content blob it patches; only the "tags" key changes. The metadata
// version is bumped as a move does; lastModified is left alone, so a tag
// change never reads as an edit (modified_time). changed false: the tag was
// already as wanted and nothing was written.
func tagDoc(tree *models.HashTree, bs *fs.LocalBlobStorage, docID, tag string, add bool, nowMs int64, recheck func(t *models.HashTree, d *models.HashDoc) error) (changed bool, err error) {
	err = fs.UpdateTree(tree, bs, func(t *models.HashTree) error {
		doc, err := t.FindDoc(docID)
		if err != nil || doc.Deleted {
			return ErrVanished
		}
		if recheck != nil {
			if err := recheck(t, doc); err != nil {
				return err
			}
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
		patched, ok, err := PatchContentTags(raw, tag, add, nowMs)
		if err != nil {
			return err
		}
		if !ok {
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

		// every fallible read is done; mutate the document
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

func readBlob(bs *fs.LocalBlobStorage, hash string) ([]byte, error) {
	r, err := bs.GetReader(hash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PatchContentTags adds tag to, or removes it from, the "tags" list of a raw
// .content blob and keeps every other key exactly (as PatchMetadata does).
// Tags match after normalisation (NormalizeTag), so removing "New" removes
// "new". An added tag is written as the tablet writes it,
// {"name": tag, "timestamp": nowMs}, or as a bare string when the list
// already holds strings. ok false: the tag was already as wanted.
func PatchContentTags(raw []byte, tag string, add bool, nowMs int64) (out []byte, ok bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return nil, false, fmt.Errorf("cannot parse content: %w", err)
	}
	want := NormalizeTag(tag)
	if want == "" {
		return nil, false, errors.New("tag cannot be empty")
	}
	var tags []any
	if v, present := m["tags"]; present && v != nil {
		list, isList := v.([]any)
		if !isList {
			return nil, false, errors.New("content tags is not a list")
		}
		tags = list
	}
	nameOf := func(t any) string {
		switch v := t.(type) {
		case string:
			return v
		case map[string]any:
			s, _ := v["name"].(string)
			return s
		}
		return ""
	}
	kept := make([]any, 0, len(tags)+1)
	found, bare := false, len(tags) > 0
	for _, t := range tags {
		if _, isString := t.(string); !isString {
			bare = false
		}
		if NormalizeTag(nameOf(t)) == want {
			found = true
			if !add {
				continue
			}
		}
		kept = append(kept, t)
	}
	if found == add {
		return nil, false, nil
	}
	if add {
		if bare {
			kept = append(kept, tag)
		} else {
			kept = append(kept, map[string]any{"name": tag, "timestamp": nowMs})
		}
	}
	m["tags"] = kept
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true, nil
}
