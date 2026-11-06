package mcpserver

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/slack-go/slack"
)

type ReactionInput struct {
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	Name      string `json:"name"`
}
type ReactionsOutput struct {
	Reactions []slack.ItemReaction `json:"reactions"`
}
type PinsOutput struct {
	Messages []slackclient.Message `json:"messages"`
}
type BookmarkInput struct {
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Link      string `json:"link"`
}
type BookmarkEditInput struct {
	ChannelID  string `json:"channel_id"`
	BookmarkID string `json:"bookmark_id"`
	Title      string `json:"title"`
	Link       string `json:"link"`
}
type BookmarkRemoveInput struct {
	ChannelID  string `json:"channel_id"`
	BookmarkID string `json:"bookmark_id"`
}
type Bookmark struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Title     string `json:"title"`
	Link      string `json:"link"`
}
type BookmarksOutput struct {
	Bookmarks []Bookmark `json:"bookmarks"`
}
type DMInput struct {
	Users []string `json:"users" jsonschema:"1-8 explicitly allowlisted human user IDs; no channel or conversation override"`
}
type DMOutput struct {
	ChannelID string `json:"channel_id"`
	IsMPIM    bool   `json:"is_mpim"`
}

var emojiRE = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,100}$`)
var bookmarkIDRE = regexp.MustCompile(`^Bk[A-Z0-9]{6,20}$`)

func (t *Tools) messageResource(ctx context.Context, in MessageInput, write bool) (slack.Message, error) {
	if _, _, e := t.authorize(ctx, in.ChannelID, write); e != nil {
		return slack.Message{}, e
	}
	return t.message(ctx, in.ChannelID, in.TS, in.ThreadTS)
}
func (t *Tools) reactions(ctx context.Context, in MessageInput) (ReactionsOutput, error) {
	if _, e := t.messageResource(ctx, in, false); e != nil {
		return ReactionsOutput{}, e
	}
	var r slack.ReactedItem
	e := slackclient.Call(ctx, func() error {
		var err error
		r, err = t.Slack.GetReactionsContext(ctx, slack.NewRefToMessage(in.ChannelID, in.TS), slack.GetReactionsParameters{Full: false})
		return err
	})
	if e != nil {
		return ReactionsOutput{}, e
	}
	if r.Type != "message" || r.Channel != in.ChannelID || r.Message == nil || r.Message.Timestamp != in.TS {
		return ReactionsOutput{}, denied()
	}
	return ReactionsOutput{r.Reactions}, nil
}
func (t *Tools) reaction(ctx context.Context, in ReactionInput, add bool) (OKOutput, error) {
	if !emojiRE.MatchString(in.Name) {
		return OKOutput{}, invalid()
	}
	if _, e := t.messageResource(ctx, MessageInput{in.ChannelID, in.TS, in.ThreadTS}, true); e != nil {
		return OKOutput{}, e
	}
	e := slackclient.Call(ctx, func() error {
		if add {
			return t.Slack.AddReactionContext(ctx, in.Name, slack.NewRefToMessage(in.ChannelID, in.TS))
		}
		return t.Slack.RemoveReactionContext(ctx, in.Name, slack.NewRefToMessage(in.ChannelID, in.TS))
	})
	return OKOutput{e == nil}, e
}
func (t *Tools) pins(ctx context.Context, in ChannelInput) (PinsOutput, error) {
	if _, _, e := t.authorize(ctx, in.ChannelID, false); e != nil {
		return PinsOutput{}, e
	}
	var items []slack.Item
	e := slackclient.Call(ctx, func() error { var err error; items, _, err = t.Slack.ListPinsContext(ctx, in.ChannelID); return err })
	if e != nil {
		return PinsOutput{}, e
	}
	if len(items) > 100 {
		return PinsOutput{}, upstream()
	}
	out := PinsOutput{Messages: []slackclient.Message{}}
	for _, i := range items {
		if i.Type != "message" {
			continue
		}
		if i.Channel != in.ChannelID || i.Message == nil {
			return PinsOutput{}, denied()
		}
		m := i.Message
		out.Messages = append(out.Messages, slackclient.Message{TS: m.Timestamp, ThreadTS: m.ThreadTimestamp, Text: m.Text, User: m.User, BotID: m.BotID})
	}
	return out, nil
}
func (t *Tools) pin(ctx context.Context, in MessageInput, add bool) (OKOutput, error) {
	if _, e := t.messageResource(ctx, in, true); e != nil {
		return OKOutput{}, e
	}
	e := slackclient.Call(ctx, func() error {
		if add {
			return t.Slack.AddPinContext(ctx, in.ChannelID, slack.NewRefToMessage(in.ChannelID, in.TS))
		}
		return t.Slack.RemovePinContext(ctx, in.ChannelID, slack.NewRefToMessage(in.ChannelID, in.TS))
	})
	return OKOutput{e == nil}, e
}
func bookmarkView(b slack.Bookmark) Bookmark { return Bookmark{b.ID, b.ChannelID, b.Title, b.Link} }
func (t *Tools) listBookmarks(ctx context.Context, ch string, write bool) (BookmarksOutput, error) {
	if _, _, e := t.authorize(ctx, ch, write); e != nil {
		return BookmarksOutput{}, e
	}
	var list []slack.Bookmark
	e := slackclient.Call(ctx, func() error { var err error; list, err = t.Slack.ListBookmarksContext(ctx, ch); return err })
	if e != nil {
		return BookmarksOutput{}, e
	}
	if len(list) > 100 {
		return BookmarksOutput{}, upstream()
	}
	out := BookmarksOutput{Bookmarks: []Bookmark{}}
	for _, b := range list {
		if b.ChannelID != ch {
			return BookmarksOutput{}, denied()
		}
		out.Bookmarks = append(out.Bookmarks, bookmarkView(b))
	}
	return out, nil
}
func validBookmark(title, link string) bool {
	if strings.TrimSpace(title) == "" || len(title) > 256 || !utf8.ValidString(title) || strings.ContainsAny(title, "\x00\r\n") || len(link) > 2048 {
		return false
	}
	u, e := url.Parse(link)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(link, "\x00\r\n")
}
func (t *Tools) addBookmark(ctx context.Context, in BookmarkInput) (Bookmark, error) {
	if !validBookmark(in.Title, in.Link) {
		return Bookmark{}, invalid()
	}
	if _, _, e := t.authorize(ctx, in.ChannelID, true); e != nil {
		return Bookmark{}, e
	}
	var b slack.Bookmark
	e := slackclient.Call(ctx, func() error {
		var err error
		b, err = t.Slack.AddBookmarkContext(ctx, in.ChannelID, slack.AddBookmarkParameters{Title: in.Title, Type: "link", Link: in.Link})
		return err
	})
	if e != nil {
		return Bookmark{}, e
	}
	if b.ChannelID != in.ChannelID {
		return Bookmark{}, denied()
	}
	return bookmarkView(b), nil
}
func (t *Tools) requireBookmark(ctx context.Context, ch, id string) error {
	if !bookmarkIDRE.MatchString(id) {
		return invalid()
	}
	list, e := t.listBookmarks(ctx, ch, true)
	if e != nil {
		return e
	}
	for _, b := range list.Bookmarks {
		if b.ID == id {
			return nil
		}
	}
	return denied()
}
func (t *Tools) editBookmark(ctx context.Context, in BookmarkEditInput) (Bookmark, error) {
	if !validBookmark(in.Title, in.Link) {
		return Bookmark{}, invalid()
	}
	if e := t.requireBookmark(ctx, in.ChannelID, in.BookmarkID); e != nil {
		return Bookmark{}, e
	}
	var b slack.Bookmark
	e := slackclient.Call(ctx, func() error {
		var err error
		b, err = t.Slack.EditBookmarkContext(ctx, in.ChannelID, in.BookmarkID, slack.EditBookmarkParameters{Title: &in.Title, Link: in.Link})
		return err
	})
	if e != nil {
		return Bookmark{}, e
	}
	if b.ChannelID != in.ChannelID || b.ID != in.BookmarkID {
		return Bookmark{}, denied()
	}
	return bookmarkView(b), nil
}
func (t *Tools) removeBookmark(ctx context.Context, in BookmarkRemoveInput) (OKOutput, error) {
	if e := t.requireBookmark(ctx, in.ChannelID, in.BookmarkID); e != nil {
		return OKOutput{}, e
	}
	e := slackclient.Call(ctx, func() error { return t.Slack.RemoveBookmarkContext(ctx, in.ChannelID, in.BookmarkID) })
	return OKOutput{e == nil}, e
}
func (t *Tools) openDM(ctx context.Context, in DMInput) (DMOutput, error) {
	if len(in.Users) < 1 || len(in.Users) > 8 {
		return DMOutput{}, invalid()
	}
	mpim := len(in.Users) > 1
	if !mpim && !t.Policy.DMWrite || mpim && !t.Policy.MPIMWrite {
		return DMOutput{}, denied()
	}
	a, e := t.identity(ctx)
	if e != nil {
		return DMOutput{}, e
	}
	users := append([]string{}, in.Users...)
	slices.Sort(users)
	for i, u := range users {
		if !t.Policy.UserAllowed(u) || u == a.UserID || i > 0 && users[i-1] == u {
			return DMOutput{}, denied()
		}
		if _, e = t.authorizedHuman(ctx, u, a.TeamID); e != nil {
			return DMOutput{}, e
		}
	}
	var ch *slack.Channel
	e = slackclient.Call(ctx, func() error {
		var err error
		ch, _, _, err = t.Slack.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: users, ReturnIM: true})
		return err
	})
	if e != nil {
		return DMOutput{}, e
	}
	if ch == nil {
		return DMOutput{}, upstream()
	}
	verified, _, e := t.authorize(ctx, ch.ID, true)
	if e != nil {
		return DMOutput{}, e
	}
	if verified.IsMpIM != mpim || !verified.IsIM && !mpim {
		return DMOutput{}, denied()
	}
	members, e := t.allMembers(ctx, ch.ID, 1)
	if e != nil {
		return DMOutput{}, e
	}
	expected := append(users, a.UserID)
	slices.Sort(expected)
	slices.Sort(members)
	if !slices.Equal(members, expected) {
		return DMOutput{}, denied()
	}
	return DMOutput{ch.ID, mpim}, nil
}
