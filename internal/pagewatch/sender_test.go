package pagewatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type seenReq struct {
	path, trace, method string
	body                map[string]json.RawMessage
}

func fakeRMPages(t *testing.T, status int, resp string) (*httptest.Server, *[]seenReq) {
	t.Helper()
	var seen []seenReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("bad body %q: %v", b, err)
		}
		seen = append(seen, seenReq{path: r.URL.Path, trace: r.Header.Get("X-Trace-Id"), method: r.Method, body: body})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestSenderPostsEventsAndParsesAccepted(t *testing.T) {
	srv, seen := fakeRMPages(t, 200, `{"accepted":["a","c"]}`)
	s := NewHTTPSender(srv.URL+"/", srv.Client())
	acc, err := s.Send(context.Background(), "u1", []Event{{TraceID: "a", Kind: KindEdit}, {TraceID: "b", Kind: KindEdit}, {TraceID: "c", Kind: KindOpen}})
	if err != nil {
		t.Fatal(err)
	}
	if !acc["a"] || acc["b"] || !acc["c"] {
		t.Fatalf("accepted = %v", acc)
	}
	r := (*seen)[0]
	if r.method != "POST" || r.path != "/api/events" || r.trace != "a" {
		t.Fatalf("request = %+v", r)
	}
	var user string
	var evs []Event
	_ = json.Unmarshal(r.body["user"], &user)
	_ = json.Unmarshal(r.body["events"], &evs)
	if user != "u1" || len(evs) != 3 || evs[2].TraceID != "c" {
		t.Fatalf("body user=%q events=%v", user, evs)
	}
}

func TestSenderHeartbeatWhenNoEvents(t *testing.T) {
	srv, seen := fakeRMPages(t, 200, `{"accepted":[]}`)
	s := NewHTTPSender(srv.URL, nil)
	acc, err := s.Send(context.Background(), "u1", nil)
	if err != nil || len(acc) != 0 {
		t.Fatalf("acc=%v err=%v", acc, err)
	}
	r := (*seen)[0]
	if r.trace != "heartbeat" || string(r.body["events"]) != "[]" {
		t.Fatalf("request = %+v events=%s", r, r.body["events"])
	}
}

func TestSender5xxReturnsError(t *testing.T) {
	srv, _ := fakeRMPages(t, 503, `down`)
	s := NewHTTPSender(srv.URL, srv.Client())
	acc, err := s.Send(context.Background(), "u1", []Event{{TraceID: "a"}})
	if err == nil || len(acc) != 0 {
		t.Fatalf("acc=%v err=%v", acc, err)
	}
}
