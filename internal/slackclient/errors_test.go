package slackclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestSanitize_NilAndPassthrough(t *testing.T) {
	t.Parallel()
	if Sanitize(nil) != nil {
		t.Error("Sanitize(nil) must be nil")
	}
	orig := NewError(CodeChannelNotFound, "x")
	if got := Sanitize(orig); got != orig {
		t.Errorf("Sanitize should pass through *Error unchanged")
	}
	// Wrapped *Error is still recovered.
	wrapped := fmt.Errorf("context: %w", orig)
	var e *Error
	if !errors.As(Sanitize(wrapped), &e) || e.Code != CodeChannelNotFound {
		t.Errorf("wrapped *Error not recovered")
	}
}

func TestSanitize_SlackErrorCodes(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"invalid_auth":           CodeAuthFailed,
		"account_inactive":       CodeAuthFailed,
		"missing_scope":          CodePermissionDenied,
		"not_allowed_token_type": CodePermissionDenied,
		"channel_not_found":      CodeChannelNotFound,
		"not_in_channel":         CodeNotInChannel,
		"thread_not_found":       CodeThreadNotFound,
		"ratelimited":            CodeRateLimited,
		"invalid_cursor":         CodeInvalidRequest,
	}
	for slackErr, wantCode := range cases {
		t.Run(slackErr, func(t *testing.T) {
			in := slack.SlackErrorResponse{Err: slackErr}
			got := Sanitize(in)
			var e *Error
			if !errors.As(got, &e) {
				t.Fatalf("not an *Error: %T", got)
			}
			if e.Code != wantCode {
				t.Errorf("Sanitize(%q).Code = %s, want %s", slackErr, e.Code, wantCode)
			}
		})
	}
}

func TestSanitize_UnknownSlackErrorIsGeneric(t *testing.T) {
	t.Parallel()
	// An undocumented Slack error string must NOT leak verbatim.
	in := slack.SlackErrorResponse{Err: "some_new_internal_error_with_secret"}
	got := Sanitize(in)
	var e *Error
	if !errors.As(got, &e) || e.Code != CodeUpstreamError {
		t.Fatalf("want UPSTREAM_ERROR, got %v", got)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Errorf("sanitized error leaked upstream string: %q", e.Error())
	}
}

func TestSanitize_TransportErrorsAreOpaque(t *testing.T) {
	t.Parallel()
	// A raw transport error potentially containing a URL/host must be opaque.
	raw := errors.New("Get \"https://slack.com/api/conversations.history?token=xoxb-leaky\": dial tcp: i/o timeout")
	got := Sanitize(raw)
	var e *Error
	if !errors.As(got, &e) || e.Code != CodeUpstreamError {
		t.Fatalf("want UPSTREAM_ERROR, got %v", got)
	}
	if strings.Contains(e.Error(), "xoxb") || strings.Contains(e.Error(), "slack.com") {
		t.Errorf("sanitized error leaked transport detail: %q", e.Error())
	}
}

func TestSanitize_ContextErrors(t *testing.T) {
	t.Parallel()
	for _, err := range []error{context.DeadlineExceeded, context.Canceled} {
		got := Sanitize(err)
		var e *Error
		if !errors.As(got, &e) || e.Code != CodeTimeout {
			t.Errorf("Sanitize(%v) = %v, want TIMEOUT", err, got)
		}
	}
}

func TestSanitize_RateLimited(t *testing.T) {
	t.Parallel()
	got := Sanitize(&slack.RateLimitedError{RetryAfter: 5 * time.Second})
	var e *Error
	if !errors.As(got, &e) || e.Code != CodeRateLimited {
		t.Errorf("want RATE_LIMITED, got %v", got)
	}
}
