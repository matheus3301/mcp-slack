package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ServerName = "mcp-slack"

func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	openWorld := true
	destructive := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &destructive, OpenWorldHint: &openWorld}
}

// New registers only the explicitly configured subset of the static catalog.
// A zero-value policy registers no tools.
func New(t *Tools, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, nil)
	addTool(s, t, ToolChannelsList, "List metadata for readable member channels; never enumerate the workspace.", true, t.ChannelsList)
	addTool(s, t, ToolHistory, "Read a bounded page of authorized channel or opted-in DM history.", true, t.History)
	addTool(s, t, ToolReplies, "Read a bounded page of replies in an authorized conversation.", true, t.Replies)
	addTool(s, t, "slack_auth_test", "Verify bot/workspace identity and report the configured catalog; no credentials.", true, t.authTest)
	addTool(s, t, "slack_conversations_info", "Read metadata for one authorized conversation.", true, t.info)
	addTool(s, t, "slack_conversations_members", "Read a bounded page of members of an authorized conversation.", true, t.members)
	addTool(s, t, "slack_chat_get_permalink", "Get the permalink of a message in an authorized conversation.", true, t.permalink)
	addTool(s, t, "slack_users_info", "Read a minimal user profile, without email, after membership or DM-recipient authorization.", true, t.userInfo)
	addTool(s, t, "slack_messages_search", "Search a bounded number of authorized history pages by literal substring. Always partial; excludes thread-only replies.", true, t.search)
	addTool(s, t, "slack_chat_post_message", "Post plain text or reply to a root thread in a writable conversation. Requires a durable operation key; uncertain sends are not repeated.", false, t.post)
	addTool(s, t, "slack_chat_update", "Update only a message authored by this bot in a writable conversation.", false, t.update)
	addTool(s, t, "slack_chat_delete", "Delete only a message authored by this bot in a writable conversation.", false, t.deleteMessage)
	addTool(s, t, "slack_chat_schedule_message", "Schedule plain text in a writable conversation, with a durable operation key.", false, t.schedule)
	addTool(s, t, "slack_chat_scheduled_messages_list", "List this token's scheduled messages in one writable conversation, never globally.", true, t.scheduled)
	addTool(s, t, "slack_chat_delete_scheduled_message", "Cancel a scheduled message after verifying it in this token's channel-filtered list.", false, t.cancelScheduled)
	addTool(s, t, "slack_reactions_get", "Read reactions on a verified message in a readable conversation.", true, t.reactions)
	addTool(s, t, "slack_reactions_add", "Add the bot's reaction to a verified message in a writable conversation.", false, func(ctx context.Context, in ReactionInput) (OKOutput, error) { return t.reaction(ctx, in, true) })
	addTool(s, t, "slack_reactions_remove", "Remove the bot's reaction from a verified message in a writable conversation.", false, func(ctx context.Context, in ReactionInput) (OKOutput, error) { return t.reaction(ctx, in, false) })
	addTool(s, t, "slack_pins_list", "List up to 100 pinned messages from a readable conversation.", true, t.pins)
	addTool(s, t, "slack_pins_add", "Pin a verified message in a writable conversation.", false, func(ctx context.Context, in MessageInput) (OKOutput, error) { return t.pin(ctx, in, true) })
	addTool(s, t, "slack_pins_remove", "Unpin a verified message in a writable conversation.", false, func(ctx context.Context, in MessageInput) (OKOutput, error) { return t.pin(ctx, in, false) })
	addTool(s, t, "slack_bookmarks_list", "List channel bookmarks in a readable conversation.", true, func(ctx context.Context, in ChannelInput) (BookmarksOutput, error) {
		return t.listBookmarks(ctx, in.ChannelID, false)
	})
	addTool(s, t, "slack_bookmarks_add", "Add an HTTP(S) link bookmark in a writable conversation.", false, t.addBookmark)
	addTool(s, t, "slack_bookmarks_edit", "Edit a bookmark verified in the writable conversation.", false, t.editBookmark)
	addTool(s, t, "slack_bookmarks_remove", "Remove a bookmark verified in the writable conversation.", false, t.removeBookmark)
	addTool(s, t, "slack_files_info", "Read minimal file metadata only when attached to a verified message in a readable conversation.", true, t.fileInfo)
	addTool(s, t, "slack_files_download", "Download at most 1 MiB as base64 from Slack, after verifying its message association. No caller URL.", true, t.download)
	addTool(s, t, "slack_files_upload", "Upload at most 1 MiB from base64 or the operator artifact root, sharing only to one writable conversation; requires operation key.", false, t.upload)
	addTool(s, t, "slack_dms_open", "Open a bot DM/MPIM only for explicitly allowed recipients and enabled DM/MPIM write permission.", false, t.openDM)
	return s
}
