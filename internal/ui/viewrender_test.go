package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/testkit/tablet"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type stubPages struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []renderRequest
	status int
}

func newStubPages(t *testing.T, status int) *stubPages {
	t.Helper()
	s := &stubPages{status: status}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rr renderRequest
		if r.URL.Path != "/api/render/doc" || r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&rr) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.bodies = append(s.bodies, rr)
		s.mu.Unlock()
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-stub-" + rr.DocID))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newViewBackend(t *testing.T, url string) (*backend15, *tablet.Tablet) {
	t.Helper()
	log.SetLevel(log.PanicLevel)
	st := tablet.NewStorage(t.TempDir())
	if err := tablet.RegisterUser(st, "mike", true); err != nil {
		t.Fatal(err)
	}
	return &backend15{blobHandler: st, rmPagesURL: url}, tablet.New(t, st, "mike")
}

func export(t *testing.T, b *backend15, id string) (string, error) {
	t.Helper()
	r, err := b.Export("mike", id, "pdf", 0)
	if err != nil {
		return "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	return string(data), err
}

func TestExportPDFUsesRmPages(t *testing.T) {
	stub := newStubPages(t, 200)
	b, tab := newViewBackend(t, stub.srv.URL)
	tab.Notebook("nb", "Notes", "")
	for _, p := range []string{"p1", "p2", "p3"} {
		if err := tab.SetPage("nb", p, []byte("rm-"+p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tab.MovePageBefore("nb", "p3", "p1"); err != nil {
		t.Fatal(err)
	}
	delete(tab.Docs["nb"].RM, "p2") // blank page: in cPages, no .rm
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := export(t, b, "nb")
	if err != nil || got != "%PDF-stub-nb" {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(stub.bodies) != 1 {
		t.Fatalf("requests: %d", len(stub.bodies))
	}
	rr := stub.bodies[0]
	if rr.User != "mike" || rr.DocID != "nb" || rr.DocType != "notebook" || rr.PDFHash != "" {
		t.Fatalf("request: %+v", rr)
	}
	if len(rr.Pages) != 3 || rr.Pages[0].PageID != "p3" || rr.Pages[1].PageID != "p1" || rr.Pages[2].PageID != "p2" {
		t.Fatalf("pages: %+v", rr.Pages)
	}
	if rr.Pages[0].Hash == "" || rr.Pages[1].Hash == "" || rr.Pages[2].Hash != "" {
		t.Fatalf("hashes (blank page must have none): %+v", rr.Pages)
	}
}

func TestExportPDFv1PageOrderAndPayload(t *testing.T) {
	stub := newStubPages(t, 200)
	b, tab := newViewBackend(t, stub.srv.URL)
	d := tab.PDFv1("pd", "Book", "", []string{"z", "a", "m"})
	d.RM = map[string][]byte{"z": []byte("rz"), "m": []byte("rm")}
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := export(t, b, "pd"); err != nil {
		t.Fatal(err)
	}
	rr := stub.bodies[0]
	if rr.DocType != "pdf" || rr.PDFHash == "" {
		t.Fatalf("request: %+v", rr)
	}
	if len(rr.Pages) != 3 || rr.Pages[0].PageID != "z" || rr.Pages[1].PageID != "a" || rr.Pages[2].PageID != "m" {
		t.Fatalf("pages: %+v", rr.Pages)
	}
	if rr.Pages[1].Hash != "" || rr.Pages[0].Hash == "" {
		t.Fatalf("hashes: %+v", rr.Pages)
	}
}

func TestExportPDFFallsBackWhenRmPagesFails(t *testing.T) {
	stub := newStubPages(t, 500)
	b, tab := newViewBackend(t, stub.srv.URL)
	d := tab.PDFv1("pd", "Book", "", []string{"a"})
	d.RM = map[string][]byte{"a": []byte("not a v3/v5 page")}
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := export(t, b, "pd")
	if err != nil || got != "%PDF-1.4 pd" {
		t.Fatalf("want the raw payload, got %q, %v", got, err)
	}
	if len(stub.bodies) != 1 {
		t.Fatalf("rm-pages asked %d times", len(stub.bodies))
	}
}

func TestExportPDFNoRmPagesURLKeepsOldBehaviour(t *testing.T) {
	stub := newStubPages(t, 200)
	b, tab := newViewBackend(t, "")
	d := tab.PDFv1("pd", "Book", "", []string{"a"})
	d.RM = map[string][]byte{"a": []byte("not a v3/v5 page")}
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := export(t, b, "pd")
	if err != nil || got != "%PDF-1.4 pd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(stub.bodies) != 0 {
		t.Fatal("rm-pages must not be called")
	}
}

func TestExportPDFUnreadablePagesSkipsRmPages(t *testing.T) {
	stub := newStubPages(t, 200)
	b, tab := newViewBackend(t, stub.srv.URL)
	d := tab.PDF("pd", "Book", "", 3, 0, time.Time{})
	d.RawContent = []byte(`{"fileType":"pdf","pageCount":3}`)
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := export(t, b, "pd")
	if err != nil || got != "%PDF-1.4 pd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(stub.bodies) != 0 {
		t.Fatal("rm-pages must not be called for an unreadable page list")
	}
}

func TestExportPDFErrorWithoutPayload(t *testing.T) {
	stub := newStubPages(t, 500)
	b, tab := newViewBackend(t, stub.srv.URL)
	tab.Notebook("nb", "Notes", "")
	if err := tab.SetPage("nb", "p1", []byte("not a v3/v5 page")); err != nil {
		t.Fatal(err)
	}
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	if got, err := export(t, b, "nb"); err == nil {
		t.Fatalf("want an error, got %q", got)
	}
}

func TestExportPDFSendsRedir(t *testing.T) {
	stub := newStubPages(t, 200)
	b, tab := newViewBackend(t, stub.srv.URL)
	// v1: redirectionPageMap aligned with pages
	d := tab.PDFv1("v1", "Book", "", []string{"z", "a", "m"})
	d.Content["redirectionPageMap"] = []any{0, -1, 1}
	// v2 PDF: one page without redir, one with
	tab.PDF("v2", "Notes", "", 2, 0, time.Time{}).Content["cPages"] = map[string]any{"pages": []any{
		map[string]any{"id": "p1", "idx": map[string]any{"value": "aa"}, "redir": map[string]any{"value": 0}},
		map[string]any{"id": "p2", "idx": map[string]any{"value": "ab"}},
	}}
	tab.Notebook("nb", "NB", "")
	if err := tab.SetPage("nb", "n1", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tab.Sync(); err != nil {
		t.Fatal(err)
	}
	reqs := map[string]renderRequest{}
	for _, id := range []string{"v1", "v2", "nb"} {
		export(t, b, id)
	}
	for _, r := range stub.bodies {
		reqs[r.DocID] = r
	}
	val := func(p renderPage) any {
		if p.Redir == nil {
			return nil
		}
		return *p.Redir
	}
	v1 := reqs["v1"].Pages
	if val(v1[0]) != 0 || val(v1[1]) != -1 || val(v1[2]) != 1 {
		t.Fatalf("v1 redir: %+v", v1)
	}
	v2 := reqs["v2"].Pages
	if val(v2[0]) != 0 || val(v2[1]) != -1 {
		t.Fatalf("v2 redir: %+v", v2)
	}
	if reqs["nb"].Pages[0].Redir != nil {
		t.Fatal("notebook must not send redir")
	}
}

func TestExportPDFRetriesOnceOn503(t *testing.T) {
	retryWait = time.Millisecond
	defer func() { retryWait = time.Second }()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("%PDF-after-retry"))
	}))
	defer srv.Close()
	b, tab := newViewBackend(t, srv.URL)
	tab.Notebook("nb", "N", "")
	tab.SetPage("nb", "p1", []byte("x"))
	tab.Sync()
	got, err := export(t, b, "nb")
	if err != nil || got != "%PDF-after-retry" || calls != 2 {
		t.Fatalf("got %q, %v, calls %d", got, err, calls)
	}
}

func TestExportPDFBusyFallsBackWithoutError(t *testing.T) {
	retryWait = time.Millisecond
	defer func() { retryWait = time.Second }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	hook := logtest.NewGlobal()
	b, tab := newViewBackend(t, srv.URL)
	log.SetLevel(log.DebugLevel)
	d := tab.PDFv1("pd", "Book", "", []string{"a"})
	d.RM = map[string][]byte{"a": []byte("junk")}
	tab.Sync()
	got, err := export(t, b, "pd")
	if err != nil || got != "%PDF-1.4 pd" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, e := range hook.AllEntries() {
		if e.Level <= log.ErrorLevel && strings.Contains(e.Message, "rm-pages") {
			t.Fatalf("503 logged at %v: %s", e.Level, e.Message)
		}
	}
}

func TestRawPayloadOnlyServesPDF(t *testing.T) {
	stub := newStubPages(t, 500)
	b, tab := newViewBackend(t, stub.srv.URL)
	tab.Notebook("nb", "N", "")
	tab.SetPage("nb", "p1", []byte("junk"))
	tab.Sync()
	if _, err := rawPayload(b.blobHandler, "mike", "nb"); err == nil {
		t.Fatal("notebook has no payload")
	}
}
