package tablet

import (
	"bytes"
	"testing"
	"time"
)

func TestPDFPayloadSurvivesLoadAndSync(t *testing.T) {
	st := NewStorage(t.TempDir())
	if err := RegisterUser(st, "u", true); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4 real bytes")
	ft := New(t, st, "u")
	ft.PDF("d", "Report", "", 0, 0, time.Time{}).PDF = pdf
	if err := ft.SetPage("d", "p1", []byte("rm")); err != nil {
		t.Fatal(err)
	}
	if err := ft.SetRedir("d", "p1", 0); err != nil {
		t.Fatal(err)
	}
	if err := ft.TagDoc("d", "extract", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := ft.Sync(); err != nil {
		t.Fatal(err)
	}
	// a second tool run: load from the server, change something, sync again
	ft2 := New(t, st, "u")
	if err := ft2.Load(); err != nil {
		t.Fatal(err)
	}
	if err := ft2.SetPage("d", "p2", []byte("rm2")); err != nil {
		t.Fatal(err)
	}
	if err := ft2.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := ft2.ServerBlob("d", "d.pdf"); !bytes.Equal(got, pdf) {
		t.Fatalf("pdf = %q", got)
	}
	if tags := ft2.ServerContent("d")["tags"].([]any); len(tags) != 1 {
		t.Fatalf("tags = %v", tags)
	}
}
