package slackclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSlack is a minimal HTTP stand-in for the Slack Web API. Handlers are
// keyed by method path (e.g. "conversations.history"). No real token is used.
type fakeSlack struct {
	mu       *http.ServeMux
	server   *httptest.Server
	lastForm map[string]string
}

func newFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	f := &fakeSlack{mu: http.NewServeMux()}
	f.server = httptest.NewServer(f.mu)
	t.Cleanup(f.server.Close)
	return f
}

// handle registers a JSON responder for a Slack method, capturing the last
// received form values so tests can assert what the adapter sent.
func (f *fakeSlack) handle(method, body string) {
	f.mu.HandleFunc("/"+method, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.lastForm = map[string]string{}
		for k := range r.Form {
			f.lastForm[k] = r.Form.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func (f *fakeSlack) api(t *testing.T, opts ...Option) API {
	t.Helper()
	base := append([]Option{
		WithAPIURL(f.server.URL + "/"),
		WithHTTPClient(f.server.Client()),
	}, opts...)
	return New("xoxb-fake-token-for-tests", base...)
}

func TestAdapter_ConversationInfo(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.handle("conversations.info", `{
		"ok": true,
		"channel": {
			"id": "C01234567", "name": "general", "is_channel": true,
			"is_private": true, "is_member": true, "num_members": 12,
			"created": 1609459200,
			"topic": {"value": "team topic"}, "purpose": {"value": "team purpose"}
		}
	}`)

	meta, err := f.api(t).ConversationInfo(context.Background(), "C01234567")
	if err != nil {
		t.Fatalf("ConversationInfo: %v", err)
	}
	if meta.ID != "C01234567" || meta.Name != "general" || !meta.IsPrivate || !meta.IsMember {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if meta.Topic != "team topic" || meta.Purpose != "team purpose" || meta.NumMembers != 12 {
		t.Errorf("shaping dropped fields: %+v", meta)
	}
	if meta.Created != 1609459200 {
		t.Errorf("created = %d", meta.Created)
	}
	if f.lastForm["channel"] != "C01234567" {
		t.Errorf("channel not sent, form=%v", f.lastForm)
	}
}

func TestAdapter_History_ShapesAndPaginates(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.handle("conversations.history", `{
		"ok": true,
		"has_more": true,
		"messages": [
			{"type":"message","user":"U1","text":"hello","ts":"1699999999.000100"},
			{"type":"message","bot_id":"B1","username":"bot","text":"hi","ts":"1699999999.000200","reply_count":3,"thread_ts":"1699999999.000200","edited":{"user":"U1","ts":"1699999999.000300"}}
		],
		"response_metadata": {"next_cursor": "CURSOR_ABC"}
	}`)

	page, err := f.api(t).ConversationHistory(context.Background(), HistoryParams{
		ChannelID: "C01234567", Limit: 50, Oldest: "1699999999.000000", Inclusive: true,
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if !page.HasMore || page.NextCursor != "CURSOR_ABC" {
		t.Errorf("pagination wrong: %+v", page)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("want 2 messages, got %d", len(page.Messages))
	}
	m := page.Messages[1]
	if m.BotID != "B1" || m.ReplyCount != 3 || m.EditedTS != "1699999999.000300" {
		t.Errorf("message shaping wrong: %+v", m)
	}
	if f.lastForm["limit"] != "50" || f.lastForm["oldest"] != "1699999999.000000" || f.lastForm["inclusive"] != "1" {
		t.Errorf("params not forwarded: %v", f.lastForm)
	}
}

func TestAdapter_Replies(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.handle("conversations.replies", `{
		"ok": true,
		"has_more": false,
		"messages": [{"type":"message","user":"U1","text":"root","ts":"1699999999.000100","thread_ts":"1699999999.000100"}],
		"response_metadata": {"next_cursor": ""}
	}`)

	page, err := f.api(t).ConversationReplies(context.Background(), RepliesParams{
		ChannelID: "C01234567", ThreadTS: "1699999999.000100", Limit: 10,
	})
	if err != nil {
		t.Fatalf("replies: %v", err)
	}
	if len(page.Messages) != 1 || page.HasMore || page.NextCursor != "" {
		t.Errorf("unexpected page: %+v", page)
	}
	if f.lastForm["ts"] != "1699999999.000100" {
		t.Errorf("thread ts not sent as ts: %v", f.lastForm)
	}
}

func TestAdapter_SlackErrorSanitized(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.handle("conversations.info", `{"ok": false, "error": "channel_not_found"}`)
	_, err := f.api(t).ConversationInfo(context.Background(), "C01234567")
	var e *Error
	if err == nil || !asError(err, &e) || e.Code != CodeChannelNotFound {
		t.Fatalf("want CHANNEL_NOT_FOUND, got %v", err)
	}
}

func TestAdapter_RateLimitRetriedThenOK(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	var calls int32
	f.mu.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{"next_cursor":""}}`))
	})

	// Fake sleeper so the 1s Retry-After does not actually delay the test.
	api := f.api(t, WithRetryPolicy(RetryPolicy{MaxRetries: 2, MaxRetryAfter: time.Minute}), withSleeper(func(context.Context, time.Duration) error { return nil }))
	_, err := api.ConversationHistory(context.Background(), HistoryParams{ChannelID: "C01234567"})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 upstream calls, got %d", calls)
	}
}

func TestAdapter_RateLimitExhaustedSanitized(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.mu.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	api := f.api(t, WithRetryPolicy(RetryPolicy{MaxRetries: 1, MaxRetryAfter: time.Minute}), withSleeper(func(context.Context, time.Duration) error { return nil }))
	_, err := api.ConversationHistory(context.Background(), HistoryParams{ChannelID: "C01234567"})
	var e *Error
	if err == nil || !asError(err, &e) || e.Code != CodeRateLimited {
		t.Fatalf("want RATE_LIMITED, got %v", err)
	}
}

func TestAdapter_ContextTimeoutSanitized(t *testing.T) {
	t.Parallel()
	f := newFakeSlack(t)
	f.mu.HandleFunc("/conversations.info", func(w http.ResponseWriter, r *http.Request) {
		// Respond only well after the client's deadline, but never block the
		// test server's shutdown indefinitely.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.api(t).ConversationInfo(ctx, "C01234567")
	var e *Error
	if err == nil || !asError(err, &e) {
		t.Fatalf("want sanitized *Error, got %v", err)
	}
	if e.Code != CodeTimeout && e.Code != CodeUpstreamError {
		t.Errorf("want TIMEOUT/UPSTREAM_ERROR, got %s", e.Code)
	}
	if strings.Contains(e.Error(), f.server.URL) {
		t.Errorf("error leaked server URL: %q", e.Error())
	}
}

// asError is a tiny local errors.As helper to keep intent obvious in tests.
func asError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
