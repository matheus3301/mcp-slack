package mcpserver

import (
	"context"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerName is the MCP implementation name advertised to clients.
const ServerName = "mcp-slack"

// readOnlyAnnotations describes a tool that only reads and can be re-run
// safely. openWorld is true because results depend on live Slack state.
func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	openWorld := true
	notDestructive := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: &notDestructive,
		OpenWorldHint:   &openWorld,
	}
}

// New builds an MCP server with the three read-only Slack tools registered.
// The typed AddTool helper auto-generates and validates input/output schemas
// from the Go structs, so malformed arguments are rejected before a handler
// runs.
func New(tools *Tools, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolChannelsList,
		Description: "List metadata (name, privacy, membership, topic, purpose) for readable Slack channels: the allowlisted ones, or in wildcard mode the channels the bot belongs to. Never enumerates the workspace, never returns a non-member channel, and returns no message content.",
		Annotations: readOnlyAnnotations("List readable Slack channels"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ChannelsListInput) (*mcp.CallToolResult, ChannelsListOutput, error) {
		out, err := tools.ChannelsList(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolHistory,
		Description: "Read a bounded page of recent messages from one allowlisted Slack channel. Supports cursor pagination and optional Slack ts bounds.",
		Annotations: readOnlyAnnotations("Read Slack channel history"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, slackclient.Page, error) {
		out, err := tools.History(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolReplies,
		Description: "Read a bounded page of replies in one thread of an allowlisted Slack channel. Supports cursor pagination and optional Slack ts bounds.",
		Annotations: readOnlyAnnotations("Read Slack thread replies"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in RepliesInput) (*mcp.CallToolResult, slackclient.Page, error) {
		out, err := tools.Replies(ctx, in)
		return nil, out, err
	})

	return server
}
