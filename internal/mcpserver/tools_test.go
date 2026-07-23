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

// wildcardAllow builds a wildcard (member-scoped) allowlist via the loader.
func wildcardAllow(t *testing.T) config.Allowlist {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			config.EnvBotToken:        "xoxb-1234567890-abcdefghijklmnop",
			config.EnvAllowedChannels: "*",
		}[k]
	})
	if err != nil {
		t.Fatalf("building wildcard allowlist: %v", err)
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
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", Name: "a", IsMember: true}
	api.infoByID["G0ABCDEFG"] = &slackclient.ChannelMeta{ID: "G0ABCDEFG", Name: "b", IsPrivate: true, IsMember: true}
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
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: true}
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

// ---- wildcard mode ----

func TestWildcard_HistoryAllowsMemberChannel(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: true}
	api.historyByID["C01234567"] = &slackclient.Page{Messages: []slackclient.Message{{TS: "1.000001"}}}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	page, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Errorf("want 1 message, got %d", len(page.Messages))
	}
	// Membership must be verified before content is read.
	if len(api.infoCalls) != 1 || api.infoCalls[0] != "C01234567" {
		t.Errorf("expected a membership pre-check, got infoCalls=%v", api.infoCalls)
	}
	if len(api.historyCalls) != 1 {
		t.Errorf("expected exactly one history call, got %d", len(api.historyCalls))
	}
}

func TestWildcard_HistoryDeniesNonMember(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: false}
	api.historyByID["C01234567"] = &slackclient.Page{Messages: []slackclient.Message{{TS: "1.0"}}}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	_, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"})
	if got := codeOf(t, err); got != slackclient.CodeNotInChannel {
		t.Fatalf("code = %s, want NOT_IN_CHANNEL", got)
	}
	// Content must NOT be fetched for a non-member channel.
	if len(api.historyCalls) != 0 {
		t.Errorf("must not read history for a non-member channel")
	}
}

func TestWildcard_RepliesDeniesNonMember(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: false}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	_, err := tools.Replies(context.Background(), RepliesInput{ChannelID: "C01234567", ThreadTS: "1699999999.000100"})
	if got := codeOf(t, err); got != slackclient.CodeNotInChannel {
		t.Fatalf("code = %s, want NOT_IN_CHANNEL", got)
	}
	if len(api.repliesCalls) != 0 {
		t.Errorf("must not read replies for a non-member channel")
	}
}

func TestWildcard_RejectsMpIMAndDM(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["G0MPIM0001"] = &slackclient.ChannelMeta{ID: "G0MPIM0001", IsMpIM: true, IsMember: true}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	// MPIM (G-prefixed, passes format) is denied by the runtime membership check.
	if got := codeOf(t, mustErr(tools.History(context.Background(), HistoryInput{ChannelID: "G0MPIM0001"}))); got != slackclient.CodePermissionDenied {
		t.Errorf("mpim code = %s, want PERMISSION_DENIED", got)
	}
	// A DM ID is rejected earlier, by format.
	if got := codeOf(t, mustErr(tools.History(context.Background(), HistoryInput{ChannelID: "D01234567"}))); got != slackclient.CodeInvalidRequest {
		t.Errorf("dm code = %s, want INVALID_REQUEST", got)
	}
}

func TestWildcard_ChannelsListUsesMemberAPIAndPaginates(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.memberPages = []*slackclient.ChannelPage{{
		Channels: []slackclient.ChannelMeta{
			{ID: "C01234567", Name: "general", IsMember: true},
			{ID: "G0ABCDEFG", Name: "private", IsPrivate: true, IsMember: true},
		},
		NextCursor: "PAGE2",
	}}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	out, err := tools.ChannelsList(context.Background(), ChannelsListInput{Limit: 2})
	if err != nil {
		t.Fatalf("ChannelsList: %v", err)
	}
	if out.NextCursor != "PAGE2" || len(out.Channels) != 2 {
		t.Fatalf("unexpected wildcard listing: %+v", out)
	}
	// Must use the member-scoped API, never per-ID info or enumeration.
	if len(api.memberCalls) != 1 || api.memberCalls[0].Limit != 2 {
		t.Errorf("expected one MemberChannels call with limit 2, got %+v", api.memberCalls)
	}
	if len(api.infoCalls) != 0 {
		t.Errorf("wildcard listing must not call ConversationInfo per channel")
	}
}

