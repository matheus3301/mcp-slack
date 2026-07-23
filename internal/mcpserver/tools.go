// Package mcpserver wires the three read-only Slack tools onto an MCP server.
//
// The entire tool surface is fixed at compile time: exactly three tools, all
// annotated read-only and non-destructive. There is no dynamic method proxy and
// no path by which a caller can reach an un-enumerated Slack API method.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/matheus3301/mcp-slack/internal/config"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/matheus3301/mcp-slack/internal/validate"
)

// Tool names. These are the only tool identifiers the server exposes and are
// asserted by the regression test that forbids write/search tooling.
const (
	ToolChannelsList = "slack_channels_list"
	ToolHistory      = "slack_conversations_history"
	ToolReplies      = "slack_conversations_replies"
)

// Tools holds the dependencies shared by the tool handlers. It is safe for
// concurrent use: API implementations are expected to be concurrency-safe and
// the allowlist is immutable.
type Tools struct {
	API   slackclient.API
	Allow config.Allowlist
}

// ---- slack_channels_list ----

// ChannelsListInput optionally narrows the set of allowlisted channels to
// return. When empty in explicit mode, metadata for every allowlisted channel
// is returned. When empty in wildcard mode, the bot's member channels are
// listed via a member-scoped Slack API, paginated by limit/cursor. The
// workspace is never enumerated, and a non-member channel is never returned.
type ChannelsListInput struct {
	ChannelIDs []string `json:"channel_ids,omitempty" jsonschema:"optional channel IDs to fetch; when omitted, allowlisted (or, in wildcard mode, member) channels are returned"`
	Limit      int      `json:"limit,omitempty" jsonschema:"wildcard listing only: max channels per page, 1-100 (default 20)"`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"wildcard listing only: pagination cursor from a previous response's next_cursor"`
}

// ChannelsListOutput is the metadata response. next_cursor is set only when
// paginating a wildcard listing.
type ChannelsListOutput struct {
	Channels   []slackclient.ChannelMeta `json:"channels"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

// ChannelsList returns metadata for channels the caller may read. It never
// returns a channel the bot is not a member of, and never a DM or MPIM.
func (t *Tools) ChannelsList(ctx context.Context, in ChannelsListInput) (ChannelsListOutput, error) {
	if len(in.ChannelIDs) > 0 {
		return t.channelsByID(ctx, in.ChannelIDs)
	}
	if t.Allow.Wildcard() {
		return t.channelsForMember(ctx, in.Limit, in.Cursor)
	}
	return t.channelsByID(ctx, t.Allow.IDs())
}

// channelsByID fetches each requested channel by ID, enforcing the allowlist
// policy, membership, and the DM/MPIM ban. A requested channel the bot has not
// joined is a hard error, so the caller gets a clear signal.
func (t *Tools) channelsByID(ctx context.Context, ids []string) (ChannelsListOutput, error) {
	seen := make(map[string]struct{}, len(ids))
	out := ChannelsListOutput{Channels: make([]slackclient.ChannelMeta, 0, len(ids))}
	for _, id := range ids {
		if err := validate.ChannelID(id); err != nil {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
		}
		if !t.Allow.Allowed(id) {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodePermissionDenied, "channel is not in the read allowlist")
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		meta, err := t.API.ConversationInfo(ctx, id)
		if err != nil {
			return ChannelsListOutput{}, err // already sanitized
		}
		if meta == nil {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeUpstreamError, "empty channel metadata")
		}
		if meta.IsIM || meta.IsMpIM {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodePermissionDenied, "direct and multi-party messages are not permitted")
		}
		if !meta.IsMember {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeNotInChannel, "the bot is not a member of the requested channel")
		}
		out.Channels = append(out.Channels, *meta)
	}
	return out, nil
}

