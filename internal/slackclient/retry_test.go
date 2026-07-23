package slackclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// recordingSleeper records requested delays instead of sleeping.
type recordingSleeper struct {
	delays []time.Duration
	err    error // if set, returned on every call (simulates ctx cancel)
}

func (r *recordingSleeper) sleep(_ context.Context, d time.Duration) error {
	r.delays = append(r.delays, d)
	return r.err
}

func TestWithRetry_SucceedsFirstTry(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{}
	calls := 0
	err := withRetry(context.Background(), DefaultRetryPolicy, rs.sleep, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 || len(rs.delays) != 0 {
		t.Fatalf("calls=%d delays=%v err=%v", calls, rs.delays, err)
	}
}

func TestWithRetry_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{}
	calls := 0
	policy := RetryPolicy{MaxRetries: 3, MaxRetryAfter: time.Minute}
	err := withRetry(context.Background(), policy, rs.sleep, func() error {
		calls++
		if calls < 3 {
			return &slack.RateLimitedError{RetryAfter: 2 * time.Second}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
	if len(rs.delays) != 2 {
		t.Errorf("slept %d times, want 2", len(rs.delays))
	}
}

func TestWithRetry_ExhaustsBudget(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{}
	calls := 0
	policy := RetryPolicy{MaxRetries: 2, MaxRetryAfter: time.Minute}
	err := withRetry(context.Background(), policy, rs.sleep, func() error {
		calls++
		return &slack.RateLimitedError{RetryAfter: time.Second}
	})
	var rl *slack.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
	if calls != 3 { // initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
	if len(rs.delays) != 2 {
		t.Errorf("slept %d times, want 2", len(rs.delays))
	}
}

func TestWithRetry_RefusesLongRetryAfter(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{}
	calls := 0
	policy := RetryPolicy{MaxRetries: 5, MaxRetryAfter: 10 * time.Second}
	err := withRetry(context.Background(), policy, rs.sleep, func() error {
		calls++
		return &slack.RateLimitedError{RetryAfter: time.Hour} // absurd
	})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (must not sleep for an hour)", calls)
	}
	if len(rs.delays) != 0 {
		t.Errorf("must not have slept; delays=%v", rs.delays)
	}
}

func TestWithRetry_NonRateLimitReturnsImmediately(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{}
	calls := 0
	sentinel := errors.New("boom")
	err := withRetry(context.Background(), DefaultRetryPolicy, rs.sleep, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) || calls != 1 || len(rs.delays) != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestWithRetry_RespectsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := withRetry(ctx, DefaultRetryPolicy, (&recordingSleeper{}).sleep, func() error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if calls != 0 {
		t.Errorf("must not call fn with canceled context; calls=%d", calls)
	}
}

func TestWithRetry_StopsWhenSleeperCancelled(t *testing.T) {
	t.Parallel()
	rs := &recordingSleeper{err: context.DeadlineExceeded}
	calls := 0
	err := withRetry(context.Background(), DefaultRetryPolicy, rs.sleep, func() error {
		calls++
		return &slack.RateLimitedError{RetryAfter: time.Second}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
