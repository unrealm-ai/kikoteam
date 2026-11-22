package service

import (
	"context"
	"strings"
	"time"
)

type ChannelObservedChat struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	ObservedAt  time.Time `json:"observed_at"`
}

func (s *ChannelService) ObservedChats(ctx context.Context, w, actor, agent, provider, q string) ([]ChannelObservedChat, error) {
	b, err := s.GetProvider(ctx, w, actor, agent, provider)
	if err != nil {
		return nil, err
	}
	if !b.IdentityConfirmed {
		return nil, Fail("channel_identity_unverified", 409, "渠道等待首条可信消息确认身份")
	}
	q = strings.TrimSpace(q)
	if len(q) > 512 || strings.ContainsAny(q, "\r\n\x00") {
		return nil, Fail("invalid_argument", 400, "查询无效")
	}
	store, ok := s.Store.(interface {
		ObservedChats(context.Context, ChannelBinding, string) ([]ChannelObservedChat, error)
	})
	if !ok {
		return nil, Fail("channel_unavailable", 503, "群记录不可用")
	}
	return store.ObservedChats(ctx, b, q)
}
