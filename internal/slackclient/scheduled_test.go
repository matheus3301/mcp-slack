package slackclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScheduledCancelOmitsLegacyAsUser(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != "POST" || r.URL.Path != "/chat.deleteScheduledMessage" {
			t.Error("unexpected endpoint")
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Error("expected JSON")
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-synthetic-test-token" {
			t.Error("missing bot authorization")
		}
		var got map[string]string
		if e := json.NewDecoder(r.Body).Decode(&got); e != nil {
			t.Error(e)
		}
		if len(got) != 2 || got["channel"] != "C01234567" || got["scheduled_message_id"] != "Q01234567" {
			t.Errorf("unexpected request keys: %v", got)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	api := NewScheduledCanceler("xoxb-synthetic-test-token", WithAPIURL(server.URL+"/"))
	if e := api.CancelScheduled(context.Background(), "C01234567", "Q01234567"); e != nil {
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatal("unexpected retry")
	}
}
func TestScheduledCancelErrorsAndNoAmbiguousRetry(t *testing.T) {
	for _, body := range []string{`{"ok":false,"error":"invalid_arguments"}`, `{"ok":false,"error":"raw-private-data"}`, `broken secret`, `{}`} {
		t.Run(body, func(t *testing.T) {
			var count atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { count.Add(1); _, _ = io.WriteString(w, body) }))
			defer s.Close()
			e := NewScheduledCanceler("xoxb-synthetic-test-token", WithAPIURL(s.URL+"/")).CancelScheduled(context.Background(), "C01234567", "Q01234567")
			if e == nil || strings.Contains(e.Error(), "private") || strings.Contains(e.Error(), "secret") {
				t.Fatalf("unsafe error %v", e)
			}
			if count.Load() != 1 {
				t.Fatal("retried ambiguous error")
			}
		})
	}
}
func TestCollaborationTransportRefusesRedirects(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached.Add(1); _, _ = io.WriteString(w, `{"ok":true}`) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	e := NewScheduledCanceler("xoxb-synthetic-test-token", WithAPIURL(redirect.URL+"/")).CancelScheduled(context.Background(), "C01234567", "Q01234567")
	if e == nil || reached.Load() != 0 {
		t.Fatal("followed redirect with bot credentials")
	}
	if _, e = NewCollaboration("xoxb-synthetic-test-token", WithAPIURL(redirect.URL+"/")).AuthTestContext(context.Background()); e == nil || reached.Load() != 0 {
		t.Fatal("SDK followed redirect")
	}
}
