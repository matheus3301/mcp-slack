package mcpserver

import (
	"context"

	"github.com/matheus3301/mcp-slack/internal/slackclient"
)

// fakeAPI is a scriptable slackclient.API for handler tests. It records calls
// and returns canned results/errors keyed by channel ID.
type fakeAPI struct {
	infoByID    map[string]*slackclient.ChannelMeta
	historyByID map[string]*slackclient.Page
	repliesByID map[string]*slackclient.Page
	err         error // if set, every method returns it

	infoCalls    []string
	historyCalls []slackclient.HistoryParams
	repliesCalls []slackclient.RepliesParams
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		infoByID:    map[string]*slackclient.ChannelMeta{},
		historyByID: map[string]*slackclient.Page{},
		repliesByID: map[string]*slackclient.Page{},
	}
}

func (f *fakeAPI) ConversationInfo(_ context.Context, id string) (*slackclient.ChannelMeta, error) {
	f.infoCalls = append(f.infoCalls, id)
	if f.err != nil {
		return nil, f.err
	}
	meta, ok := f.infoByID[id]
	if !ok {
		return nil, slackclient.NewError(slackclient.CodeChannelNotFound, "not found")
	}
	return meta, nil
}

func (f *fakeAPI) ConversationHistory(_ context.Context, p slackclient.HistoryParams) (*slackclient.Page, error) {
	f.historyCalls = append(f.historyCalls, p)
	if f.err != nil {
		return nil, f.err
	}
	page, ok := f.historyByID[p.ChannelID]
	if !ok {
		return &slackclient.Page{}, nil
	}
	return page, nil
}

func (f *fakeAPI) ConversationReplies(_ context.Context, p slackclient.RepliesParams) (*slackclient.Page, error) {
	f.repliesCalls = append(f.repliesCalls, p)
	if f.err != nil {
		return nil, f.err
	}
	page, ok := f.repliesByID[p.ChannelID]
	if !ok {
		return &slackclient.Page{}, nil
	}
	return page, nil
}
