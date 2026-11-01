package pagewatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SendTimeout the longest one POST to rm-pages may take
const SendTimeout = 10 * time.Second

// Sender delivers one sweep's events for one user to rm-pages. accepted holds
// the trace ids rm-pages took; an id missing from it was not accepted. An
// empty event list is a heartbeat: rm-pages records the contact.
type Sender interface {
	Send(ctx context.Context, user string, events []Event) (accepted map[string]bool, err error)
}

type httpSender struct {
	base   string
	client *http.Client
}

// NewHTTPSender POSTs to {baseURL}/api/events. A nil client gets a default one.
func NewHTTPSender(baseURL string, client *http.Client) Sender {
	if client == nil {
		client = &http.Client{}
	}
	return &httpSender{base: strings.TrimRight(baseURL, "/"), client: client}
}

func (h *httpSender) Send(ctx context.Context, user string, events []Event) (map[string]bool, error) {
	if events == nil {
		events = []Event{}
	}
	body, err := json.Marshal(struct {
		User   string  `json:"user"`
		Events []Event `json:"events"`
	}{user, events})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/api/events", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	trace := "heartbeat"
	if len(events) > 0 {
		trace = events[0].TraceID
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Trace-Id", trace)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("rm-pages answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("rm-pages answer is not JSON: %w", err)
	}
	acc := make(map[string]bool, len(out.Accepted))
	for _, id := range out.Accepted {
		acc[id] = true
	}
	return acc, nil
}
