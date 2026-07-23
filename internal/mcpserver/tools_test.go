package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/matheus3301/mcp-slack/internal/config"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
)

// allowlist builds a config.Allowlist from IDs via the public config loader.
func allowlist(t *testing.T, ids ...string) config.Allowlist {
	t.Helper()
	joined := ""
	for i, id := range ids {
		if i > 0 {
			joined += ","
		}
		joined += id
	}
	env := map[string]string{
		config.EnvBotToken:        "xoxb-1234567890-abcdefghijklmnop",
		config.EnvAllowedChannels: joined,
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("building allowlist: %v", err)
	}
	return cfg.Allowlist()
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *slackclient.Error
	if !errors.As(err, &e) {
		t.Fatalf("error is not a sanitized *Error: %v", err)
	}
	return e.Code
}

func TestChannelsList_DefaultsToAllAllowlisted(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", Name: "a"}
	api.infoByID["G0ABCDEFG"] = &slackclient.ChannelMeta{ID: "G0ABCDEFG", Name: "b", IsPrivate: true}
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567", "G0ABCDEFG")}

	out, err := tools.ChannelsList(context.Background(), ChannelsListInput{})
	if err != nil {
		t.Fatalf("ChannelsList: %v", err)
	}
	if len(out.Channels) != 2 {
		t.Fatalf("want 2 channels, got %d", len(out.Channels))
	}
	if len(api.infoCalls) != 2 {
		t.Errorf("expected one info call per channel, got %v", api.infoCalls)
	}
}

func TestChannelsList_SubsetFilter(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567"}
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567", "G0ABCDEFG")}

	out, err := tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"C01234567"}})
	if err != nil {
		t.Fatalf("ChannelsList: %v", err)
	}
	if len(out.Channels) != 1 || out.Channels[0].ID != "C01234567" {
		t.Errorf("subset filter wrong: %+v", out.Channels)
	}
	if len(api.infoCalls) != 1 {
		t.Errorf("must only fetch requested channel, got %v", api.infoCalls)
	}
}

func TestChannelsList_DeniesNonAllowlisted(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	_, err := tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"C99999999"}})
	if got := codeOf(t, err); got != slackclient.CodePermissionDenied {
		t.Errorf("code = %s, want PERMISSION_DENIED", got)
	}
	if len(api.infoCalls) != 0 {
		t.Errorf("must not call Slack for a denied channel, got %v", api.infoCalls)
	}
}

func TestChannelsList_RejectsMalformedID(t *testing.T) {
	t.Parallel()
	tools := &Tools{API: newFakeAPI(), Allow: allowlist(t, "C01234567")}
	_, err := tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"not-an-id"}})
	if got := codeOf(t, err); got != slackclient.CodeInvalidRequest {
		t.Errorf("code = %s, want INVALID_REQUEST", got)
	}
}

func TestChannelsList_RejectsDMDefense(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	// Even if an IM somehow slipped past the allowlist, ChannelsList refuses it.
	api.infoByID["G0ABCDEFG"] = &slackclient.ChannelMeta{ID: "G0ABCDEFG", IsMpIM: true}
	tools := &Tools{API: api, Allow: allowlist(t, "G0ABCDEFG")}
	_, err := tools.ChannelsList(context.Background(), ChannelsListInput{})
	if got := codeOf(t, err); got != slackclient.CodePermissionDenied {
		t.Errorf("code = %s, want PERMISSION_DENIED for mpim", got)
	}
}

func TestHistory_HappyPathAndDefaults(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.historyByID["C01234567"] = &slackclient.Page{
		Messages:   []slackclient.Message{{TS: "1.000001", Text: "hi"}},
		HasMore:    true,
		NextCursor: "NEXT",
	}
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	page, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if page.NextCursor != "NEXT" || !page.HasMore || len(page.Messages) != 1 {
		t.Errorf("unexpected page: %+v", page)
	}
	// Default limit must be applied.
	if got := api.historyCalls[0].Limit; got != 20 {
		t.Errorf("default limit = %d, want 20", got)
	}
}

func TestHistory_DeniesNonAllowlisted(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}
	_, err := tools.History(context.Background(), HistoryInput{ChannelID: "G0ABCDEFG"})
	if got := codeOf(t, err); got != slackclient.CodePermissionDenied {
		t.Errorf("code = %s, want PERMISSION_DENIED", got)
	}
	if len(api.historyCalls) != 0 {
		t.Errorf("must not call Slack for denied channel")
	}
}

func TestHistory_ValidatesInputs(t *testing.T) {
	t.Parallel()
	tools := &Tools{API: newFakeAPI(), Allow: allowlist(t, "C01234567")}
	cases := []HistoryInput{
		{ChannelID: "C01234567", Limit: 101},
		{ChannelID: "C01234567", Limit: -1},
		{ChannelID: "C01234567", Cursor: "bad cursor!"},
		{ChannelID: "C01234567", Oldest: "not-a-ts"},
		{ChannelID: "C01234567", Latest: "123.45"},
		{ChannelID: "badid"},
	}
	for i, in := range cases {
		if _, err := tools.History(context.Background(), in); err == nil {
			t.Errorf("case %d: expected error for %+v", i, in)
		} else if got := codeOf(t, err); got != slackclient.CodeInvalidRequest {
			t.Errorf("case %d: code = %s, want INVALID_REQUEST", i, got)
		}
	}
}

func TestReplies_HappyPath(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.repliesByID["C01234567"] = &slackclient.Page{Messages: []slackclient.Message{{TS: "1.000001"}}}
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	page, err := tools.Replies(context.Background(), RepliesInput{ChannelID: "C01234567", ThreadTS: "1699999999.000100", Limit: 5})
	if err != nil {
		t.Fatalf("Replies: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Errorf("want 1 message, got %d", len(page.Messages))
	}
	if api.repliesCalls[0].ThreadTS != "1699999999.000100" || api.repliesCalls[0].Limit != 5 {
		t.Errorf("params not forwarded: %+v", api.repliesCalls[0])
	}
}

func TestReplies_RequiresThreadTS(t *testing.T) {
	t.Parallel()
	tools := &Tools{API: newFakeAPI(), Allow: allowlist(t, "C01234567")}
	_, err := tools.Replies(context.Background(), RepliesInput{ChannelID: "C01234567"})
	if got := codeOf(t, err); got != slackclient.CodeInvalidRequest {
		t.Errorf("code = %s, want INVALID_REQUEST for missing thread_ts", got)
	}
}

func TestReplies_DeniesNonAllowlisted(t *testing.T) {
	t.Parallel()
	tools := &Tools{API: newFakeAPI(), Allow: allowlist(t, "C01234567")}
	_, err := tools.Replies(context.Background(), RepliesInput{ChannelID: "G0ABCDEFG", ThreadTS: "1699999999.000100"})
	if got := codeOf(t, err); got != slackclient.CodePermissionDenied {
		t.Errorf("code = %s, want PERMISSION_DENIED", got)
	}
}

func TestHandlers_PropagateSanitizedSlackErrors(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.err = slackclient.NewError(slackclient.CodeNotInChannel, "nope")
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	if _, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"}); codeOf(t, err) != slackclient.CodeNotInChannel {
		t.Errorf("history did not propagate NOT_IN_CHANNEL")
	}
}
