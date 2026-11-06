package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/matheus3301/mcp-slack/internal/receipts"
	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/matheus3301/mcp-slack/internal/validate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/slack-go/slack"
)

const requestTimeout = 120 * time.Second

func addTool[I, O any](s *mcp.Server, t *Tools, name, description string, read bool, fn func(context.Context, I) (O, error)) {
	if !t.Policy.Enabled(name) {
		return
	}
	annotations := readOnlyAnnotations(name)
	if !read {
		destructive := true
		annotations.ReadOnlyHint = false
		annotations.IdempotentHint = false
		annotations.DestructiveHint = &destructive
	}
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description, Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, O, error) {
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		out, err := fn(ctx, in)
		if err != nil {
			var safe *slackclient.Error
			switch {
			case errors.As(err, &safe):
			case errors.Is(err, receipts.ErrUncertain):
				err = slackclient.NewError("OPERATION_UNCERTAIN", "receipt is pending; reconcile before using a new key")
			case errors.Is(err, receipts.ErrConflict):
				err = slackclient.NewError("OPERATION_CONFLICT", "operation key was used with different arguments")
			case errors.Is(err, receipts.ErrStore):
				err = slackclient.NewError("STATE_UNAVAILABLE", "protected receipt storage is unavailable")
			default:
				err = slackclient.Sanitize(err)
			}
		}
		return nil, out, err
	})
}

