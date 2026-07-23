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
// return. When empty, metadata for every allowlisted channel is returned. IDs
// outside the allowlist are rejected; the workspace is never enumerated.
type ChannelsListInput struct {
	ChannelIDs []string `json:"channel_ids,omitempty" jsonschema:"optional subset of allowlisted channel IDs to fetch; when omitted, all allowlisted channels are returned"`
}

// ChannelsListOutput is the metadata response.
type ChannelsListOutput struct {
	Channels []slackclient.ChannelMeta `json:"channels"`
}

// ChannelsList returns metadata for allowlisted channels, fetching each by ID.
func (t *Tools) ChannelsList(ctx context.Context, in ChannelsListInput) (ChannelsListOutput, error) {
	var targets []string
	if len(in.ChannelIDs) > 0 {
		if len(in.ChannelIDs) > t.Allow.Len() {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeInvalidRequest, "too many channel IDs requested")
		}
		seen := make(map[string]struct{}, len(in.ChannelIDs))
		for _, id := range in.ChannelIDs {
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
			targets = append(targets, id)
		}
	} else {
		targets = t.Allow.IDs()
	}

	out := ChannelsListOutput{Channels: make([]slackclient.ChannelMeta, 0, len(targets))}
	for _, id := range targets {
		meta, err := t.API.ConversationInfo(ctx, id)
		if err != nil {
			// Already sanitized by the slackclient layer.
			return ChannelsListOutput{}, err
		}
		if meta == nil {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodeUpstreamError, "empty channel metadata")
		}
		// Defense in depth: never surface a DM/MPIM even if one were somehow
		// allowlisted.
		if meta.IsIM || meta.IsMpIM {
			return ChannelsListOutput{}, slackclient.NewError(slackclient.CodePermissionDenied, "direct and multi-party messages are not permitted")
		}
		out.Channels = append(out.Channels, *meta)
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

// requireAllowed enforces both format and allowlist membership for a channel.
func (t *Tools) requireAllowed(channelID string) error {
	if err := validate.ChannelID(channelID); err != nil {
		return slackclient.NewError(slackclient.CodeInvalidRequest, err.Error())
	}
	if !t.Allow.Allowed(channelID) {
		return slackclient.NewError(slackclient.CodePermissionDenied, "channel is not in the read allowlist")
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
