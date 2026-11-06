package slackclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/slack-go/slack"
)

// ScheduledCanceler is a single typed Slack operation, not a method proxy.
// slack-go v0.27.0 unconditionally sends legacy as_user=false; modern bot
// installations can reject that field. Keep the wire workaround here until
// the SDK permits omission, retaining the same transport and retry boundaries.
type ScheduledCanceler interface {
	CancelScheduled(context.Context, string, string) error
}
type scheduledCanceler struct {
	token    string
	endpoint string
	client   *http.Client
}

func NewScheduledCanceler(token string, opts ...Option) ScheduledCanceler {
	o := options{httpClient: &http.Client{Timeout: 30 * time.Second}, apiURL: "https://slack.com/api/"}
	for _, fn := range opts {
		fn(&o)
	}
	return &scheduledCanceler{token: token, endpoint: o.apiURL + "chat.deleteScheduledMessage", client: collaborationHTTPClient(o.httpClient)}
}
func (c *scheduledCanceler) CancelScheduled(ctx context.Context, channel, id string) error {
	// Only these two fields are serialized. Identity always comes from the bot
	// token in the Authorization header, never a caller-supplied identity field.
	data, err := json.Marshal(struct {
		Channel string `json:"channel"`
		ID      string `json:"scheduled_message_id"`
	}{channel, id})
	if err != nil {
		return coded(CodeInvalidRequest)
	}
	return Sanitize(Call(ctx, func() error {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, e := c.client.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			seconds, e := strconv.Atoi(resp.Header.Get("Retry-After"))
			if e != nil || seconds <= 0 || seconds > 30 {
				return coded(CodeRateLimited)
			}
			return &slack.RateLimitedError{RetryAfter: time.Duration(seconds) * time.Second}
		}
		if resp.StatusCode != http.StatusOK {
			return coded(CodeUpstreamError)
		}
		var result slack.SlackResponse
		if e = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); e != nil {
			return e
		}
		if e = result.Err(); e != nil {
			return e
		}
		if !result.Ok {
			return coded(CodeUpstreamError)
		}
		return nil
	}))
}
