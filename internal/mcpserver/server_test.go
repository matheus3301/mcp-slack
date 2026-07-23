package mcpserver

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect spins up the server over an in-memory transport and returns a
// connected client session. This exercises the real MCP initialize/list/call
// protocol path end to end, with no stdio pipes or credentials.
func connect(t *testing.T, tools *Tools) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := New(tools, "test")

	go func() {
		// Run returns when the client disconnects; errors here are not fatal
		// to the test and the goroutine simply exits.
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func testTools(t *testing.T) *Tools {
	t.Helper()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", Name: "general", IsPrivate: true, IsMember: true}
	api.historyByID["C01234567"] = &slackclient.Page{
		Messages:   []slackclient.Message{{TS: "1699999999.000100", User: "U1", Text: "hello"}},
		NextCursor: "CURSOR_1",
		HasMore:    true,
	}
	api.repliesByID["C01234567"] = &slackclient.Page{
		Messages: []slackclient.Message{{TS: "1699999999.000100", ThreadTS: "1699999999.000100", Text: "root"}},
	}
	return &Tools{API: api, Allow: allowlist(t, "C01234567")}
}

func TestSmoke_ListToolsExactNamesAndAnnotations(t *testing.T) {
	t.Parallel()
	session := connect(t, testTools(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make([]string, 0, len(res.Tools))
	byName := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		got = append(got, tl.Name)
		byName[tl.Name] = tl
	}
	sort.Strings(got)

	want := []string{ToolChannelsList, ToolHistory, ToolReplies}
	sort.Strings(want)
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 tools, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool names = %v, want %v", got, want)
		}
	}

	// Every tool must be annotated read-only, idempotent, and non-destructive.
	for name, tl := range byName {
		if tl.Annotations == nil {
			t.Fatalf("tool %s missing annotations", name)
		}
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("tool %s not marked ReadOnlyHint", name)
		}
		if !tl.Annotations.IdempotentHint {
			t.Errorf("tool %s not marked IdempotentHint", name)
		}
		if tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint {
			t.Errorf("tool %s must be non-destructive", name)
		}
		if tl.InputSchema == nil {
			t.Errorf("tool %s missing input schema", name)
		}
	}
}

func TestSmoke_CallChannelsList(t *testing.T) {
	t.Parallel()
	session := connect(t, testTools(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolChannelsList,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	var out ChannelsListOutput
	decodeStructured(t, res.StructuredContent, &out)
	if len(out.Channels) != 1 || out.Channels[0].ID != "C01234567" {
		t.Errorf("unexpected channels: %+v", out.Channels)
	}
}

func TestSmoke_CallHistoryAndReplies(t *testing.T) {
	t.Parallel()
	session := connect(t, testTools(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hres, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolHistory,
		Arguments: map[string]any{"channel_id": "C01234567", "limit": 10},
	})
	if err != nil || hres.IsError {
		t.Fatalf("history call failed: err=%v result=%+v", err, hres)
	}
	var page slackclient.Page
	decodeStructured(t, hres.StructuredContent, &page)
	if page.NextCursor != "CURSOR_1" || len(page.Messages) != 1 {
		t.Errorf("unexpected history page: %+v", page)
	}

	rres, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolReplies,
		Arguments: map[string]any{"channel_id": "C01234567", "thread_ts": "1699999999.000100"},
	})
	if err != nil || rres.IsError {
		t.Fatalf("replies call failed: err=%v result=%+v", err, rres)
	}
}

func TestSmoke_CallDeniedChannelIsToolError(t *testing.T) {
	t.Parallel()
	session := connect(t, testTools(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolHistory,
		Arguments: map[string]any{"channel_id": "G0ABCDEFG"},
	})
	if err != nil {
		t.Fatalf("transport error (should be a tool error instead): %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for a non-allowlisted channel")
	}
	if text := firstText(res.Content); text == "" ||
		!strings.Contains(text, slackclient.CodePermissionDenied) {
		t.Errorf("expected PERMISSION_DENIED in error content, got %q", text)
	}
}

func TestSmoke_WildcardListAndMembershipGate(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.memberPages = []*slackclient.ChannelPage{{
		Channels: []slackclient.ChannelMeta{{ID: "C01234567", Name: "general", IsMember: true}},
	}}
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: true}
	api.infoByID["C09999999"] = &slackclient.ChannelMeta{ID: "C09999999", IsMember: false}
	api.historyByID["C01234567"] = &slackclient.Page{Messages: []slackclient.Message{{TS: "1.0"}}}

	session := connect(t, &Tools{API: api, Allow: wildcardAllow(t)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Wildcard channels_list returns member channels via the member-scoped API.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolChannelsList, Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("channels_list: err=%v result=%+v", err, res)
	}
	var out ChannelsListOutput
	decodeStructured(t, res.StructuredContent, &out)
	if len(out.Channels) != 1 || out.Channels[0].ID != "C01234567" {
		t.Errorf("wildcard list wrong: %+v", out.Channels)
	}

	// History on a member channel succeeds.
	ok, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolHistory, Arguments: map[string]any{"channel_id": "C01234567"}})
	if err != nil || ok.IsError {
		t.Fatalf("history(member): err=%v result=%+v", err, ok)
	}

	// History on a non-member channel is a tool error, with no content read.
	deny, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolHistory, Arguments: map[string]any{"channel_id": "C09999999"}})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !deny.IsError || !strings.Contains(firstText(deny.Content), slackclient.CodeNotInChannel) {
		t.Errorf("expected NOT_IN_CHANNEL tool error, got %+v", deny.Content)
	}
}

func TestSmoke_UnknownToolRejected(t *testing.T) {
	t.Parallel()
	session := connect(t, testTools(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "slack_post_message"})
	if err == nil {
		t.Fatal("calling an unregistered tool must fail")
	}
}

func decodeStructured(t *testing.T, sc any, target any) {
	t.Helper()
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

func firstText(content []mcp.Content) string {
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
