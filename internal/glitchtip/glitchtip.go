// Package glitchtip sends errors to GlitchTip (Sentry envelope protocol)
// without an SDK: a logrus hook for Error and worse, and a gin middleware for
// handler panics. A nil *Client is valid and does nothing, so callers never
// need to check whether reporting is configured.
package glitchtip

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	queueSize = 64
	// RateLimit events per minute per process; the rest are dropped.
	RateLimit   = 30
	sendTimeout = 3 * time.Second
)

// Client sends events to one GlitchTip project.
type Client struct {
	url     string
	auth    string
	release string
	http    *http.Client
	queue   chan []byte
	pending sync.WaitGroup

	mu     sync.Mutex
	window time.Time
	count  int
	now    func() time.Time
}

// New parses a DSN ("http://key@host/project"). An empty DSN returns nil, nil.
func New(dsn, release string) (*Client, error) {
	if dsn == "" {
		return nil, nil
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil || u.User.Username() == "" || u.Host == "" {
		return nil, fmt.Errorf("glitchtip: bad DSN")
	}
	project := strings.Trim(u.Path, "/")
	if project == "" {
		return nil, fmt.Errorf("glitchtip: DSN has no project id")
	}
	c := &Client{
		url:     fmt.Sprintf("%s://%s/api/%s/envelope/", u.Scheme, u.Host, project),
		auth:    "Sentry sentry_version=7, sentry_client=rmfakecloud-glitchtip/1, sentry_key=" + u.User.Username(),
		release: release,
		http:    &http.Client{Timeout: sendTimeout},
		queue:   make(chan []byte, queueSize),
		now:     time.Now,
	}
	go c.worker()
	return c, nil
}

// Event is one error report. Frames are oldest first.
type Event struct {
	Level  string // "error", "fatal"
	Type   string
	Value  string
	Frames []Frame
	Extra  map[string]any
	// Tags are searchable in GlitchTip (trace_id, stage, ...)
	Tags    map[string]string
	Request *Request
}

// Request is the HTTP request an event happened in.
type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

// Capture queues an event and returns its id, or "" when reporting is off,
// rate limited or the queue is full. It never blocks.
func (c *Client) Capture(ev Event) string {
	if c == nil || !c.allow() {
		return ""
	}
	id, body := c.envelope(ev)
	c.pending.Add(1)
	select {
	case c.queue <- body:
		return id
	default:
		c.pending.Done()
		return ""
	}
}

// CaptureSync sends an event now and returns the id GlitchTip accepted.
// For the last words of a process (Fatal, Panic) and for tests of the wiring.
func (c *Client) CaptureSync(ev Event) (string, error) {
	if c == nil {
		return "", fmt.Errorf("glitchtip: not configured")
	}
	if !c.allow() {
		return "", fmt.Errorf("glitchtip: rate limited")
	}
	_, body := c.envelope(ev)
	return c.send(body)
}

// Flush waits until queued events are sent, at most timeout. False on timeout.
func (c *Client) Flush(timeout time.Duration) bool {
	if c == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		c.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *Client) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.window) >= time.Minute {
		c.window, c.count = now, 0
	}
	if c.count >= RateLimit {
		return false
	}
	c.count++
	return true
}

func (c *Client) worker() {
	for body := range c.queue {
		if _, err := c.send(body); err != nil {
			// not logrus: this runs under its hook and must not report itself
			fmt.Fprintln(os.Stderr, err)
		}
		c.pending.Done()
	}
}

func (c *Client) send(body []byte) (id string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("glitchtip: send panicked: %v", r)
		}
	}()
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", c.auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("glitchtip: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("glitchtip: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &out) != nil || out.ID == "" {
		return "", fmt.Errorf("glitchtip: not accepted: %s", bytes.TrimSpace(data))
	}
	return out.ID, nil
}

func (c *Client) envelope(ev Event) (string, []byte) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	ts := c.now().UTC().Format(time.RFC3339Nano)
	level := ev.Level
	if level == "" {
		level = "error"
	}
	exc := map[string]any{"type": ev.Type, "value": ev.Value}
	if len(ev.Frames) > 0 {
		exc["stacktrace"] = map[string]any{"frames": ev.Frames}
	}
	event := map[string]any{
		"event_id":  id,
		"timestamp": ts,
		"platform":  "go",
		"level":     level,
		"logger":    "rmfakecloud",
		"exception": map[string]any{"values": []any{exc}},
	}
	if c.release != "" {
		event["release"] = c.release
	}
	if host, err := os.Hostname(); err == nil {
		event["server_name"] = host
	}
	if len(ev.Extra) > 0 {
		event["extra"] = ev.Extra
	}
	if len(ev.Tags) > 0 {
		event["tags"] = ev.Tags
	}
	if ev.Request != nil {
		event["request"] = ev.Request
	}
	var buf bytes.Buffer
	enc := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			data, _ = json.Marshal(map[string]any{"event_id": id, "message": "glitchtip: unencodable event"})
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	enc(map[string]any{"event_id": id, "sent_at": ts})
	enc(map[string]any{"type": "event"})
	enc(event)
	return id, buf.Bytes()
}
