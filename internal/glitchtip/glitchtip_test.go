package glitchtip_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/glitchtip"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// fakeGlitchtip records the event of every envelope it accepts.
type fakeGlitchtip struct {
	*httptest.Server
	mu     sync.Mutex
	events []map[string]any
	auth   []string
}

func newFake(t *testing.T) *fakeGlitchtip {
	f := &fakeGlitchtip{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/7/envelope/" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		sc := bufio.NewScanner(bytes.NewReader(body))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var lines [][]byte
		for sc.Scan() {
			lines = append(lines, append([]byte(nil), sc.Bytes()...))
		}
		if len(lines) != 3 {
			http.Error(w, "bad envelope", http.StatusBadRequest)
			return
		}
		var ev map[string]any
		if err := json.Unmarshal(lines[2], &ev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.events = append(f.events, ev)
		f.auth = append(f.auth, r.Header.Get("X-Sentry-Auth"))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"id": ev["event_id"].(string)})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGlitchtip) dsn() string {
	return strings.Replace(f.URL, "http://", "http://k3y@", 1) + "/7"
}

func (f *fakeGlitchtip) got() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.events...)
}

func exception(ev map[string]any) map[string]any {
	return ev["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)
}

func newClient(t *testing.T, f *fakeGlitchtip) *glitchtip.Client {
	c, err := glitchtip.New(f.dsn(), "1.2.3")
	if err != nil || c == nil {
		t.Fatalf("New: %v %v", c, err)
	}
	return c
}

func TestEmptyDSNIsOffAndNilClientIsSafe(t *testing.T) {
	c, err := glitchtip.New("", "x")
	if c != nil || err != nil {
		t.Fatalf("empty DSN: %v %v", c, err)
	}
	if id := c.Capture(glitchtip.Event{Value: "x"}); id != "" {
		t.Errorf("nil Capture returned %q", id)
	}
	if _, err := c.CaptureSync(glitchtip.Event{}); err == nil {
		t.Error("nil CaptureSync: no error")
	}
	if !c.Flush(time.Millisecond) {
		t.Error("nil Flush timed out")
	}
	for _, bad := range []string{"not a dsn", "http://host/7", "http://key@host/"} {
		if _, err := glitchtip.New(bad, ""); err == nil {
			t.Errorf("New(%q): no error", bad)
		}
	}
}

func TestCaptureSyncReturnsAcceptedID(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	id, err := c.CaptureSync(glitchtip.Event{Type: "test", Value: "wiring check"})
	if err != nil || len(id) != 32 {
		t.Fatalf("id %q err %v", id, err)
	}
	ev := f.got()[0]
	if ev["release"] != "1.2.3" || ev["platform"] != "go" || exception(ev)["value"] != "wiring check" {
		t.Errorf("event %v", ev)
	}
	if !strings.Contains(f.auth[0], "sentry_key=k3y") {
		t.Errorf("auth header %q", f.auth[0])
	}
}

func TestCaptureSyncFailsWhenNotAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c, _ := glitchtip.New(strings.Replace(srv.URL, "http://", "http://k@", 1)+"/7", "")
	if _, err := c.CaptureSync(glitchtip.Event{Value: "x"}); err == nil {
		t.Error("403 counted as accepted")
	}
}

func TestRateLimit(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	sent := 0
	for i := 0; i < glitchtip.RateLimit+5; i++ {
		if c.Capture(glitchtip.Event{Value: "x"}) != "" {
			sent++
		}
	}
	c.Flush(5 * time.Second)
	if sent != glitchtip.RateLimit || len(f.got()) != glitchtip.RateLimit {
		t.Errorf("queued %d, received %d, want %d", sent, len(f.got()), glitchtip.RateLimit)
	}
}

