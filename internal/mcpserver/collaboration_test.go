package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matheus3301/mcp-slack/internal/config"
	"github.com/matheus3301/mcp-slack/internal/receipts"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// All identifiers are synthetic fixtures, never deployment identifiers.
const fixtureChannel = "C01234567"
const fixtureTeam = "T01234567"
const fixtureBot = "B01234567"
const fixtureUser = "U01234567"
const fixtureHuman = "U07654321"
const fixtureTS = "1790000000.123456"
const fixtureFile = "F01234567"

var fullCatalog = []string{
	"slack_auth_test", "slack_channels_list", "slack_conversations_history", "slack_conversations_replies", "slack_conversations_info", "slack_conversations_members", "slack_chat_get_permalink", "slack_users_info", "slack_messages_search", "slack_chat_post_message", "slack_chat_update", "slack_chat_delete", "slack_chat_schedule_message", "slack_chat_scheduled_messages_list", "slack_chat_delete_scheduled_message", "slack_reactions_get", "slack_reactions_add", "slack_reactions_remove", "slack_pins_list", "slack_pins_add", "slack_pins_remove", "slack_bookmarks_list", "slack_bookmarks_add", "slack_bookmarks_edit", "slack_bookmarks_remove", "slack_files_info", "slack_files_download", "slack_files_upload", "slack_dms_open",
}
var productionCatalog = []string{"slack_auth_test", "slack_chat_post_message", "slack_chat_update", "slack_chat_schedule_message", "slack_chat_scheduled_messages_list", "slack_chat_delete_scheduled_message"}
var mutationMethods = []string{"chat.postMessage", "chat.update", "chat.delete", "chat.scheduleMessage", "chat.deleteScheduledMessage", "reactions.add", "reactions.remove", "pins.add", "pins.remove", "bookmarks.add", "bookmarks.edit", "bookmarks.remove", "files.getUploadURLExternal", "files.completeUploadExternal", "conversations.open"}

