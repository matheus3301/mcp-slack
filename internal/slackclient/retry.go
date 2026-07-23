package slackclient

import (
	"context"
	"errors"
	"time"

	"github.com/slack-go/slack"
)

// RetryPolicy bounds how the client reacts to Slack HTTP 429 responses. It is
// deliberately conservative: a small, fixed number of retries and a hard cap on
// how long we are willing to wait, so the server never sleeps unboundedly or
// retries forever.
type RetryPolicy struct {
	// MaxRetries is the number of additional attempts after the first failure.
	MaxRetries int
	// MaxRetryAfter is the longest Retry-After delay we will honor. If Slack
	// asks for longer, we give up immediately with a RATE_LIMITED error rather
	// than block a request for minutes.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy is used when no policy is supplied.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:    2,
	MaxRetryAfter: 30 * time.Second,
}

// sleeper waits for d or until ctx is done, returning ctx.Err() if cancelled.
// It is injected so tests can run without real time passing.
type sleeper func(ctx context.Context, d time.Duration) error

// realSleep is the production sleeper.
func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// withRetry runs fn, honoring Slack rate-limit responses within the bounds of
// policy. Non-rate-limit errors are returned immediately. The returned error is
// NOT sanitized here; callers pass it through Sanitize.
func withRetry(ctx context.Context, policy RetryPolicy, sleep sleeper, fn func() error) error {
	if sleep == nil {
		sleep = realSleep
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		var rl *slack.RateLimitedError
		if !errors.As(lastErr, &rl) {
			return lastErr
		}

		// Out of retry budget, or Slack wants us to wait longer than we allow.
		if attempt >= policy.MaxRetries || rl.RetryAfter <= 0 || rl.RetryAfter > policy.MaxRetryAfter {
			return lastErr
		}

		if err := sleep(ctx, rl.RetryAfter); err != nil {
			return err
		}
	}
}