func TestHookReportsErrorsOnlyWithStackAndFields(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.AddHook(glitchtip.Hook{C: c})

	log.Warn("not reported")
	log.Info("not reported")
	log.WithField("user", "mike").Error("the document has no pages")
	c.Flush(5 * time.Second)

	got := f.got()
	if len(got) != 1 {
		t.Fatalf("%d events, want 1", len(got))
	}
	exc := exception(got[0])
	if exc["value"] != "the document has no pages" || exc["type"] != "log.Error" {
		t.Errorf("exception %v", exc)
	}
	if got[0]["extra"].(map[string]any)["user"] != "mike" {
		t.Errorf("extra %v", got[0]["extra"])
	}
	frames := exc["stacktrace"].(map[string]any)["frames"].([]any)
	last := frames[len(frames)-1].(map[string]any)
	if !strings.HasSuffix(last["function"].(string), "TestHookReportsErrorsOnlyWithStackAndFields") || last["in_app"] != true {
		t.Errorf("innermost frame %v", last)
	}
	for _, fr := range frames {
		if m, _ := fr.(map[string]any)["module"].(string); strings.Contains(m, "logrus") {
			t.Errorf("logrus frame kept: %v", fr)
		}
	}
}

func TestHookUsesErrorTypeFromWithError(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.AddHook(glitchtip.Hook{C: c})
	log.WithError(io.ErrUnexpectedEOF).Error("upload failed")
	c.Flush(5 * time.Second)
	if typ := exception(f.got()[0])["type"]; typ != "*errors.errorString" {
		t.Errorf("type %v", typ)
	}
}

func TestPanicLevelIsSentBeforeLogrusPanics(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.AddHook(glitchtip.Hook{C: c})
	func() {
		defer func() { _ = recover() }()
		log.Panic("boom")
	}()
	// no Flush: a fatal-level event must already be delivered
	got := f.got()
	if len(got) != 1 || got[0]["level"] != "fatal" || exception(got[0])["type"] != "log.Panic" {
		t.Errorf("events %v", got)
	}
}

func TestGinRecoveryReportsAndRepanics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFake(t)
	c := newClient(t, f)
	r := gin.New()
	r.Use(gin.CustomRecovery(func(ctx *gin.Context, _ any) { ctx.AbortWithStatus(http.StatusInternalServerError) }))
	r.Use(c.GinRecovery())
	r.GET("/boom", func(*gin.Context) { panic("handler exploded") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/boom?x=1", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d: the outer Recovery did not see the panic", w.Code)
	}
	c.Flush(5 * time.Second)
	got := f.got()
	if len(got) != 1 {
		t.Fatalf("%d events", len(got))
	}
	if exception(got[0])["value"] != "handler exploded" {
		t.Errorf("exception %v", exception(got[0]))
	}
	req := got[0]["request"].(map[string]any)
	if req["method"] != "GET" || req["url"] != "/boom?x=1" {
		t.Errorf("request %v", req)
	}
}

func TestNilClientGinRecoveryPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var c *glitchtip.Client
	r := gin.New()
	r.Use(c.GinRecovery())
	r.GET("/ok", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ok", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("status %d", w.Code)
	}
}

func TestEventTagsInEnvelope(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	if _, err := c.CaptureSync(glitchtip.Event{Value: "x", Tags: map[string]string{"trace_id": "abc", "stage": "deliver"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CaptureSync(glitchtip.Event{Value: "no tags"}); err != nil {
		t.Fatal(err)
	}
	got := f.got()
	tags, _ := got[0]["tags"].(map[string]any)
	if tags["trace_id"] != "abc" || tags["stage"] != "deliver" {
		t.Errorf("tags %v", got[0]["tags"])
	}
	if _, ok := got[1]["tags"]; ok {
		t.Errorf("empty tags sent: %v", got[1]["tags"])
	}
}

func TestHookLiftsTraceFieldsToTags(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.AddHook(glitchtip.Hook{C: c})

	log.WithFields(logrus.Fields{
		"trace_id": "t1", "stage": "detect", "doc_id": "d1", "page_id": "p1", "rule_id": "r1", "user": "mike",
	}).Error("lagging")
	c.Flush(5 * time.Second)

	ev := f.got()[0]
	tags, _ := ev["tags"].(map[string]any)
	for k, want := range map[string]string{"trace_id": "t1", "stage": "detect", "doc_id": "d1", "page_id": "p1", "rule_id": "r1"} {
		if tags[k] != want {
			t.Errorf("tag %s = %v, want %s", k, tags[k], want)
		}
	}
	extra, _ := ev["extra"].(map[string]any)
	if extra["user"] != "mike" {
		t.Errorf("extra %v", extra)
	}
	for _, k := range []string{"trace_id", "stage", "doc_id", "page_id", "rule_id"} {
		if _, ok := extra[k]; ok {
			t.Errorf("%s left in extra", k)
		}
	}
}
