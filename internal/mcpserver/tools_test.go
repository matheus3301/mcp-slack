package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
)

// Every fixture explicitly selects its catalog and expected workspace. The
// same real SDK/HTTP fixture is used for reads and writes.
func newReadFixture(t *testing.T) *collaborationFixture {
	t.Helper()
	f := newCollaborationFixture(t)
	f.env["SLACK_CAPABILITIES"] = "read"
	for _, key := range []string{"SLACK_DM_READ_ENABLED", "SLACK_DM_WRITE_ENABLED", "SLACK_MPIM_READ_ENABLED", "SLACK_MPIM_WRITE_ENABLED"} {
		delete(f.env, key)
	}
	f.configure(t)
	return f
}
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *slackclient.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected sanitized error, got %v", err)
	}
	return e.Code
}
func TestChannelsListExplicitSubsetAndDedup(t *testing.T) {
	f := newReadFixture(t)
	f.env["SLACK_READ_ALLOWED_CHANNELS"] = fixtureChannel + ",G07654321"
	f.configure(t)
	out, e := f.tools.ChannelsList(context.Background(), ChannelsListInput{})
	if e != nil || len(out.Channels) != 2 {
		t.Fatalf("all=%+v err=%v", out, e)
	}
	out, e = f.tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{fixtureChannel, fixtureChannel}})
	if e != nil || len(out.Channels) != 1 || out.Channels[0].ID != fixtureChannel {
		t.Fatalf("subset=%+v err=%v", out, e)
	}
	if f.count("conversations.info") != 3 || f.count("auth.test") != 3 {
		t.Fatal("metadata did not pass shared identity/membership checks")
	}
}
func TestReadAuthorizationBoundaries(t *testing.T) {
	for _, allow := range []string{fixtureChannel, "*"} {
		for _, kind := range []string{"wrong team", "wrong bot", "not member", "wrong channel", "wrong channel team", "not channel", "DM", "MPIM", "shared", "upstream"} {
			for _, operation := range []string{"list", "history", "replies"} {
				t.Run(allow+"/"+kind+"/"+operation, func(t *testing.T) {
					f := newReadFixture(t)
					f.env["SLACK_READ_ALLOWED_CHANNELS"] = allow
					f.configure(t)
					ch := fixtureChannelInfo(fixtureChannel)
					switch kind {
					case "wrong team":
						f.respond("auth.test", map[string]any{"ok": true, "team_id": "T09999999", "bot_id": fixtureBot, "user_id": fixtureUser})
					case "wrong bot":
						f.respond("auth.test", map[string]any{"ok": true, "team_id": fixtureTeam, "bot_id": "B09999999", "user_id": fixtureUser})
					case "upstream":
						f.respond("conversations.info", map[string]any{"ok": false, "error": "not_in_channel"})
					default:
						switch kind {
						case "not member":
							ch["is_member"] = false
						case "wrong channel":
							ch["id"] = "C09999999"
						case "wrong channel team":
							ch["context_team_id"] = "T09999999"
						case "not channel":
							ch["is_channel"] = false
						case "DM":
							ch["is_im"] = true
						case "MPIM":
							ch["is_mpim"] = true
						case "shared":
							ch["is_shared"] = true
						}
						f.respond("conversations.info", map[string]any{"ok": true, "channel": ch})
					}
					var err error
					switch operation {
					case "list":
						_, err = f.tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{fixtureChannel}})
					case "history":
						_, err = f.tools.History(context.Background(), HistoryInput{ChannelID: fixtureChannel})
					case "replies":
						_, err = f.tools.Replies(context.Background(), RepliesInput{ChannelID: fixtureChannel, ThreadTS: fixtureTS})
					}
					if err == nil {
						t.Fatal("unauthorized read accepted")
					}
					if f.count("conversations.history")+f.count("conversations.replies") != 0 {
						t.Fatal("content fetched before authorization")
					}
				})
			}
		}
	}
}
func TestReadRejectsUnlistedAndMalformedDestinations(t *testing.T) {
	f := newReadFixture(t)
	for _, id := range []string{"C09999999", "D01234567", "U01234567", "#general", ""} {
		if _, e := f.tools.ChannelsList(context.Background(), ChannelsListInput{ChannelIDs: []string{id}}); e == nil {
			t.Fatalf("list accepted %q", id)
		}
		if _, e := f.tools.History(context.Background(), HistoryInput{ChannelID: id}); e == nil {
			t.Fatalf("history accepted %q", id)
		}
		if _, e := f.tools.Replies(context.Background(), RepliesInput{ChannelID: id, ThreadTS: fixtureTS}); e == nil {
			t.Fatalf("replies accepted %q", id)
		}
	}
	if f.count("conversations.info")+f.count("conversations.history")+f.count("conversations.replies") != 0 {
		t.Fatal("denied destination reached Slack")
	}
}
func TestReadWindowValidationAndForwarding(t *testing.T) {
	f := newReadFixture(t)
	ctx := context.Background()
	for _, in := range []HistoryInput{{ChannelID: fixtureChannel, Limit: 101}, {ChannelID: fixtureChannel, Limit: -1}, {ChannelID: fixtureChannel, Cursor: "bad cursor!"}, {ChannelID: fixtureChannel, Oldest: "bad"}, {ChannelID: fixtureChannel, Latest: "1.2"}} {
		if _, e := f.tools.History(ctx, in); codeOf(t, e) != slackclient.CodeInvalidRequest {
			t.Fatal(e)
		}
		if _, e := f.tools.Replies(ctx, RepliesInput{ChannelID: in.ChannelID, ThreadTS: fixtureTS, Limit: in.Limit, Cursor: in.Cursor, Oldest: in.Oldest, Latest: in.Latest}); codeOf(t, e) != slackclient.CodeInvalidRequest {
			t.Fatal(e)
		}
	}
	if _, e := f.tools.Replies(ctx, RepliesInput{ChannelID: fixtureChannel}); codeOf(t, e) != slackclient.CodeInvalidRequest {
		t.Fatal(e)
	}
	if f.count("conversations.history")+f.count("conversations.replies") != 0 {
		t.Fatal("invalid window fetched content")
	}
	page, e := f.tools.History(ctx, HistoryInput{ChannelID: fixtureChannel})
	if e != nil || len(page.Messages) != 1 {
		t.Fatalf("page=%+v err=%v", page, e)
	}
	if f.last("conversations.history").Get("limit") != "20" {
		t.Fatal("missing default page limit")
	}
	_, e = f.tools.Replies(ctx, RepliesInput{ChannelID: fixtureChannel, ThreadTS: fixtureTS, Limit: 5, Cursor: "NEXT", Oldest: fixtureTS, Latest: fixtureTS, Inclusive: true})
	if e != nil {
		t.Fatal(e)
	}
	form := f.last("conversations.replies")
	for key, want := range map[string]string{"ts": fixtureTS, "limit": "5", "cursor": "NEXT", "oldest": fixtureTS, "latest": fixtureTS, "inclusive": "1"} {
		if form.Get(key) != want {
			t.Errorf("%s=%q, want %q", key, form.Get(key), want)
		}
	}
}
func TestWildcardListingVerifiesIdentityBeforeEnumeration(t *testing.T) {
	f := newReadFixture(t)
	f.env["SLACK_READ_ALLOWED_CHANNELS"] = "*"
	f.configure(t)
	f.respond("auth.test", map[string]any{"ok": true, "team_id": "T09999999", "bot_id": fixtureBot, "user_id": fixtureUser})
	if _, e := f.tools.ChannelsList(context.Background(), ChannelsListInput{}); e == nil {
		t.Fatal("wrong workspace enumerated")
	}
	if f.count("users.conversations") != 0 {
		t.Fatal("enumerated before checking identity")
	}
}
func TestWildcardListingFiltersAndPaginates(t *testing.T) {
	f := newReadFixture(t)
	f.env["SLACK_READ_ALLOWED_CHANNELS"] = "*"
	f.configure(t)
	f.override("users.conversations", func(w http.ResponseWriter, _ *http.Request) {
		dm := fixtureChannelInfo("D01234567")
		dm["is_im"] = true
		mpim := fixtureChannelInfo("G01234567")
		mpim["is_mpim"] = true
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channels": []any{fixtureChannelInfo(fixtureChannel), dm, mpim}, "response_metadata": map[string]string{"next_cursor": "NEXT"}})
	})
	out, e := f.tools.ChannelsList(context.Background(), ChannelsListInput{Limit: 5, Cursor: "PREVIOUS"})
	if e != nil || len(out.Channels) != 1 || out.NextCursor != "NEXT" {
		t.Fatalf("out=%+v err=%v", out, e)
	}
	form := f.last("users.conversations")
	if form.Get("limit") != "5" || form.Get("cursor") != "PREVIOUS" {
		t.Fatal("pagination not forwarded")
	}
	if f.count("conversations.info") != 1 {
		t.Fatal("member result was not reauthorized")
	}
	for _, in := range []ChannelsListInput{{Limit: 101}, {Cursor: "bad cursor!"}} {
		if _, e = f.tools.ChannelsList(context.Background(), in); e == nil {
			t.Fatal("invalid listing window")
		}
	}
}
func TestReadMembershipRevocation(t *testing.T) {
	f := newReadFixture(t)
	if _, e := f.tools.History(context.Background(), HistoryInput{ChannelID: fixtureChannel}); e != nil {
		t.Fatal(e)
	}
	ch := fixtureChannelInfo(fixtureChannel)
	ch["is_member"] = false
	f.respond("conversations.info", map[string]any{"ok": true, "channel": ch})
	if _, e := f.tools.History(context.Background(), HistoryInput{ChannelID: fixtureChannel}); codeOf(t, e) != slackclient.CodeNotInChannel {
		t.Fatal(e)
	}
	if f.count("conversations.history") != 1 {
		t.Fatal("membership authorization was cached")
	}
}