// channelsForMember lists the bot's member channels via the member-scoped
// Slack API. Every returned channel is one the bot belongs to.
func (t *Tools) channelsForMember(ctx context.Context, limit int, cursor string) (ChannelsListOutput, error) {
	norm, err := validate.Limit(limit)
	if err != nil {
		return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	if err := validate.Cursor(cursor); err != nil {
		return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}

	page, err := t.API.MemberChannels(ctx, slackclient.ListParams{Limit: norm, Cursor: cursor})
	if err != nil {
		return ChannelsListOutput{}, err // already sanitized
	}
	out := ChannelsListOutput{
		Channels:   make([]slackclient.ChannelMeta, 0, len(page.Channels)),
		NextCursor: page.NextCursor,
	}
	for _, c := range page.Channels {
		// Defense in depth: the member API excludes DMs/MPIMs, but never trust
		// that blindly.
		if c.IsIM || c.IsMpIM || !c.IsMember {
			continue
		}
		out.Channels = append(out.Channels, c)
	}
	return out, nil
}

// ---- slack_conversations_history ----

// HistoryInput is the bounded history request for a single allowlisted channel.
type HistoryInput struct {
	ChannelID string `json:"channel_id" jsonschema:"allowlisted Slack channel ID (C... or G...)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max messages to return, 1-100 (default 20)"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"pagination cursor from a previous response's next_cursor"`
	Oldest    string `json:"oldest,omitempty" jsonschema:"only include messages at or after this Slack ts (e.g. 1699999999.123456)"`
	Latest    string `json:"latest,omitempty" jsonschema:"only include messages up to this Slack ts"`
	Inclusive bool   `json:"inclusive,omitempty" jsonschema:"include messages with ts exactly equal to oldest or latest"`
}

// History returns bounded message history for one allowlisted channel.
func (t *Tools) History(ctx context.Context, in HistoryInput) (slackclient.Page, error) {
	if err := t.requireAllowed(in.ChannelID); err != nil {
		return slackclient.Page{}, err
	}
	limit, err := t.validateWindow(in.Limit, in.Cursor, in.Oldest, in.Latest)
	if err != nil {
		return slackclient.Page{}, err
	}
	if err := t.requireMembership(ctx, in.ChannelID); err != nil {
		return slackclient.Page{}, err
	}

	page, err := t.API.ConversationHistory(ctx, slackclient.HistoryParams{
		ChannelID: in.ChannelID,
		Limit:     limit,
		Cursor:    in.Cursor,
		Oldest:    in.Oldest,
		Latest:    in.Latest,
		Inclusive: in.Inclusive,
	})
	if err != nil {
		return slackclient.Page{}, err
	}
	return *page, nil
}

// ---- slack_conversations_replies ----

// RepliesInput is the bounded thread-replies request.
type RepliesInput struct {
	ChannelID string `json:"channel_id" jsonschema:"allowlisted Slack channel ID (C... or G...)"`
	ThreadTS  string `json:"thread_ts" jsonschema:"Slack ts of the thread's parent message (e.g. 1699999999.123456)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max messages to return, 1-100 (default 20)"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"pagination cursor from a previous response's next_cursor"`
	Oldest    string `json:"oldest,omitempty" jsonschema:"only include messages at or after this Slack ts"`
	Latest    string `json:"latest,omitempty" jsonschema:"only include messages up to this Slack ts"`
	Inclusive bool   `json:"inclusive,omitempty" jsonschema:"include messages with ts exactly equal to oldest or latest"`
}

// Replies returns bounded replies for one thread in an allowlisted channel.
func (t *Tools) Replies(ctx context.Context, in RepliesInput) (slackclient.Page, error) {
	if err := t.requireAllowed(in.ChannelID); err != nil {
		return slackclient.Page{}, err
	}
	if err := validate.RequiredTimestamp(in.ThreadTS); err != nil {
		return slackclient.Page{}, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	limit, err := t.validateWindow(in.Limit, in.Cursor, in.Oldest, in.Latest)
	if err != nil {
		return slackclient.Page{}, err
	}
	if err := t.requireMembership(ctx, in.ChannelID); err != nil {
		return slackclient.Page{}, err
	}

	page, err := t.API.ConversationReplies(ctx, slackclient.RepliesParams{
		ChannelID: in.ChannelID,
		ThreadTS:  in.ThreadTS,
		Limit:     limit,
		Cursor:    in.Cursor,
		Oldest:    in.Oldest,
		Latest:    in.Latest,
		Inclusive: in.Inclusive,
	})
	if err != nil {
		return slackclient.Page{}, err
	}
	return *page, nil
}

// ---- shared validation ----

// requireAllowed enforces both format and allowlist policy for a channel.
func (t *Tools) requireAllowed(channelID string) error {
	if err := validate.ChannelID(channelID); err != nil {
		return slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	if !t.Allow.Allowed(channelID) {
		return slackclient.NewError(slackclient.CodePermissionDenied, "channel is not in the read allowlist")
	}
	return nil
}

// requireMembership verifies, in wildcard mode, that the bot belongs to the
// channel before any content is read. It also rejects DMs and MPIMs. In
// explicit mode it is a no-op: the operator curated the allowlist, and Slack
// still returns NOT_IN_CHANNEL (sanitized) if the bot is not a member.
func (t *Tools) requireMembership(ctx context.Context, channelID string) error {
	if !t.Allow.Wildcard() {
		return nil
	}
	meta, err := t.API.ConversationInfo(ctx, channelID)
	if err != nil {
		return err // already sanitized (e.g. CHANNEL_NOT_FOUND for private channels)
	}
	if meta == nil {
		return slackclient.NewError(slackclient.CodeUpstreamError, "empty channel metadata")
	}
	if meta.IsIM || meta.IsMpIM {
		return slackclient.NewError(slackclient.CodePermissionDenied, "direct and multi-party messages are not permitted")
	}
	if !meta.IsMember {
		return slackclient.NewError(slackclient.CodeNotInChannel, "the bot is not a member of the requested channel")
	}
	return nil
}

// validateWindow validates and normalizes the common paging window arguments.
func (t *Tools) validateWindow(limit int, cursor, oldest, latest string) (int, error) {
	norm, err := validate.Limit(limit)
	if err != nil {
		return 0, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	if err := validate.Cursor(cursor); err != nil {
		return 0, slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	if err := validate.Timestamp(oldest); err != nil {
		return 0, slackclient.NewError(slackclient.CodeInvalidRequest, fmt.Sprintf("oldest: %s", err.Error()))
	}
	if err := validate.Timestamp(latest); err != nil {
		return 0, slackclient.NewError(slackclient.CodeInvalidRequest, fmt.Sprintf("latest: %s", err.Error()))
	}
	return norm, nil
}
