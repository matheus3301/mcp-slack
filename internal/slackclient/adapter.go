package slackclient

import (
	"context"
	"net/http"
	"time"

	"github.com/slack-go/slack"
)

// slackAPI is the subset of *slack.Client the adapter uses. Narrowing it keeps
// the surface auditable and makes clear that only read methods are called.
type slackAPI interface {
	GetConversationInfoContext(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error)
	GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
}

// adapter implements API on top of the Slack SDK, adding bounded retries and
// error sanitization and mapping Slack types to our minimal structs.
type adapter struct {
	sc     slackAPI
	policy RetryPolicy
	sleep  sleeper
}

// Option configures the adapter/Slack client.
type Option func(*options)

type options struct {
	apiURL     string
	httpClient *http.Client
	policy     RetryPolicy
	sleep      sleeper
}

// WithAPIURL points the client at an alternate Slack base URL. Used by
// integration tests to target a fake HTTP server; unused in production.
func WithAPIURL(url string) Option { return func(o *options) { o.apiURL = url } }

// WithHTTPClient sets a custom *http.Client (e.g. one wired to a test server).
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.httpClient = c } }

// WithRetryPolicy overrides the default retry policy.
func WithRetryPolicy(p RetryPolicy) Option { return func(o *options) { o.policy = p } }

// withSleeper injects a fake sleeper (test-only helper).
func withSleeper(s sleeper) Option { return func(o *options) { o.sleep = s } }

// New builds an API backed by the real Slack Web API. The token is passed
// straight to the SDK and is never stored elsewhere or logged.
func New(token string, opts ...Option) API {
	o := options{
		policy: DefaultRetryPolicy,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, fn := range opts {
		fn(&o)
	}

	slackOpts := []slack.Option{slack.OptionHTTPClient(o.httpClient)}
	if o.apiURL != "" {
		slackOpts = append(slackOpts, slack.OptionAPIURL(o.apiURL))
	}
	// Note: we do NOT enable slack.OptionRetry; rate-limit handling is owned by
	// withRetry so it stays bounded and testable.

	return &adapter{
		sc:     slack.New(token, slackOpts...),
		policy: o.policy,
		sleep:  o.sleep,
	}
}

func (a *adapter) ConversationInfo(ctx context.Context, channelID string) (*ChannelMeta, error) {
	var ch *slack.Channel
	err := withRetry(ctx, a.policy, a.sleep, func() error {
		var e error
		ch, e = a.sc.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
			ChannelID:         channelID,
			IncludeNumMembers: true,
		})
		return e
	})
	if err != nil {
		return nil, Sanitize(err)
	}
	return mapChannel(ch), nil
}

func (a *adapter) ConversationHistory(ctx context.Context, p HistoryParams) (*Page, error) {
	var resp *slack.GetConversationHistoryResponse
	err := withRetry(ctx, a.policy, a.sleep, func() error {
		var e error
		resp, e = a.sc.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: p.ChannelID,
			Limit:     p.Limit,
			Cursor:    p.Cursor,
			Oldest:    p.Oldest,
			Latest:    p.Latest,
			Inclusive: p.Inclusive,
		})
		return e
	})
	if err != nil {
		return nil, Sanitize(err)
	}
	return &Page{
		Messages:   mapMessages(resp.Messages),
		HasMore:    resp.HasMore,
		NextCursor: resp.ResponseMetaData.NextCursor,
	}, nil
}

func (a *adapter) ConversationReplies(ctx context.Context, p RepliesParams) (*Page, error) {
	var (
		msgs    []slack.Message
		hasMore bool
		cursor  string
	)
	err := withRetry(ctx, a.policy, a.sleep, func() error {
		var e error
		msgs, hasMore, cursor, e = a.sc.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: p.ChannelID,
			Timestamp: p.ThreadTS,
			Limit:     p.Limit,
			Cursor:    p.Cursor,
			Oldest:    p.Oldest,
			Latest:    p.Latest,
			Inclusive: p.Inclusive,
		})
		return e
	})
	if err != nil {
		return nil, Sanitize(err)
	}
	return &Page{
		Messages:   mapMessages(msgs),
		HasMore:    hasMore,
		NextCursor: cursor,
	}, nil
}

// mapChannel projects a Slack channel down to ChannelMeta.
func mapChannel(ch *slack.Channel) *ChannelMeta {
	if ch == nil {
		return nil
	}
	return &ChannelMeta{
		ID:         ch.ID,
		Name:       ch.Name,
		IsPrivate:  ch.IsPrivate,
		IsArchived: ch.IsArchived,
		IsMember:   ch.IsMember,
		IsChannel:  ch.IsChannel,
		IsGroup:    ch.IsGroup,
		IsIM:       ch.IsIM,
		IsMpIM:     ch.IsMpIM,
		NumMembers: ch.NumMembers,
		Topic:      ch.Topic.Value,
		Purpose:    ch.Purpose.Value,
		Created:    int64(ch.Created),
	}
}

// mapMessages projects Slack messages down to our minimal Message type.
func mapMessages(in []slack.Message) []Message {
	out := make([]Message, 0, len(in))
	for i := range in {
		m := in[i].Msg
		msg := Message{
			Type:       m.Type,
			SubType:    m.SubType,
			TS:         m.Timestamp,
			ThreadTS:   m.ThreadTimestamp,
			User:       m.User,
			BotID:      m.BotID,
			Username:   m.Username,
			Text:       m.Text,
			ReplyCount: m.ReplyCount,
		}
		if m.Edited != nil {
			msg.EditedTS = m.Edited.Timestamp
		}
		out = append(out, msg)
	}
	return out
}
