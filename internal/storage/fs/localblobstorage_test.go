package fs

import (
	"strings"
	"testing"
	"time"
)

func TestModTimeReturnsFileMtime(t *testing.T) {
	uid := "tester"
	st := newTestBlobStorage(t, uid)
	bs := st.BlobStorage(uid)
	before := time.Now().Add(-2 * time.Second)
	if err := bs.Write("abc123", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	mt, err := bs.ModTime("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if mt.Before(before) || mt.After(time.Now().Add(time.Second)) {
		t.Fatalf("mtime %v not near now", mt)
	}
	if _, err := bs.ModTime("missing"); err != ErrorNotFound {
		t.Fatalf("missing err = %v", err)
	}
}