type callRecord struct {
	method string
	form   url.Values
}
type collaborationFixture struct {
	server    *httptest.Server
	mu        sync.Mutex
	calls     []callRecord
	overrides map[string]func(http.ResponseWriter, *http.Request)
	env       map[string]string
	tools     *Tools
}
type fixtureTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "files.slack.com" {
		r = r.Clone(r.Context())
		u := *r.URL
		u.Scheme = f.target.Scheme
		u.Host = f.target.Host
		r.URL = &u
		r.Host = f.target.Host
	}
	return f.base.RoundTrip(r)
}
func newCollaborationFixture(t *testing.T) *collaborationFixture {
	t.Helper()
	f := &collaborationFixture{overrides: map[string]func(http.ResponseWriter, *http.Request){}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.env = map[string]string{"SLACK_BOT_TOKEN": "xoxb-synthetic-test-token", "SLACK_READ_ALLOWED_CHANNELS": fixtureChannel, "SLACK_WRITE_ALLOWED_CHANNELS": fixtureChannel, "SLACK_EXPECTED_TEAM_ID": fixtureTeam, "SLACK_EXPECTED_BOT_ID": fixtureBot, "SLACK_CAPABILITIES": "read,diagnostics,conversations,discovery,messages,schedule,reactions,pins,bookmarks,files,dms", "SLACK_STATE_DIR": filepath.Join(t.TempDir(), "receipts"), "SLACK_ARTIFACT_ROOT": t.TempDir(), "SLACK_DM_READ_ENABLED": "true", "SLACK_DM_WRITE_ENABLED": "true", "SLACK_MPIM_READ_ENABLED": "true", "SLACK_MPIM_WRITE_ENABLED": "true", "SLACK_DM_ALLOWED_USERS": fixtureHuman + ",U02345678"}
	f.configure(t)
	return f
}
func (f *collaborationFixture) configure(t *testing.T) {
	t.Helper()
	cfg, e := config.Load(func(k string) string { return f.env[k] })
	if e != nil {
		t.Fatal(e)
	}
	target, _ := url.Parse(f.server.URL)
	client := &http.Client{Transport: fixtureTransport{target, http.DefaultTransport}}
	opts := []slackclient.Option{slackclient.WithAPIURL(f.server.URL + "/"), slackclient.WithHTTPClient(client)}
	st, e := receipts.Open(cfg.Policy.StateDir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close() })
	f.tools = &Tools{API: slackclient.New(cfg.BotToken(), opts...), Slack: slackclient.NewCollaboration(cfg.BotToken(), opts...), Allow: cfg.Allowlist(), Policy: cfg.Policy, Store: st, Canceler: slackclient.NewScheduledCanceler(cfg.BotToken(), opts...)}
}
func (f *collaborationFixture) override(method string, fn func(http.ResponseWriter, *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[method] = fn
}
func (f *collaborationFixture) respond(method string, body any) {
	f.override(method, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(body) })
}
func (f *collaborationFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.method == method {
			n++
		}
	}
	return n
}
func (f *collaborationFixture) mutations() int {
	n := 0
	for _, m := range mutationMethods {
		n += f.count(m)
	}
	return n
}
func (f *collaborationFixture) last(method string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].method == method {
			return f.calls[i].form
		}
	}
	return nil
}
func fixtureMessage(ts string) map[string]any {
	return map[string]any{"type": "message", "ts": ts, "user": fixtureUser, "bot_id": fixtureBot, "text": "hello fixture", "files": []any{map[string]any{"id": fixtureFile}}}
}
func fixtureChannelInfo(id string) map[string]any {
	return map[string]any{"id": id, "is_member": true, "is_channel": true, "context_team_id": fixtureTeam}
}
func (f *collaborationFixture) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/")
	_ = r.ParseForm()
	f.mu.Lock()
	f.calls = append(f.calls, callRecord{method, r.Form})
	override := f.overrides[method]
	f.mu.Unlock()
	if override != nil {
		override(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	ch := r.Form.Get("channel")
	if ch == "" {
		ch = fixtureChannel
	}
	ts := r.Form.Get("latest")
	if ts == "" {
		ts = fixtureTS
	}
	result := map[string]any{"ok": true}
	switch method {
	case "auth.test":
		result["team_id"] = fixtureTeam
		result["bot_id"] = fixtureBot
		result["user_id"] = fixtureUser
	case "conversations.info":
		info := fixtureChannelInfo(ch)
		if strings.HasPrefix(ch, "D") {
			info["is_im"] = true
			info["is_channel"] = false
		}
		if ch == "G01234567" {
			info["is_mpim"] = true
			info["is_channel"] = false
		}
		result["channel"] = info
	case "conversations.members":
		members := []string{fixtureUser, fixtureHuman}
		if ch == "G01234567" {
			members = append(members, "U02345678")
		}
		result["members"] = members
	case "users.info":
		result["user"] = map[string]any{"id": r.Form.Get("user"), "team_id": fixtureTeam, "name": "person", "real_name": "Fixture Person", "profile": map[string]any{"email": "never-return@example.invalid", "display_name": "Fixture"}}
	case "conversations.history":
		result["messages"] = []any{fixtureMessage(ts)}
	case "conversations.replies":
		m := fixtureMessage(ts)
		m["thread_ts"] = r.Form.Get("ts")
		result["messages"] = []any{m}
	case "users.conversations":
		result["channels"] = []any{fixtureChannelInfo(fixtureChannel)}
	case "chat.postMessage", "chat.update", "chat.delete":
		result["channel"] = ch
		result["ts"] = fixtureTS
	case "chat.getPermalink":
		result["permalink"] = "https://fixture.slack.com/archives/" + ch + "/p1790000000123456"
	case "chat.scheduleMessage":
		result["channel"] = ch
		result["scheduled_message_id"] = "Q01234567"
	case "chat.scheduledMessages.list":
		result["scheduled_messages"] = []any{map[string]any{"id": "Q01234567", "channel_id": ch, "text": "scheduled fixture", "post_at": time.Now().Add(time.Hour).Unix()}}
	case "chat.deleteScheduledMessage", "reactions.add", "reactions.remove", "pins.add", "pins.remove", "bookmarks.remove":
	case "reactions.get":
		m := fixtureMessage(fixtureTS)
		m["reactions"] = []any{map[string]any{"name": "wave", "count": 1, "users": []string{fixtureUser}}}
		result["type"] = "message"
		result["channel"] = ch
		result["message"] = m
	case "pins.list":
		result["items"] = []any{map[string]any{"type": "message", "channel": ch, "message": fixtureMessage(fixtureTS)}}
	case "bookmarks.list":
		result["bookmarks"] = []any{map[string]any{"id": "Bk07654321", "channel_id": ch, "title": "fixture", "link": "https://example.invalid"}}
	case "bookmarks.add", "bookmarks.edit":
		result["bookmark"] = map[string]any{"id": "Bk07654321", "channel_id": r.Form.Get("channel_id"), "title": r.Form.Get("title"), "link": r.Form.Get("link")}
	case "files.info":
		result["file"] = map[string]any{"id": fixtureFile, "size": 4, "name": "test.txt", "title": "fixture", "mimetype": "text/plain", "url_private_download": "https://files.slack.com/files-pri/" + fixtureTeam + "-" + fixtureFile + "/test.txt"}
	case "files.getUploadURLExternal":
		result["file_id"] = fixtureFile
		result["upload_url"] = "https://files.slack.com/upload/v1/fixture"
	case "files.completeUploadExternal":
		result["files"] = []any{map[string]any{"id": fixtureFile, "title": "fixture"}}
	case "conversations.open":
		id := "D01234567"
		if strings.Contains(r.Form.Get("users"), ",") {
			id = "G01234567"
		}
		result["channel"] = map[string]any{"id": id}
	case "files-pri/" + fixtureTeam + "-" + fixtureFile + "/test.txt":
		_, _ = w.Write([]byte("test"))
		return
	case "upload/v1/fixture":
		_, _ = w.Write([]byte("OK - 4"))
		return
	default:
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"ok":false,"error":"unexpected_test_endpoint"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
func callTool(t *testing.T, s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestExpandedCatalogAndProductionFilter(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			f := newCollaborationFixture(t)
			want := append([]string{}, fullCatalog...)
			if restricted {
				f.env["SLACK_CAPABILITIES"] = "diagnostics,messages,schedule"
				f.env["SLACK_ENABLED_TOOLS"] = strings.Join(productionCatalog, ",")
				for _, k := range []string{"SLACK_DM_READ_ENABLED", "SLACK_DM_WRITE_ENABLED", "SLACK_MPIM_READ_ENABLED", "SLACK_MPIM_WRITE_ENABLED"} {
					delete(f.env, k)
				}
				delete(f.env, "SLACK_READ_ALLOWED_CHANNELS")
				f.configure(t)
				want = append([]string{}, productionCatalog...)
			}
			s := connect(t, f.tools)
			r, e := s.ListTools(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			var got []string
			for _, tool := range r.Tools {
				got = append(got, tool.Name)
				if tool.InputSchema == nil || tool.OutputSchema == nil {
					t.Fatalf("missing schemas: %s", tool.Name)
				}
			}
			slices.Sort(want)
			slices.Sort(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("catalog=%v", got)
			}
			if f.count("auth.test") != 0 {
				t.Fatal("discovery must not contact Slack")
			}
			if restricted {
				if _, e = s.CallTool(context.Background(), &mcp.CallToolParams{Name: "slack_chat_delete", Arguments: map[string]any{}}); e == nil {
					t.Fatal("disabled delete registered")
				}
			}
		})
	}
}
func TestEveryExpandedToolAgainstSimulatedSlack(t *testing.T) {
	f := newCollaborationFixture(t)
	s := connect(t, f.tools)
	channel := map[string]any{"channel_id": fixtureChannel}
	message := map[string]any{"channel_id": fixtureChannel, "ts": fixtureTS}
	file := map[string]any{"channel_id": fixtureChannel, "ts": fixtureTS, "file_id": fixtureFile}
	cases := []struct {
		name string
		args any
	}{
		{"slack_auth_test", map[string]any{}}, {ToolChannelsList, map[string]any{}}, {ToolHistory, channel}, {ToolReplies, map[string]any{"channel_id": fixtureChannel, "thread_ts": fixtureTS}},
		{"slack_conversations_info", channel}, {"slack_conversations_members", channel}, {"slack_chat_get_permalink", message}, {"slack_users_info", map[string]any{"user_id": fixtureHuman, "channel_id": fixtureChannel}}, {"slack_messages_search", map[string]any{"channel_id": fixtureChannel, "query": "HELLO"}},
		{"slack_chat_post_message", map[string]any{"channel_id": fixtureChannel, "text": "hello <!channel> & <@U01234567>", "operation_key": "post-1"}},
		{"slack_chat_update", map[string]any{"channel_id": fixtureChannel, "ts": fixtureTS, "text": "updated"}}, {"slack_chat_delete", message},
		{"slack_chat_schedule_message", map[string]any{"channel_id": fixtureChannel, "text": "later", "post_at": time.Now().Add(time.Hour).Unix(), "operation_key": "schedule-1"}}, {"slack_chat_scheduled_messages_list", channel}, {"slack_chat_delete_scheduled_message", map[string]any{"channel_id": fixtureChannel, "scheduled_message_id": "Q01234567"}},
		{"slack_reactions_get", message}, {"slack_reactions_add", map[string]any{"channel_id": fixtureChannel, "ts": fixtureTS, "name": "wave"}}, {"slack_reactions_remove", map[string]any{"channel_id": fixtureChannel, "ts": fixtureTS, "name": "wave"}},
		{"slack_pins_list", channel}, {"slack_pins_add", message}, {"slack_pins_remove", message}, {"slack_bookmarks_list", channel},
		{"slack_bookmarks_add", map[string]any{"channel_id": fixtureChannel, "title": "Example", "link": "https://example.invalid"}}, {"slack_bookmarks_edit", map[string]any{"channel_id": fixtureChannel, "bookmark_id": "Bk07654321", "title": "Updated", "link": "https://example.invalid/new"}}, {"slack_bookmarks_remove", map[string]any{"channel_id": fixtureChannel, "bookmark_id": "Bk07654321"}},
		{"slack_files_info", file}, {"slack_files_download", file}, {"slack_files_upload", map[string]any{"channel_id": fixtureChannel, "filename": "test.txt", "content_base64": base64.StdEncoding.EncodeToString([]byte("test")), "operation_key": "upload-1"}}, {"slack_dms_open", map[string]any{"users": []string{fixtureHuman}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := callTool(t, s, tc.name, tc.args)
			if r.IsError {
				t.Fatalf("%v", r.Content)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "never-return@example.invalid") || strings.Contains(string(b), "xoxb-") {
				t.Fatal("sensitive response projection")
			}
		})
	}
	if len(cases) != 29 {
		t.Fatal("missing catalog coverage")
	}
	form := f.last("chat.postMessage")
	for key, want := range map[string]string{"mrkdwn": "false", "parse": "none", "link_names": "false", "unfurl_links": "false", "unfurl_media": "false"} {
		if form.Get(key) != want {
			t.Errorf("%s=%q", key, form.Get(key))
		}
	}
	if strings.Contains(form.Get("text"), "<!channel>") || form.Get("reply_broadcast") == "true" || form.Get("username") != "" {
		t.Fatal("unsafe message rendering")
	}
	if f.last("chat.scheduledMessages.list").Get("channel") != fixtureChannel {
		t.Fatal("global schedule listing")
	}
}

func TestWriteSecurityBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*collaborationFixture)
		channel string
	}{
		{"not allowlisted", nil, "C09999999"}, {"user destination", nil, fixtureHuman}, {"channel name", nil, "#general"},
		{"wrong team", func(f *collaborationFixture) {
			f.respond("auth.test", map[string]any{"ok": true, "team_id": "T09999999", "bot_id": fixtureBot, "user_id": fixtureUser})
		}, fixtureChannel},
		{"not bot", func(f *collaborationFixture) {
			f.respond("auth.test", map[string]any{"ok": true, "team_id": fixtureTeam, "user_id": fixtureUser})
		}, fixtureChannel},
		{"wrong bot", func(f *collaborationFixture) {
			f.respond("auth.test", map[string]any{"ok": true, "team_id": fixtureTeam, "bot_id": "B09999999", "user_id": fixtureUser})
		}, fixtureChannel},
	}
	for _, field := range []string{"is_member", "is_channel", "is_archived", "is_read_only", "is_shared", "is_ext_shared", "is_org_shared", "is_global_shared", "is_pending_ext_shared", "is_im", "is_mpim", "context_team_id", "id"} {
		cases = append(cases, struct {
			name    string
			setup   func(*collaborationFixture)
			channel string
		}{field, func(f *collaborationFixture) {
			ch := fixtureChannelInfo(fixtureChannel)
			switch field {
			case "is_member", "is_channel":
				ch[field] = false
			case "context_team_id":
				ch[field] = "T09999999"
			case "id":
				ch[field] = "C09999999"
			default:
				ch[field] = true
			}
			f.respond("conversations.info", map[string]any{"ok": true, "channel": ch})
		}, fixtureChannel})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCollaborationFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			_, e := f.tools.post(context.Background(), PostInput{ChannelID: tc.channel, Text: "hello", OperationKey: "denied"})
			if e == nil || f.mutations() != 0 {
				t.Fatalf("err=%v mutations=%d", e, f.mutations())
			}
		})
	}
}
func TestIndependentReadWriteGrants(t *testing.T) {
	f := newCollaborationFixture(t)
	f.env["SLACK_READ_ALLOWED_CHANNELS"] = "C07654321"
	f.configure(t)
	if _, e := f.tools.History(context.Background(), HistoryInput{ChannelID: fixtureChannel}); e == nil {
		t.Fatal("write grant allowed history")
	}
	if _, e := f.tools.post(context.Background(), PostInput{ChannelID: "C07654321", Text: "hello", OperationKey: "read-only"}); e == nil {
		t.Fatal("read grant allowed write")
	}
	if f.mutations() != 0 {
		t.Fatal("denied operation reached Slack")
	}
}
func TestOwnMessageAndResourceBoundaries(t *testing.T) {
	for _, kind := range []string{"other user", "other bot", "missing bot", "wrong ts", "reply as root", "file not attached", "bookmark other channel", "schedule other channel", "reaction other channel", "pin other channel"} {
		t.Run(kind, func(t *testing.T) {
			f := newCollaborationFixture(t)
			ctx := context.Background()
			m := fixtureMessage(fixtureTS)
			var err error
			switch kind {
			case "other user", "other bot", "missing bot", "wrong ts", "reply as root":
				switch kind {
				case "other user":
					m["user"] = fixtureHuman
				case "other bot":
					m["bot_id"] = "B09999999"
				case "missing bot":
					delete(m, "bot_id")
				case "wrong ts":
					m["ts"] = "1790000000.000001"
				case "reply as root":
					m["thread_ts"] = "1790000000.000001"
				}
				f.respond("conversations.history", map[string]any{"ok": true, "messages": []any{m}})
				if kind == "reply as root" {
					_, err = f.tools.post(ctx, PostInput{ChannelID: fixtureChannel, Text: "reply", ThreadTS: fixtureTS, OperationKey: "reply"})
				} else {
					_, err = f.tools.update(ctx, UpdateInput{ChannelID: fixtureChannel, TS: fixtureTS, Text: "change"})
					if _, e := f.tools.deleteMessage(ctx, MessageInput{ChannelID: fixtureChannel, TS: fixtureTS}); e == nil {
						t.Fatal("delete allowed")
					}
				}
			case "file not attached":
				m["files"] = []any{}
				f.respond("conversations.history", map[string]any{"ok": true, "messages": []any{m}})
				_, err = f.tools.fileInfo(ctx, FileInput{ChannelID: fixtureChannel, TS: fixtureTS, FileID: fixtureFile})
				if f.count("files.info") != 0 {
					t.Fatal("unassociated file fetched")
				}
			case "bookmark other channel":
				f.respond("bookmarks.list", map[string]any{"ok": true, "bookmarks": []any{map[string]any{"id": "Bk07654321", "channel_id": "C09999999"}}})
				_, err = f.tools.removeBookmark(ctx, BookmarkRemoveInput{fixtureChannel, "Bk07654321"})
			case "schedule other channel":
				f.respond("chat.scheduledMessages.list", map[string]any{"ok": true, "scheduled_messages": []any{map[string]any{"id": "Q01234567", "channel_id": "C09999999"}}})
				_, err = f.tools.cancelScheduled(ctx, CancelInput{fixtureChannel, "Q01234567"})
			case "reaction other channel":
				f.respond("reactions.get", map[string]any{"ok": true, "type": "message", "channel": "C09999999", "message": m})
				_, err = f.tools.reactions(ctx, MessageInput{ChannelID: fixtureChannel, TS: fixtureTS})
			case "pin other channel":
				f.respond("pins.list", map[string]any{"ok": true, "items": []any{map[string]any{"type": "message", "channel": "C09999999", "message": m}}})
				_, err = f.tools.pins(ctx, ChannelInput{fixtureChannel})
			}
			if err == nil || f.mutations() != 0 {
				t.Fatalf("err=%v mutations=%d", err, f.mutations())
			}
		})
	}
}
func TestDMAndMPIMBoundaries(t *testing.T) {
	for _, kind := range []string{"disabled DM", "disabled MPIM", "outsider", "missing bot", "incomplete members", "wrong user team", "bot recipient", "returned wrong members"} {
		t.Run(kind, func(t *testing.T) {
			f := newCollaborationFixture(t)
			ch := "G01234567"
			switch kind {
			case "disabled DM":
				ch = "D01234567"
				f.env["SLACK_DM_WRITE_ENABLED"] = "false"
				f.configure(t)
			case "disabled MPIM":
				f.env["SLACK_MPIM_WRITE_ENABLED"] = "false"
				f.configure(t)
			case "outsider":
				f.respond("conversations.members", map[string]any{"ok": true, "members": []string{fixtureUser, fixtureHuman, "U09999999"}})
			case "missing bot":
				f.respond("conversations.members", map[string]any{"ok": true, "members": []string{fixtureHuman, "U02345678"}})
			case "incomplete members":
				f.respond("conversations.members", map[string]any{"ok": true, "members": []string{fixtureUser, fixtureHuman}, "response_metadata": map[string]any{"next_cursor": "MORE"}})
			case "wrong user team", "bot recipient":
				u := map[string]any{"id": fixtureHuman, "team_id": fixtureTeam}
				if kind == "wrong user team" {
					u["team_id"] = "T09999999"
				} else {
					u["is_bot"] = true
				}
				f.respond("users.info", map[string]any{"ok": true, "user": u})
			case "returned wrong members":
				f.respond("conversations.members", map[string]any{"ok": true, "members": []string{fixtureUser, "U02345678"}})
			}
			if kind == "returned wrong members" {
				if _, e := f.tools.openDM(context.Background(), DMInput{[]string{fixtureHuman}}); e == nil {
					t.Fatal("wrong conversation accepted")
				}
				return
			}
			if _, e := f.tools.post(context.Background(), PostInput{ChannelID: ch, Text: "blocked", OperationKey: "dm-denied"}); e == nil {
				t.Fatal("DM write allowed")
			}
			if f.mutations() != 0 {
				t.Fatal("denied write reached Slack")
			}
		})
	}
	f := newCollaborationFixture(t)
	ctx := context.Background()
	out, e := f.tools.openDM(ctx, DMInput{[]string{fixtureHuman, "U02345678"}})
	if e != nil || !out.IsMPIM {
		t.Fatalf("MPIM=%+v err=%v", out, e)
	}
	for _, ch := range []string{"D01234567", "G01234567"} {
		if _, e = f.tools.History(ctx, HistoryInput{ChannelID: ch}); e != nil {
			t.Fatal(e)
		}
		if _, e = f.tools.Replies(ctx, RepliesInput{ChannelID: ch, ThreadTS: fixtureTS}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestPostReceiptsReplayConflictAndAmbiguity(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			f := newCollaborationFixture(t)
			if ambiguous {
				f.override("chat.postMessage", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(500)
					_, _ = w.Write([]byte("secret raw body"))
				})
			}
			in := PostInput{ChannelID: fixtureChannel, Text: "private-body-never-persist", OperationKey: "stable"}
			out, e := f.tools.post(context.Background(), in)
			if ambiguous && e == nil || !ambiguous && e != nil {
				t.Fatalf("first=%+v %v", out, e)
			}
			// Reopen the state as a restarted process would.
			f.configure(t)
			out, e = f.tools.post(context.Background(), in)
			if ambiguous && e != receipts.ErrUncertain || !ambiguous && (e != nil || !out.Replayed) {
				t.Fatalf("replay=%+v %v", out, e)
			}
			if f.count("chat.postMessage") != 1 {
				t.Fatal("duplicate send")
			}
			in.Text = "different"
			if _, e = f.tools.post(context.Background(), in); e != receipts.ErrConflict {
				t.Fatalf("conflict=%v", e)
			}
			entries, _ := os.ReadDir(f.env["SLACK_STATE_DIR"])
			for _, entry := range entries {
				b, e := os.ReadFile(filepath.Join(f.env["SLACK_STATE_DIR"], entry.Name()))
				if e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(b), "private-body") || strings.Contains(string(b), "xoxb-") {
					t.Fatal("persisted message/token")
				}
			}
		})
	}
}
func TestFilePathURLAndSizeBoundaries(t *testing.T) {
	f := newCollaborationFixture(t)
	root := f.env["SLACK_ARTIFACT_ROOT"]
	if e := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("test"), 0600); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"../outside.txt", outside, "escape", ".", "safe.txt/../../outside"} {
		if _, e := f.tools.uploadBytes(UploadInput{Filename: "f.txt", ArtifactPath: path}); e == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	if _, e := f.tools.uploadBytes(UploadInput{Filename: "safe.txt", ArtifactPath: "safe.txt"}); e != nil {
		t.Fatal(e)
	}
	for _, in := range []UploadInput{{Filename: "../name", ContentBase64: "dGVzdA=="}, {Filename: "file", ContentBase64: "invalid?"}, {Filename: "file", ContentBase64: base64.StdEncoding.EncodeToString(make([]byte, MaxFileBytes+1))}, {Filename: "file", ContentBase64: "dGVzdA==", ArtifactPath: "safe.txt"}} {
		if _, e := f.tools.uploadBytes(in); e == nil {
			t.Fatal("invalid upload accepted")
		}
	}
	for _, raw := range []string{"https://evil.invalid/upload/v1/x", "http://files.slack.com/upload/v1/x", "https://files.slack.com.evil.invalid/upload/v1/x", "https://files.slack.com@evil.invalid/upload/v1/x", "https://files.slack.com:444/upload/v1/x", "https://files.slack.com/upload/../private", "https://files.slack.com/upload/%2e%2e/private"} {
		if safeSlackFileURL(raw, "/upload/") {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://files.slack.com/upload/v1/fixture", "https://files.slack.com/upload/v1/fixture?token=synthetic"} {
		if !safeSlackFileURL(raw, "/upload/") {
			t.Errorf("rejected real Slack URL shape")
		}
	}
	f.respond("files.info", map[string]any{"ok": true, "file": map[string]any{"id": fixtureFile, "size": 4, "url_private_download": "https://evil.invalid/secret"}})
	if _, e := f.tools.download(context.Background(), FileInput{ChannelID: fixtureChannel, TS: fixtureTS, FileID: fixtureFile}); e == nil {
		t.Fatal("download SSRF accepted")
	}
	f.respond("files.getUploadURLExternal", map[string]any{"ok": true, "file_id": fixtureFile, "upload_url": "https://evil.invalid/upload/v1/x"})
	if _, e := f.tools.upload(context.Background(), UploadInput{ChannelID: fixtureChannel, Filename: "f.txt", ContentBase64: "dGVzdA==", OperationKey: "bad-url"}); e == nil {
		t.Fatal("upload SSRF accepted")
	}
	if f.count("files.completeUploadExternal") != 0 {
		t.Fatal("invalid upload completed")
	}
}
func TestToolSchemaRejectsIdentityAndUnsafeFields(t *testing.T) {
	f := newCollaborationFixture(t)
	s := connect(t, f.tools)
	for _, field := range []string{"username", "icon_url", "as_user", "reply_broadcast", "unfurl_links", "blocks", "attachments", "method", "url"} {
		args := map[string]any{"channel_id": fixtureChannel, "text": "hello", "operation_key": "invalid", field: "override"}
		r, e := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "slack_chat_post_message", Arguments: args})
		if e == nil && !r.IsError {
			t.Errorf("accepted %s", field)
		}
	}
	if f.mutations() != 0 {
		t.Fatal("schema rejection mutated Slack")
	}
}
func TestStdioCollaborationHelper(t *testing.T) {
	if os.Getenv("MCP_SLACK_TEST_HELPER") != "1" {
		return
	}
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		os.Exit(2)
	}
	opts := []slackclient.Option{slackclient.WithAPIURL(os.Getenv("MCP_SLACK_TEST_URL"))}
	st, e := receipts.Open(cfg.Policy.StateDir)
	if e != nil {
		os.Exit(3)
	}
	defer st.Close()
	tools := &Tools{API: slackclient.New(cfg.BotToken(), opts...), Slack: slackclient.NewCollaboration(cfg.BotToken(), opts...), Allow: cfg.Allowlist(), Policy: cfg.Policy, Store: st, Canceler: slackclient.NewScheduledCanceler(cfg.BotToken(), opts...)}
	if New(tools, "test").Run(context.Background(), &mcp.StdioTransport{}) != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
