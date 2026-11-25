package service

import (
	"context"
	"errors"
	"strings"
)

type ChannelGroupProvider interface {
	List(context.Context, string, string, string, string) (ChannelGroupPage, error)
}
type DiscoveredChannelGroup struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Provider       string          `json:"provider"`
	Evidence       string          `json:"evidence"`
	Selection      *GroupSelection `json:"selection,omitempty"`
	SelectionToken string          `json:"selection_token,omitempty"`
}
type DiscoveredChannelGroupPage struct {
	Items      []DiscoveredChannelGroup `json:"items"`
	Source     string                   `json:"source,omitempty"`
	NextCursor string                   `json:"next_cursor"`
}
type ChannelGroupCache interface {
	FilterFeishuGroups(context.Context, ChannelBinding, string, []ChannelGroupItem) ([]ChannelGroupItem, error)
	PublishGroups(context.Context, ChannelBinding, string, ChannelGroupPage) (DiscoveredChannelGroupPage, error)
}
type ChannelGroupDiscovery struct {
	Channels *ChannelService
	Store    ChannelGroupCache
	Provider ChannelGroupProvider
	Secret   []byte
}

func (s *ChannelGroupDiscovery) Discover(ctx context.Context, w, actor, agent, cursor string) (DiscoveredChannelGroupPage, error) {
	var out DiscoveredChannelGroupPage
	if len(cursor) > 2048 || strings.ContainsRune(cursor, 0) {
		return out, Fail("invalid_group_cursor", 400, "分页游标无效")
	}
	if s == nil || s.Channels == nil || s.Store == nil || s.Provider == nil || len(s.Secret) < 32 {
		return out, Fail("dependency_unavailable", 503, "群发现不可用")
	}
	b, e := s.Channels.GetProvider(ctx, w, actor, agent, "feishu")
	if e != nil {
		return out, e
	}
	if !b.Enabled || b.TenantID == "" || b.BotID == "" || !b.HasSecret {
		return out, Fail("channel_identity_unconfirmed", 409, "请先完成飞书连接")
	}
	secret, e := s.Channels.open(b)
	if e != nil {
		return out, e
	}
	page, e := s.Provider.List(ctx, b.AppID, secret, b.TenantID, cursor)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out.Items = []DiscoveredChannelGroup{}
	if e != nil {
		var providerErr *Error
		if errors.As(e, &providerErr) && (providerErr.Code == "group_tenant_mismatch" || providerErr.Code == "group_identity_invalid") {
			return out, e
		}
		// Revalidate the actor and binding even when the provider is down.
		if _, err := s.Store.FilterFeishuGroups(ctx, b, actor, nil); err != nil {
			return out, err
		}
		out.Source = "observed"
		return out, nil
	}
	items, e := s.Store.FilterFeishuGroups(ctx, b, actor, page.Items)
	if e != nil {
		return out, e
	}
	out.Source, out.NextCursor = "provider", page.NextCursor
	for _, item := range items {
		proof, err := s.signGroupProof(b, actor, item)
		if err != nil {
			return out, err
		}
		id := NewID()
		selected := feishuSelection(b, id, item.Name, item.ID)
		out.Items = append(out.Items, DiscoveredChannelGroup{ID: id, Name: selected.Name, Provider: "feishu", Evidence: "provider_verified", Selection: &selected, SelectionToken: proof})
	}
	return out, nil
}
