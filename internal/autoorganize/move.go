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

// ErrVanished the document is no longer in the tree
var ErrVanished = errors.New("document vanished")

// NotQualifiedError the document no longer qualifies at move time
type NotQualifiedError struct {
	Reason string
}

func (e *NotQualifiedError) Error() string {
	return "no longer qualifies: " + e.Reason
}

// IsNotQualified a re-check refusal (vanished or no longer qualifies), not a failure
func IsNotQualified(err error) bool {
	var nq *NotQualifiedError
	return errors.Is(err, ErrVanished) || errors.As(err, &nq)
}

// moveDoc moves docID from `from` to `to` with fs.UpdateTree. The closure runs
// again after every generation conflict (UpdateTree re-mirrors and retries),
// so on every invocation it re-finds the document, re-checks that its parent
// is still `from` and that recheck still passes, and only then sets Parent
// and Version. DocumentName and every other field are left untouched.
func moveDoc(tree *models.HashTree, bs *fs.LocalBlobStorage, docID, from, to string, recheck func(t *models.HashTree, d *models.HashDoc) error) error {
	return fs.UpdateTree(tree, bs, func(t *models.HashTree) error {
		doc, err := t.FindDoc(docID)
		if err != nil || doc.Deleted {
			return ErrVanished
		}
		if doc.Parent != from {
			return &NotQualifiedError{Reason: "no longer in the source folder"}
		}
		if recheck != nil {
			if err := recheck(t, doc); err != nil {
				return err
			}
		}
		var meta *models.HashEntry
		for _, f := range doc.Files {
			if f.IsMetadata() {
				meta = f
				break
			}
		}
		if meta == nil {
			return errors.New("document has no metadata entry")
		}
		r, err := bs.GetReader(meta.Hash)
		if err != nil {
			return fmt.Errorf("cannot read metadata: %w", err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return fmt.Errorf("cannot read metadata: %w", err)
		}
		version := doc.Version + 1
		patched, err := PatchMetadata(raw, to, version)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(patched)
		hash := hex.EncodeToString(sum[:])

		// every fallible read is done; mutate the document
		doc.Parent = to
		doc.Version = version
		return fs.CommitDocMetadata(t, doc, bs, hash, bytes.NewReader(patched))
	})
}

// PatchMetadata sets parent and version in a raw .metadata blob and keeps every
// other key exactly, including keys the server's MetadataFile struct does not
// know about (§10: the struct would drop them on re-marshal).
func PatchMetadata(raw []byte, parent string, version int) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	m := map[string]any{}
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("cannot parse metadata: %w", err)
	}
	m["parent"] = parent
	m["version"] = version
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
