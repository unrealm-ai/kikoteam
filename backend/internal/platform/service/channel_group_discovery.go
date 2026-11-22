package service

import (
	"context"
	"strings"
)

type ChannelGroupProvider interface {
	List(context.Context, string, string, string, string) (ChannelGroupPage, error)
}
type DiscoveredChannelGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Evidence string `json:"evidence"`
}
type DiscoveredChannelGroupPage struct {
	Items      []DiscoveredChannelGroup `json:"items"`
	NextCursor string                   `json:"next_cursor"`
}
type ChannelGroupCache interface {
	PublishGroups(context.Context, ChannelBinding, string, ChannelGroupPage) (DiscoveredChannelGroupPage, error)
}
type ChannelGroupDiscovery struct {
	Channels *ChannelService
	Store    ChannelGroupCache
	Provider ChannelGroupProvider
}

func (s *ChannelGroupDiscovery) Discover(ctx context.Context, w, actor, agent, cursor string) (DiscoveredChannelGroupPage, error) {
	var out DiscoveredChannelGroupPage
	if len(cursor) > 2048 || strings.ContainsRune(cursor, 0) {
		return out, Fail("invalid_group_cursor", 400, "分页游标无效")
	}
	if s == nil || s.Channels == nil || s.Store == nil || s.Provider == nil {
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
	if e != nil {
		return out, e
	}
	return s.Store.PublishGroups(ctx, b, actor, page)
}