type EmptyInput struct{}
type ChannelInput struct {
	ChannelID string `json:"channel_id"`
}
type PageInput struct {
	ChannelID string `json:"channel_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}
type MessageInput struct {
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts,omitempty" jsonschema:"parent timestamp when targeting a reply"`
}
type PostInput struct {
	ChannelID    string `json:"channel_id"`
	Text         string `json:"text"`
	ThreadTS     string `json:"thread_ts,omitempty"`
	OperationKey string `json:"operation_key" jsonschema:"stable unique key; reuse identical arguments on retry; never replace after an uncertain outcome"`
}
type UpdateInput struct {
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	Text      string `json:"text"`
}
type ScheduleInput struct {
	ChannelID    string `json:"channel_id"`
	Text         string `json:"text"`
	ThreadTS     string `json:"thread_ts,omitempty"`
	PostAt       int64  `json:"post_at" jsonschema:"Unix seconds, in the future and within 120 days"`
	OperationKey string `json:"operation_key"`
}
type CancelInput struct {
	ChannelID          string `json:"channel_id"`
	ScheduledMessageID string `json:"scheduled_message_id"`
}
type OperationOutput struct {
	receipts.Receipt
	Permalink string `json:"permalink,omitempty"`
	Replayed  bool   `json:"replayed"`
}
type OKOutput struct {
	OK bool `json:"ok"`
}
type AuthOutput struct {
	Connected    bool     `json:"connected"`
	TeamID       string   `json:"team_id"`
	UserID       string   `json:"user_id"`
	BotID        string   `json:"bot_id"`
	Capabilities []string `json:"capabilities"`
	Tools        []string `json:"tools"`
}
type MembersOutput struct {
	Members    []string `json:"members"`
	NextCursor string   `json:"next_cursor,omitempty"`
}
type PermalinkOutput struct {
	Permalink string `json:"permalink"`
}
type UserInput struct {
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"readable channel proving membership, unless an explicitly allowed DM recipient"`
}
type UserOutput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	RealName    string `json:"real_name"`
	IsBot       bool   `json:"is_bot"`
}
type SearchInput struct {
	ChannelID string `json:"channel_id"`
	Query     string `json:"query" jsonschema:"case-insensitive literal substring, not Slack search syntax"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	MaxPages  int    `json:"max_pages,omitempty" jsonschema:"1-5 pages, default 1"`
}
type SearchOutput struct {
	Messages     []slackclient.Message `json:"messages"`
	Partial      bool                  `json:"partial"`
	NextCursor   string                `json:"next_cursor,omitempty"`
	PagesScanned int                   `json:"pages_scanned"`
}
type ScheduledOutput struct {
	Messages   []slack.ScheduledMessage `json:"messages"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

func (t *Tools) authTest(ctx context.Context, _ EmptyInput) (AuthOutput, error) {
	a, e := t.identity(ctx)
	if e != nil {
		return AuthOutput{}, e
	}
	return AuthOutput{true, a.TeamID, a.UserID, a.BotID, t.Policy.Capabilities(), t.Policy.ToolNames()}, nil
}
func (t *Tools) info(ctx context.Context, in ChannelInput) (slackclient.ChannelMeta, error) {
	ch, _, e := t.authorize(ctx, in.ChannelID, false)
	if e != nil {
		return slackclient.ChannelMeta{}, e
	}
	return slackclient.ChannelMeta{ID: ch.ID, Name: ch.Name, IsMember: ch.IsMember, IsPrivate: ch.IsPrivate, IsChannel: ch.IsChannel, IsGroup: ch.IsGroup, IsIM: ch.IsIM, IsMpIM: ch.IsMpIM, IsArchived: ch.IsArchived, NumMembers: ch.NumMembers, Topic: ch.Topic.Value, Purpose: ch.Purpose.Value, Created: int64(ch.Created)}, nil
}
func (t *Tools) members(ctx context.Context, in PageInput) (MembersOutput, error) {
	limit, e := validate.Limit(in.Limit)
	if e != nil || validate.Cursor(in.Cursor) != nil {
		return MembersOutput{}, invalid()
	}
	if _, _, e = t.authorize(ctx, in.ChannelID, false); e != nil {
		return MembersOutput{}, e
	}
	var out MembersOutput
	e = slackclient.Call(ctx, func() error {
		var err error
		out.Members, out.NextCursor, err = t.Slack.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{ChannelID: in.ChannelID, Limit: limit, Cursor: in.Cursor})
		return err
	})
	if len(out.Members) > limit {
		return MembersOutput{}, upstream()
	}
	return out, e
}
func (t *Tools) permalink(ctx context.Context, in MessageInput) (PermalinkOutput, error) {
	if _, _, e := t.authorize(ctx, in.ChannelID, false); e != nil {
		return PermalinkOutput{}, e
	}
	if _, e := t.message(ctx, in.ChannelID, in.TS, in.ThreadTS); e != nil {
		return PermalinkOutput{}, e
	}
	link, e := t.link(ctx, in.ChannelID, in.TS)
	return PermalinkOutput{link}, e
}
func (t *Tools) link(ctx context.Context, ch, ts string) (string, error) {
	var link string
	e := slackclient.Call(ctx, func() error {
		var err error
		link, err = t.Slack.GetPermalinkContext(ctx, &slack.PermalinkParameters{Channel: ch, Ts: ts})
		return err
	})
	if e != nil {
		return "", e
	}
	u, e := url.Parse(link)
	if e != nil || u.Scheme != "https" || u.User != nil || !strings.HasSuffix(u.Hostname(), ".slack.com") || u.Port() != "" {
		return "", upstream()
	}
	return link, nil
}
func (t *Tools) userInfo(ctx context.Context, in UserInput) (UserOutput, error) {
	if !validID(in.UserID, "UW") {
		return UserOutput{}, invalid()
	}
	a, e := t.identity(ctx)
	if e != nil {
		return UserOutput{}, e
	}
	dmAllowed := t.Policy.UserAllowed(in.UserID) && (t.Policy.DMRead || t.Policy.DMWrite || t.Policy.MPIMRead || t.Policy.MPIMWrite)
	if !dmAllowed {
		if _, _, e = t.authorize(ctx, in.ChannelID, false); e != nil {
			return UserOutput{}, e
		}
		members, err := t.allMembers(ctx, in.ChannelID, maxPages)
		if err != nil {
			return UserOutput{}, err
		}
		found := false
		for _, u := range members {
			if u == in.UserID {
				found = true
			}
		}
		if !found {
			return UserOutput{}, denied()
		}
	}
	var u *slack.User
	e = slackclient.Call(ctx, func() error { var err error; u, err = t.Slack.GetUserInfoContext(ctx, in.UserID); return err })
	if e != nil {
		return UserOutput{}, e
	}
	if u == nil || u.ID != in.UserID || u.TeamID != a.TeamID {
		return UserOutput{}, denied()
	}
	return UserOutput{u.ID, u.Name, u.Profile.DisplayName, u.RealName, u.IsBot}, nil
}
func (t *Tools) search(ctx context.Context, in SearchInput) (SearchOutput, error) {
	if in.Query == "" || len(in.Query) > 256 || !utf8.ValidString(in.Query) {
		return SearchOutput{}, invalid()
	}
	if in.MaxPages == 0 {
		in.MaxPages = 1
	}
	if in.MaxPages < 1 || in.MaxPages > maxPages {
		return SearchOutput{}, invalid()
	}
	if _, e := validate.Limit(in.Limit); e != nil || validate.Cursor(in.Cursor) != nil {
		return SearchOutput{}, invalid()
	}
	out := SearchOutput{Messages: []slackclient.Message{}, Partial: true}
	cursor := in.Cursor
	for range in.MaxPages {
		page, e := t.History(ctx, HistoryInput{ChannelID: in.ChannelID, Limit: in.Limit, Cursor: cursor})
		if e != nil {
			return SearchOutput{}, e
		}
		out.PagesScanned++
		for _, m := range page.Messages {
			if strings.Contains(strings.ToLower(m.Text), strings.ToLower(in.Query)) {
				out.Messages = append(out.Messages, m)
			}
		}
		out.NextCursor = page.NextCursor
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || validate.Cursor(page.NextCursor) != nil {
			return SearchOutput{}, upstream()
		}
		cursor = page.NextCursor
	}
	// History-only search always remains partial: thread-only replies are absent.
	return out, nil
}

var operationKeyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validText(text string) bool {
	return strings.TrimSpace(text) != "" && len(text) <= 12000 && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}
func textOptions(text, thread string) []slack.MsgOption {
	opts := []slack.MsgOption{slack.MsgOptionText(text, true), slack.MsgOptionDisableMarkdown(), slack.MsgOptionParse(false), slack.MsgOptionLinkNames(false), slack.MsgOptionDisableLinkUnfurl(), slack.MsgOptionDisableMediaUnfurl()}
	if thread != "" {
		opts = append(opts, slack.MsgOptionTS(thread))
	}
	return opts
}
func (t *Tools) operation(a *slack.AuthTestResponse, tool, key string, input any, send func() (receipts.Receipt, error)) (OperationOutput, error) {
	if !operationKeyRE.MatchString(key) {
		return OperationOutput{}, invalid()
	}
	if t.Store == nil {
		return OperationOutput{}, receipts.ErrStore
	}
	data, e := json.Marshal(input)
	if e != nil {
		return OperationOutput{}, invalid()
	}
	digest := receipts.Digest(data)
	namespace := a.TeamID + ":" + a.BotID + ":" + tool
	receipt, replayed, e := t.Store.Begin(namespace, key, digest)
	if e != nil {
		return OperationOutput{}, e
	}
	if !replayed {
		receipt, e = send()
		if e != nil {
			return OperationOutput{}, receipts.ErrUncertain
		}
		if e = t.Store.Complete(namespace, key, digest, receipt); e != nil {
			return OperationOutput{}, receipts.ErrUncertain
		}
	}
	return OperationOutput{Receipt: receipt, Replayed: replayed}, nil
}
func (t *Tools) post(ctx context.Context, in PostInput) (OperationOutput, error) {
	if !validText(in.Text) || validate.Timestamp(in.ThreadTS) != nil || !operationKeyRE.MatchString(in.OperationKey) {
		return OperationOutput{}, invalid()
	}
	_, a, e := t.authorize(ctx, in.ChannelID, true)
	if e != nil {
		return OperationOutput{}, e
	}
	if e = t.rootThread(ctx, in.ChannelID, in.ThreadTS); e != nil {
		return OperationOutput{}, e
	}
	out, e := t.operation(a, "post", in.OperationKey, in, func() (receipts.Receipt, error) {
		var ch, ts string
		err := slackclient.Call(ctx, func() error {
			var e error
			ch, ts, e = t.Slack.PostMessageContext(ctx, in.ChannelID, textOptions(in.Text, in.ThreadTS)...)
			return e
		})
		if err != nil {
			return receipts.Receipt{}, err
		}
		if ch != in.ChannelID || validate.RequiredTimestamp(ts) != nil {
			return receipts.Receipt{}, upstream()
		}
		return receipts.Receipt{ChannelID: ch, TS: ts}, nil
	})
	if e == nil {
		out.Permalink, _ = t.link(ctx, out.ChannelID, out.TS)
	}
	return out, e
}
func (t *Tools) ownMessage(ctx context.Context, in MessageInput) error {
	_, a, e := t.authorize(ctx, in.ChannelID, true)
	if e != nil {
		return e
	}
	m, e := t.message(ctx, in.ChannelID, in.TS, in.ThreadTS)
	if e != nil {
		return e
	}
	if m.User != a.UserID || m.BotID != a.BotID {
		return denied()
	}
	return nil
}
func (t *Tools) update(ctx context.Context, in UpdateInput) (OperationOutput, error) {
	if !validText(in.Text) {
		return OperationOutput{}, invalid()
	}
	if e := t.ownMessage(ctx, MessageInput{in.ChannelID, in.TS, in.ThreadTS}); e != nil {
		return OperationOutput{}, e
	}
	var ch, ts string
	e := slackclient.Call(ctx, func() error {
		var err error
		ch, ts, _, err = t.Slack.UpdateMessageContext(ctx, in.ChannelID, in.TS, textOptions(in.Text, "")...)
		return err
	})
	if e != nil {
		return OperationOutput{}, e
	}
	if ch != in.ChannelID || ts != in.TS {
		return OperationOutput{}, upstream()
	}
	return OperationOutput{Receipt: receipts.Receipt{ChannelID: ch, TS: ts}}, nil
}
func (t *Tools) deleteMessage(ctx context.Context, in MessageInput) (OKOutput, error) {
	if e := t.ownMessage(ctx, in); e != nil {
		return OKOutput{}, e
	}
	var ch, ts string
	e := slackclient.Call(ctx, func() error {
		var err error
		ch, ts, err = t.Slack.DeleteMessageContext(ctx, in.ChannelID, in.TS)
		return err
	})
	if e != nil {
		return OKOutput{}, e
	}
	if ch != in.ChannelID || ts != in.TS {
		return OKOutput{}, upstream()
	}
	return OKOutput{true}, nil
}
func (t *Tools) schedule(ctx context.Context, in ScheduleInput) (OperationOutput, error) {
	now := time.Now().Unix()
	if !validText(in.Text) || validate.Timestamp(in.ThreadTS) != nil || in.PostAt <= now || in.PostAt > now+120*86400 || !operationKeyRE.MatchString(in.OperationKey) {
		return OperationOutput{}, invalid()
	}
	_, a, e := t.authorize(ctx, in.ChannelID, true)
	if e != nil {
		return OperationOutput{}, e
	}
	if e = t.rootThread(ctx, in.ChannelID, in.ThreadTS); e != nil {
		return OperationOutput{}, e
	}
	return t.operation(a, "schedule", in.OperationKey, in, func() (receipts.Receipt, error) {
		var ch, id string
		err := slackclient.Call(ctx, func() error {
			var e error
			ch, id, e = t.Slack.ScheduleMessageContext(ctx, in.ChannelID, jsonNumber(in.PostAt), textOptions(in.Text, in.ThreadTS)...)
			return e
		})
		if err != nil {
			return receipts.Receipt{}, err
		}
		if ch != in.ChannelID || !validID(id, "Q") {
			return receipts.Receipt{}, upstream()
		}
		return receipts.Receipt{ChannelID: ch, ScheduledMessageID: id}, nil
	})
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
func (t *Tools) scheduled(ctx context.Context, in PageInput) (ScheduledOutput, error) {
	limit, e := validate.Limit(in.Limit)
	if e != nil || validate.Cursor(in.Cursor) != nil {
		return ScheduledOutput{}, invalid()
	}
	if _, _, e = t.authorize(ctx, in.ChannelID, true); e != nil {
		return ScheduledOutput{}, e
	}
	var out ScheduledOutput
	e = slackclient.Call(ctx, func() error {
		var err error
		out.Messages, out.NextCursor, err = t.Slack.GetScheduledMessagesContext(ctx, &slack.GetScheduledMessagesParameters{Channel: in.ChannelID, Limit: limit, Cursor: in.Cursor})
		return err
	})
	if e != nil {
		return ScheduledOutput{}, e
	}
	if len(out.Messages) > limit {
		return ScheduledOutput{}, upstream()
	}
	for _, m := range out.Messages {
		if m.Channel != in.ChannelID {
			return ScheduledOutput{}, denied()
		}
	}
	return out, nil
}
func (t *Tools) cancelScheduled(ctx context.Context, in CancelInput) (OKOutput, error) {
	if !validID(in.ScheduledMessageID, "Q") {
		return OKOutput{}, invalid()
	}
	cursor := ""
	found := false
	for range maxPages {
		page, e := t.scheduled(ctx, PageInput{in.ChannelID, 100, cursor})
		if e != nil {
			return OKOutput{}, e
		}
		for _, m := range page.Messages {
			if m.ID == in.ScheduledMessageID {
				found = true
			}
		}
		if found || page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return OKOutput{}, denied()
		}
		cursor = page.NextCursor
	}
	if !found || t.Canceler == nil {
		return OKOutput{}, denied()
	}
	e := t.Canceler.CancelScheduled(ctx, in.ChannelID, in.ScheduledMessageID)
	return OKOutput{e == nil}, e
}
