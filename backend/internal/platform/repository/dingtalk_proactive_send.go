package repository

import (
	"context"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *ChannelStore) DingTalkProactiveSendPermissionMissing(ctx context.Context, b service.ChannelBinding) (bool, error) {
	if b.Provider != "dingtalk" || !b.Enabled {
		return false, nil
	}
	var missing bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM manager_dingtalk_proactive_send_observations o
 JOIN platform_channel_bindings b ON b.id=o.binding_id AND b.connection_generation=o.connection_generation
 WHERE b.id=$1 AND b.workspace_id=$2 AND b.agent_id=$3 AND b.enabled AND b.provider='dingtalk'
 AND b.connection_generation=$4 AND o.permission_missing)`, b.ID, b.WorkspaceID, b.AgentID, b.Generation).Scan(&missing)
	return missing, err
}