func TestStdioSimulatedSlackIntegration(t *testing.T) {
	f := newCollaborationFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestStdioCollaborationHelper$")
	cmd.Env = []string{"MCP_SLACK_TEST_HELPER=1", "MCP_SLACK_TEST_URL=" + f.server.URL + "/"}
	for k, v := range f.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "test"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, e := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 29 {
		t.Fatalf("catalog err=%v", e)
	}
	r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "slack_chat_post_message", Arguments: map[string]any{"channel_id": fixtureChannel, "text": "stdio", "operation_key": "stdio"}})
	if e != nil || r.IsError {
		t.Fatalf("post err=%v result=%+v", e, r)
	}
	r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "slack_chat_post_message", Arguments: map[string]any{"channel_id": "C09999999", "text": "denied", "operation_key": "stdio-denied"}})
	if e != nil || !r.IsError {
		t.Fatalf("denial err=%v result=%+v", e, r)
	}
	if f.count("chat.postMessage") != 1 {
		t.Fatal("unexpected sends")
	}
}

func TestDownloadUsesVerifiedFileAcrossStorageTenants(t *testing.T) {
	f := newCollaborationFixture(t)
	path := "files-pri/T07654321-" + fixtureFile + "/download/test.txt"
	f.respond("files.info", map[string]any{"ok": true, "file": map[string]any{"id": fixtureFile, "size": 4, "name": "test.txt", "url_private_download": "https://files.slack.com/" + path}})
	f.override(path, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("test")) })
	out, e := f.tools.download(context.Background(), FileInput{ChannelID: fixtureChannel, TS: fixtureTS, FileID: fixtureFile})
	if e != nil || out.ContentBase64 != "dGVzdA==" {
		t.Fatalf("download=%+v %v", out, e)
	}
	for _, raw := range []string{"https://files.slack.com/files-pri/T07654321-F09999999/download/test.txt", "https://files.slack.com/files-pri/invalid-" + fixtureFile + "/download/test.txt", "https://evil.invalid/files-pri/T07654321-" + fixtureFile + "/download/test.txt", "https://files.slack.com/files-pri/T07654321-" + fixtureFile + "/../secret"} {
		if safeDownloadURL(raw, fixtureFile) {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestAmbiguousScheduleAndUploadNeverRestart(t *testing.T) {
	for _, kind := range []string{"schedule", "upload"} {
		t.Run(kind, func(t *testing.T) {
			f := newCollaborationFixture(t)
			method := "chat.scheduleMessage"
			if kind == "upload" {
				method = "files.completeUploadExternal"
			}
			f.override(method, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("upstream-private-details"))
			})
			schedule := ScheduleInput{ChannelID: fixtureChannel, Text: "later", PostAt: time.Now().Add(time.Hour).Unix(), OperationKey: "uncertain"}
			upload := UploadInput{ChannelID: fixtureChannel, Filename: "test.txt", ContentBase64: "dGVzdA==", OperationKey: "uncertain"}
			for range 2 {
				var e error
				if kind == "schedule" {
					_, e = f.tools.schedule(context.Background(), schedule)
				} else {
					_, e = f.tools.upload(context.Background(), upload)
				}
				if e != receipts.ErrUncertain {
					t.Fatalf("err=%v", e)
				}
			}
			if f.count(method) != 1 {
				t.Fatal("ambiguous mutation retried")
			}
			if kind == "upload" && (f.count("files.getUploadURLExternal") != 1 || f.count("upload/v1/fixture") != 1) {
				t.Fatal("multi-stage upload restarted")
			}
		})
	}
}
func TestPostConnectionLossNeverRetries(t *testing.T) {
	f := newCollaborationFixture(t)
	f.override("chat.postMessage", func(w http.ResponseWriter, _ *http.Request) {
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, _, e := h.Hijack()
		if e == nil {
			_ = conn.Close()
		}
	})
	in := PostInput{ChannelID: fixtureChannel, Text: "possible delivery", OperationKey: "connection-lost"}
	for range 2 {
		if _, e := f.tools.post(context.Background(), in); e != receipts.ErrUncertain {
			t.Fatalf("err=%v", e)
		}
	}
	if f.count("chat.postMessage") != 1 {
		t.Fatal("transport failure repeated a send")
	}
}
func TestPostConcurrentOperationKey(t *testing.T) {
	f := newCollaborationFixture(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			_, e := f.tools.post(context.Background(), PostInput{ChannelID: fixtureChannel, Text: "once", OperationKey: "concurrent"})
			if e != nil && e != receipts.ErrUncertain && e != receipts.ErrStore {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if f.count("chat.postMessage") != 1 {
		t.Fatal("concurrent duplicate send")
	}
}
func TestPostRetriesOnlyExplicit429(t *testing.T) {
	f := newCollaborationFixture(t)
	f.override("chat.postMessage", func(w http.ResponseWriter, _ *http.Request) {
		if f.count("chat.postMessage") == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": fixtureChannel, "ts": fixtureTS})
	})
	if _, e := f.tools.post(context.Background(), PostInput{ChannelID: fixtureChannel, Text: "rate limited", OperationKey: "rate-limit"}); e != nil {
		t.Fatal(e)
	}
	if f.count("chat.postMessage") != 2 {
		t.Fatal("did not honor explicit 429")
	}
}
func TestBoundedSearchAndMembership(t *testing.T) {
	f := newCollaborationFixture(t)
	f.override("conversations.history", func(w http.ResponseWriter, r *http.Request) {
		next := r.Form.Get("cursor") + "A"
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []any{fixtureMessage(fixtureTS)}, "has_more": true, "response_metadata": map[string]string{"next_cursor": next}})
	})
	out, e := f.tools.search(context.Background(), SearchInput{ChannelID: fixtureChannel, Query: "hello", MaxPages: 5, Limit: 1})
	if e != nil || out.PagesScanned != 5 || !out.Partial || out.NextCursor == "" || f.count("conversations.history") != 5 {
		t.Fatalf("out=%+v err=%v", out, e)
	}
	if _, e = f.tools.search(context.Background(), SearchInput{ChannelID: fixtureChannel, Query: "x", MaxPages: 6}); e == nil {
		t.Fatal("unbounded search")
	}
	f.override("conversations.members", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []string{fixtureUser}, "response_metadata": map[string]string{"next_cursor": r.Form.Get("cursor") + "A"}})
	})
	if _, e = f.tools.userInfo(context.Background(), UserInput{UserID: "U09999999", ChannelID: fixtureChannel}); e == nil {
		t.Fatal("unverified user lookup")
	}
	if f.count("conversations.members") != 5 || f.count("users.info") != 0 {
		t.Fatal("unbounded membership or unauthorized profile read")
	}
}
