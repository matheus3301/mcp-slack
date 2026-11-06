package mcpserver

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
	"github.com/matheus3301/mcp-slack/internal/validate"
	"github.com/slack-go/slack"
)

const maxPages = 5

var idRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{6,20}$`)

func validID(s, prefixes string) bool {
	return idRE.MatchString(s) && strings.ContainsRune(prefixes, rune(s[0]))
}
func denied() error {
	return slackclient.NewError(slackclient.CodePermissionDenied, "destination or resource is not authorized")
}
func invalid() error {
	return slackclient.NewError(slackclient.CodeInvalidRequest, "invalid or out-of-bounds arguments")
}
func upstream() error {
	return slackclient.NewError(slackclient.CodeUpstreamError, "unexpected Slack response")
}
func (t *Tools) identity(ctx context.Context) (*slack.AuthTestResponse, error) {
	if t.Slack == nil || t.Policy.TeamID == "" {
		return nil, denied()
	}
	var a *slack.AuthTestResponse
	err := slackclient.Call(ctx, func() error { var e error; a, e = t.Slack.AuthTestContext(ctx); return e })
	if err != nil {
		return nil, slackclient.Sanitize(err)
	}
	if a == nil || !validID(a.BotID, "B") || !validID(a.UserID, "UW") || a.TeamID != t.Policy.TeamID || (t.Policy.BotID != "" && a.BotID != t.Policy.BotID) {
		return nil, denied()
	}
	return a, nil
}
func (t *Tools) authorize(ctx context.Context, id string, write bool) (*slack.Channel, *slack.AuthTestResponse, error) {
	if !validID(id, "CGD") {
		return nil, nil, invalid()
	}
	allow := t.Allow
	if write {
		allow = t.Policy.Write
	}
	dmPossible := t.Policy.DMRead || t.Policy.MPIMRead
	if write {
		dmPossible = t.Policy.DMWrite || t.Policy.MPIMWrite
	}
	// Deny explicit non-destinations before touching Slack. G IDs can be MPIMs.
	if !allow.Allowed(id) && !(dmPossible && (id[0] == 'D' || id[0] == 'G')) {
		return nil, nil, denied()
	}
	a, e := t.identity(ctx)
	if e != nil {
		return nil, nil, e
	}
	var ch *slack.Channel
	e = slackclient.Call(ctx, func() error {
		var err error
		ch, err = t.Slack.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: id, IncludeNumMembers: true})
		return err
	})
	if e != nil {
		return nil, nil, slackclient.Sanitize(e)
	}
	if ch == nil || ch.ID != id {
		return nil, nil, denied()
	}
	if ch.ContextTeamID != "" && ch.ContextTeamID != a.TeamID {
		return nil, nil, denied()
	}
	if ch.IsShared || ch.IsExtShared || ch.IsOrgShared || ch.IsGlobalShared || ch.IsPendingExtShared || len(ch.PendingShared) > 0 {
		return nil, nil, denied()
	}
	for _, team := range append(append([]string{}, ch.SharedTeamIDs...), ch.ConnectedTeamIDs...) {
		if team != a.TeamID {
			return nil, nil, denied()
		}
	}
	if ch.IsIM || ch.IsMpIM {
		permitted := t.Policy.DMRead
		if ch.IsMpIM {
			permitted = t.Policy.MPIMRead
		}
		if write {
			permitted = t.Policy.DMWrite
			if ch.IsMpIM {
				permitted = t.Policy.MPIMWrite
			}
		}
		if !permitted || ch.IsIM && ch.IsMpIM {
			return nil, nil, denied()
		}
		if ch.IsIM && id[0] != 'D' || ch.IsMpIM && id[0] != 'G' {
			return nil, nil, denied()
		}
		members, err := t.allMembers(ctx, id, 1)
		if err != nil {
			return nil, nil, err
		}
		if !slices.Contains(members, a.UserID) || len(members) < 2 || len(members) > 9 || ch.IsIM && len(members) != 2 {
			return nil, nil, denied()
		}
		for _, u := range members {
			if u == a.UserID {
				continue
			}
			if !t.Policy.UserAllowed(u) {
				return nil, nil, denied()
			}
			if _, err = t.authorizedHuman(ctx, u, a.TeamID); err != nil {
				return nil, nil, err
			}
		}
	} else {
		if !allow.Allowed(id) || validate.ChannelID(id) != nil || !ch.IsChannel && !ch.IsGroup {
			return nil, nil, denied()
		}
		if !ch.IsMember {
			return nil, nil, slackclient.NewError(slackclient.CodeNotInChannel, "the bot is not a member of the requested channel")
		}
	}
	if write && (ch.IsArchived || ch.IsReadOnly) {
		return nil, nil, denied()
	}
	return ch, a, nil
}
func (t *Tools) allMembers(ctx context.Context, id string, pages int) ([]string, error) {
	var out []string
	cursor := ""
	for range pages {
		var members []string
		var next string
		e := slackclient.Call(ctx, func() error {
			var err error
			members, next, err = t.Slack.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{ChannelID: id, Limit: 100, Cursor: cursor})
			return err
		})
		if e != nil {
			return nil, slackclient.Sanitize(e)
		}
		if len(members) > 100 {
			return nil, upstream()
		}
		out = append(out, members...)
		if next == "" {
			return out, nil
		}
		if next == cursor || validate.Cursor(next) != nil {
			return nil, denied()
		}
		cursor = next
	}
	return nil, denied() // incomplete membership is never evidence of permission
}
func (t *Tools) authorizedHuman(ctx context.Context, id, team string) (*slack.User, error) {
	if !validID(id, "UW") {
		return nil, invalid()
	}
	var u *slack.User
	e := slackclient.Call(ctx, func() error { var err error; u, err = t.Slack.GetUserInfoContext(ctx, id); return err })
	if e != nil {
		return nil, slackclient.Sanitize(e)
	}
	if u == nil || u.ID != id || u.TeamID != team || u.IsBot || u.Deleted || u.IsStranger || u.IsAppUser {
		return nil, denied()
	}
	return u, nil
}

// message resolves the final resource in its authorized conversation. An
// optional parent timestamp supports replies without scanning unrelated pages.
func (t *Tools) message(ctx context.Context, ch, ts, thread string) (slack.Message, error) {
	if validate.RequiredTimestamp(ts) != nil || validate.Timestamp(thread) != nil {
		return slack.Message{}, invalid()
	}
	if thread != "" {
		cursor := ""
		for range maxPages {
			var messages []slack.Message
			var more bool
			var next string
			e := slackclient.Call(ctx, func() error {
				var err error
				messages, more, next, err = t.Slack.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: ch, Timestamp: thread, Oldest: ts, Latest: ts, Inclusive: true, Limit: 100, Cursor: cursor})
				return err
			})
			if e != nil {
				return slack.Message{}, slackclient.Sanitize(e)
			}
			for _, m := range messages {
				if m.Timestamp == ts && (m.ThreadTimestamp == thread || m.Timestamp == thread) {
					return m, nil
				}
			}
			if !more && next == "" {
				break
			}
			if next == "" || next == cursor || validate.Cursor(next) != nil {
				break
			}
			cursor = next
		}
	} else {
		var p *slack.GetConversationHistoryResponse
		e := slackclient.Call(ctx, func() error {
			var err error
			p, err = t.Slack.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: ch, Oldest: ts, Latest: ts, Inclusive: true, Limit: 1})
			return err
		})
		if e != nil {
			return slack.Message{}, slackclient.Sanitize(e)
		}
		if p != nil && len(p.Messages) == 1 && p.Messages[0].Timestamp == ts {
			return p.Messages[0], nil
		}
	}
	return slack.Message{}, denied()
}
func (t *Tools) rootThread(ctx context.Context, ch, ts string) error {
	if ts == "" {
		return nil
	}
	m, e := t.message(ctx, ch, ts, "")
	if e != nil {
		return e
	}
	if m.ThreadTimestamp != "" && m.ThreadTimestamp != ts {
		return denied()
	}
	return nil
}