func TestWildcard_ChannelsListDropsNonMemberDefensively(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	// A malformed upstream row (non-member or mpim) must never be returned.
	api.memberPages = []*slackclient.ChannelPage{{
		Channels: []slackclient.ChannelMeta{
			{ID: "C01234567", Name: "ok", IsMember: true},
			{ID: "C09999999", Name: "sneaky", IsMember: false},
			{ID: "G0MPIM0001", IsMpIM: true, IsMember: true},
		},
	}}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	out, err := tools.ChannelsList(context.Background(), ChannelsListInput{})
	if err != nil {
		t.Fatalf("ChannelsList: %v", err)
	}
	if len(out.Channels) != 1 || out.Channels[0].ID != "C01234567" {
		t.Errorf("must return only the member, non-mpim channel: %+v", out.Channels)
	}
}

func TestWildcard_ChannelsListByIDVerifiesMembership(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.infoByID["C01234567"] = &slackclient.ChannelMeta{ID: "C01234567", IsMember: true}
	api.infoByID["C09999999"] = &slackclient.ChannelMeta{ID: "C09999999", IsMember: false}
	tools := &Tools{API: api, Allow: wildcardAllow(t)}

	// A member channel by ID is returned.
	out, err := tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"C01234567"}})
	if err != nil || len(out.Channels) != 1 {
		t.Fatalf("member-by-id: out=%+v err=%v", out, err)
	}
	// A non-member channel by ID is a hard error, never returned.
	_, err = tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"C09999999"}})
	if got := codeOf(t, err); got != slackclient.CodeNotInChannel {
		t.Errorf("non-member by id code = %s, want NOT_IN_CHANNEL", got)
	}
}

func TestWildcard_ChannelsListByIDStillRejectsBadFormat(t *testing.T) {
	t.Parallel()
	tools := &Tools{API: newFakeAPI(), Allow: wildcardAllow(t)}
	if got := codeOf(t, mustListErr(tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{"D01234567"}}))); got != slackclient.CodeInvalidRequest {
		t.Errorf("dm-by-id code = %s, want INVALID_REQUEST", got)
	}
}

func TestExplicit_HistorySkipsMembershipPrecheck(t *testing.T) {
	t.Parallel()
	// In explicit mode there is no pre-flight ConversationInfo call; Slack
	// enforces membership and the error is sanitized.
	api := newFakeAPI()
	api.historyByID["C01234567"] = &slackclient.Page{Messages: []slackclient.Message{{TS: "1.0"}}}
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	if _, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"}); err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(api.infoCalls) != 0 {
		t.Errorf("explicit mode must not add a membership pre-check, got %v", api.infoCalls)
	}
}

// mustErr returns the error from a (Page, error) result for terse assertions.
func mustErr(_ slackclient.Page, err error) error { return err }

// mustListErr returns the error from a (ChannelsListOutput, error) result.
func mustListErr(_ ChannelsListOutput, err error) error { return err }

func TestHandlers_PropagateSanitizedSlackErrors(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.err = slackclient.NewError(slackclient.CodeNotInChannel, "nope")
	tools := &Tools{API: api, Allow: allowlist(t, "C01234567")}

	if _, err := tools.History(context.Background(), HistoryInput{ChannelID: "C01234567"}); codeOf(t, err) != slackclient.CodeNotInChannel {
		t.Errorf("history did not propagate NOT_IN_CHANNEL")
	}
}
