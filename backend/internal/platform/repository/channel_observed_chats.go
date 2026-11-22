package repository

import (
	"context"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *ChannelStore) ObservedChats(ctx context.Context, b service.ChannelBinding, q string) ([]service.ChannelObservedChat, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.external_chat_id,g.display_name,g.observed_at FROM platform_channel_observed_chats g JOIN platform_channel_bindings b ON b.id=g.binding_id WHERE g.binding_id=$1 AND g.identity_epoch=b.identity_epoch AND g.external_tenant_id=b.external_tenant_id AND b.enabled AND b.connection_generation=$2 AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND ($3='' OR strpos(lower(g.display_name),lower($3))>0 OR strpos(g.external_chat_id,$3)>0) ORDER BY g.observed_at DESC,g.external_chat_id LIMIT 100`, b.ID, b.Generation, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration and final close are checked below.
	result := []service.ChannelObservedChat{}
	for rows.Next() {
		var chat service.ChannelObservedChat
		if err = rows.Scan(&chat.ID, &chat.DisplayName, &chat.ObservedAt); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		result = append(result, chat)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}
