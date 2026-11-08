package models

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// mapStorage a RemoteStorage backed by an in-memory hash -> bytes map.
type mapStorage struct {
	blobs map[string][]byte
}

func (m mapStorage) GetRootIndex() (string, int64, error) { return "", 0, nil }

func (m mapStorage) GetReader(hash string) (io.ReadCloser, error) {
	b, ok := m.blobs[hash]
	if !ok {
		return nil, fmt.Errorf("no blob %q", hash)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func testRmBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "third_party", "rmapi", "encoding", "rm", "test_v5.rm"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A real firmware 3.x .content: formatVersion 2, no flat "pages" key,
// cPages pages out of idx order, one deleted page, object pageTags. Before
// the fix ArchiveFromHashDoc returned zero pages for this doc and the
// object pageTags failed the unmarshal outright, so the old exporter
// reported "the document has no pages".
const v3Content = `{
	"fileType": "notebook",
	"formatVersion": 2,
	"pageCount": 2,
	"cPages": {"pages": [
		{"id": "page-b", "idx": {"timestamp": "1:2", "value": "ab"}, "redir": {"value": 0}},
		{"id": "page-a", "idx": {"timestamp": "1:2", "value": "aa"}},
		{"id": "page-gone", "idx": {"timestamp": "1:2", "value": "ac"}, "deleted": {"timestamp": "1:2", "value": 1}}
	]},
	"pageTags": [{"name": "todo", "pageId": "page-a", "timestamp": 1793543844647}]
}`

func TestArchiveFromHashDocCPages(t *testing.T) {
	rm := testRmBytes(t)
	rs := mapStorage{blobs: map[string][]byte{
		"content-hash": []byte(v3Content),
		"rm-a":         rm,
		"rm-b":         rm,
	}}
	doc := &HashDoc{HashEntry: HashEntry{EntryName: "doc-uuid"}}
	doc.Files = []*HashEntry{
		{Hash: "content-hash", EntryName: "doc-uuid.content"},
		{Hash: "rm-a", EntryName: "page-a.rm"},
		{Hash: "rm-b", EntryName: "page-b.rm"},
	}

	a, err := ArchiveFromHashDoc(doc, rs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(a.Pages), 2; got != want {
		t.Fatalf("Pages = %d, want %d (deleted page skipped)", got, want)
	}
	// idx order: page-a ("aa") before page-b ("ab")
	if a.Pages[0].DocPage != -1 {
		t.Fatalf("page-a DocPage = %d, want -1 (no redir)", a.Pages[0].DocPage)
	}
	if a.Pages[1].DocPage != 0 {
		t.Fatalf("page-b DocPage = %d, want 0 (redir.value)", a.Pages[1].DocPage)
	}
	if a.Pages[0].Data == nil || a.Pages[1].Data == nil {
		t.Fatal("pages have no parsed .rm data")
	}
	if got, want := a.Content.Tags, []string{"todo"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Content.Tags = %v, want %v", got, want)
	}
}

// The flat v1 path is unchanged: pages come from the "pages" list in array
// order, and object pageTags still parse.
func TestArchiveFromHashDocFlatPagesUnchanged(t *testing.T) {
	rm := testRmBytes(t)
	rs := mapStorage{blobs: map[string][]byte{
		"content-hash": []byte(`{
			"fileType": "notebook",
			"formatVersion": 1,
			"pageCount": 2,
			"pages": ["page-a", "page-b"],
			"redirectionPageMap": [0, 1],
			"pageTags": [{"name": "todo", "pageId": "page-a", "timestamp": 1793543844647}]
		}`),
		"rm-a": rm,
		"rm-b": rm,
	}}
	doc := &HashDoc{HashEntry: HashEntry{EntryName: "doc-uuid"}}
	doc.Files = []*HashEntry{
		{Hash: "content-hash", EntryName: "doc-uuid.content"},
		{Hash: "rm-a", EntryName: "page-a.rm"},
		{Hash: "rm-b", EntryName: "page-b.rm"},
	}

	a, err := ArchiveFromHashDoc(doc, rs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(a.Pages), 2; got != want {
		t.Fatalf("Pages = %d, want %d", got, want)
	}
}
