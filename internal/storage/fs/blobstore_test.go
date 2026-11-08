package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danjacques/gofslock/fslock"
	"github.com/ddvk/rmfakecloud/internal/config"
)

func newTestBlobStorage(t *testing.T, uid string) *FileSystemStorage {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}
	fs := NewStorage(cfg)
	if err := os.MkdirAll(fs.getUserBlobPath(uid), 0700); err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestUpdateBlobDocumentMovesAndRenames(t *testing.T) {
	uid := "tester"
	fs := newTestBlobStorage(t, uid)

	folder, err := fs.CreateBlobFolder(uid, "Folder", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := fs.CreateBlobDocument(uid, "report.pdf", "", strings.NewReader("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}

	if err := fs.UpdateBlobDocument(uid, doc.ID, "renamed", folder.ID); err != nil {
		t.Fatal(err)
	}

	// drop the cache so the tree is rebuilt from the blobs alone
	if err := os.Remove(filepath.Join(fs.UserDataPath(uid), cachedTreeName)); err != nil {
		t.Fatal(err)
	}
	tree, err := fs.GetCachedTree(uid)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := tree.FindDoc(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Parent != folder.ID {
		t.Errorf("parent = %q, want %q", moved.Parent, folder.ID)
	}
	if moved.DocumentName != "renamed" {
		t.Errorf("name = %q, want renamed", moved.DocumentName)
	}
	if moved.Version != 2 {
		t.Errorf("version = %d, want 2", moved.Version)
	}
}

func TestUpdateBlobDocumentEmptyNameKeepsName(t *testing.T) {
	uid := "tester"
	fs := newTestBlobStorage(t, uid)
	folder, _ := fs.CreateBlobFolder(uid, "Folder", "")
	doc, _ := fs.CreateBlobDocument(uid, "report.pdf", "", strings.NewReader("%PDF-1.4"))
	if err := fs.UpdateBlobDocument(uid, doc.ID, "", folder.ID); err != nil {
		t.Fatal(err)
	}
	tree, _ := fs.GetCachedTree(uid)
	moved, _ := tree.FindDoc(doc.ID)
	if moved.DocumentName != "report" || moved.Parent != folder.ID {
		t.Fatalf("name %q parent %q", moved.DocumentName, moved.Parent)
	}
}

func TestUpdateBlobDocumentRefusesBadParents(t *testing.T) {
	uid := "tester"
	fs := newTestBlobStorage(t, uid)
	outer, _ := fs.CreateBlobFolder(uid, "Outer", "")
	inner, _ := fs.CreateBlobFolder(uid, "Inner", outer.ID)
	doc, _ := fs.CreateBlobDocument(uid, "report.pdf", "", strings.NewReader("%PDF-1.4"))
	for name, c := range map[string]struct{ id, parent string }{
		"into itself":       {outer.ID, outer.ID},
		"into a descendant": {outer.ID, inner.ID},
		"into a document":   {inner.ID, doc.ID},
		"into nothing":      {doc.ID, "no-such-folder"},
	} {
		if err := fs.UpdateBlobDocument(uid, c.id, "", c.parent); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// to the root and to the trash are fine
	for _, p := range []string{"", "trash"} {
		if err := fs.UpdateBlobDocument(uid, inner.ID, "", p); err != nil {
			t.Errorf("to %q: %v", p, err)
		}
	}
}

// GetCachedTree must ride out a briefly-held root lock (tablet sync writing
// the root blob, or a concurrent root read checksumming) instead of failing
// the read with ErrLockHeld.
func TestGetCachedTreeRetriesWhileRootLocked(t *testing.T) {
	uid := "tester"
	fs := newTestBlobStorage(t, uid)

	if _, err := fs.CreateBlobDocument(uid, "report.pdf", "", strings.NewReader("%PDF-1.4")); err != nil {
		t.Fatal(err)
	}

	historyPath := filepath.Join(fs.getUserBlobPath(uid), historyFile)
	lock, err := fslock.Lock(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := fs.GetCachedTree(uid)
		done <- err
	}()

	// Hold the lock past the first retry sleeps, then release: the retry
	// must pick the tree up instead of failing with ErrLockHeld.
	time.Sleep(100 * time.Millisecond)
	lock.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GetCachedTree = %v, want nil after lock released", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GetCachedTree did not return after lock released")
	}
}

// ... but a lock held past the whole retry budget must still surface
// ErrLockHeld instead of hanging or masking the error.
func TestGetCachedTreeStillFailsWhenRootLocked(t *testing.T) {
	uid := "tester"
	fs := newTestBlobStorage(t, uid)

	if _, err := fs.CreateBlobDocument(uid, "report.pdf", "", strings.NewReader("%PDF-1.4")); err != nil {
		t.Fatal(err)
	}

	historyPath := filepath.Join(fs.getUserBlobPath(uid), historyFile)
	lock, err := fslock.Lock(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	_, err = fs.GetCachedTree(uid)
	if !errors.Is(err, fslock.ErrLockHeld) {
		t.Fatalf("GetCachedTree err = %v, want ErrLockHeld", err)
	}
}
