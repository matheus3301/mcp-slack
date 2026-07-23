// Package slackclient wraps the Slack Web API behind a small, read-only
// interface. Only three conversation-read methods are exposed; there is
// deliberately no way to post, edit, delete, react, search, or call an
// arbitrary Slack method through this package.
//
// The concrete adapter maps Slack's rich response types down to the minimal,
// deterministic structs defined here. Message bodies are passed through to the
// caller but are never logged or persisted by this package.
package slackclient

import "context"

// ChannelMeta is the minimal, JSON-friendly view of a Slack conversation's
// metadata. It intentionally omits membership lists, unread counts, and other
// fields that are noisy or sensitive.
type ChannelMeta struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	IsPrivate  bool   `json:"is_private"`
	IsArchived bool   `json:"is_archived"`
	IsMember   bool   `json:"is_member"`
	IsChannel  bool   `json:"is_channel"`
	IsGroup    bool   `json:"is_group"`
	IsIM       bool   `json:"is_im"`
	IsMpIM     bool   `json:"is_mpim"`
	NumMembers int    `json:"num_members,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Created    int64  `json:"created,omitempty"`
}

// Message is the minimal, JSON-friendly view of a Slack message.
type Message struct {
	Type       string `json:"type,omitempty"`
	SubType    string `json:"subtype,omitempty"`
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts,omitempty"`
	User       string `json:"user,omitempty"`
	BotID      string `json:"bot_id,omitempty"`
	Username   string `json:"username,omitempty"`
	Text       string `json:"text,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
	EditedTS   string `json:"edited_ts,omitempty"`
}

// Page is the shared shape returned by history and replies reads.
type Page struct {
	Messages   []Message `json:"messages"`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// HistoryParams are the validated arguments for a conversations.history read.
type HistoryParams struct {
	ChannelID string
	Limit     int
	Cursor    string
	Oldest    string
	Latest    string
	Inclusive bool
}

// RepliesParams are the validated arguments for a conversations.replies read.
type RepliesParams struct {
	ChannelID string
	ThreadTS  string
	Limit     int
	Cursor    string
	Oldest    string
	Latest    string
	Inclusive bool
}

// API is the read-only Slack surface consumed by the MCP tools. Depending on an
// interface rather than *slack.Client lets the tools be unit-tested against a
// fake with no network and no credentials.
type API interface {
	ConversationInfo(ctx context.Context, channelID string) (*ChannelMeta, error)
	ConversationHistory(ctx context.Context, p HistoryParams) (*Page, error)
	ConversationReplies(ctx context.Context, p RepliesParams) (*Page, error)
}
