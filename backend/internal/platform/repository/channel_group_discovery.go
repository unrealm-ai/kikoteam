package repository

import (
	"context"
	"database/sql"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type ChannelGroupCache struct{ DB *sql.DB }

func (s *ChannelGroupCache) PublishGroups(ctx context.Context, b service.ChannelBinding, actor string, page service.ChannelGroupPage) (service.DiscoveredChannelGroupPage, error) {
	out := service.DiscoveredChannelGroupPage{Items: []service.DiscoveredChannelGroup{}, NextCursor: page.NextCursor}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	if e = tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active' AND role IN ('owner','admin') FOR SHARE`, b.WorkspaceID, actor).Scan(&id); e != nil {
		return out, service.Fail("workspace_forbidden", 403, "没有群发现权限")
	}
	if e = tx.QueryRowContext(ctx, `SELECT id FROM platform_channel_bindings WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 AND provider='feishu' AND enabled AND identity_epoch=$4 AND connection_generation=$5 AND config_revision=$6 AND external_tenant_id=$7 AND app_id=$8 FOR SHARE`, b.ID, b.WorkspaceID, b.AgentID, b.IdentityEpoch, b.Generation, b.Revision, b.TenantID, b.AppID).Scan(&id); e != nil {
		return out, service.Fail("channel_changed", 409, "连接已变化，请重新查询")
	}
	for _, v := range page.Items {
		candidate := service.NewID()
		e = tx.QueryRowContext(ctx, `INSERT INTO platform_channel_group_candidates(id,binding_id,identity_epoch,external_tenant_id,connection_generation,config_revision,external_chat_id,display_name,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()+interval '5 minutes') ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_chat_id) DO UPDATE SET connection_generation=EXCLUDED.connection_generation,config_revision=EXCLUDED.config_revision,display_name=EXCLUDED.display_name,confirmed_at=now(),expires_at=EXCLUDED.expires_at RETURNING id`, candidate, b.ID, b.IdentityEpoch, b.TenantID, b.Generation, b.Revision, v.ID, v.Name).Scan(&candidate)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, service.DiscoveredChannelGroup{ID: candidate, Name: v.Name, Provider: "feishu", Evidence: "provider_verified"})
	}
	return out, tx.Commit()
}
