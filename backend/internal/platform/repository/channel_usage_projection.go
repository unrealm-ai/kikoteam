package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Caller owns clock -> agent -> binding lock order and commits policy, grants,
// Hub revisions and notifier watermark in the same transaction.
func RefreshChannelUsageTx(ctx context.Context, tx *sql.Tx, w, agent string, config []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE platform_channel_bindings b SET trigger=p.value->'trigger',history=p.value->'history',access=p.value->'access',rules_version=rules_version+1,config_revision=config_revision+1,applied_status='pending',applied_error='' FROM (SELECT id,platform_agent_usage_projection($3::jsonb,provider,id,identity_epoch) value FROM platform_channel_bindings WHERE workspace_id=$1 AND agent_id=$2) p WHERE b.id=p.id`, w, agent, config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_channel_config_clock SET revision=revision+1 WHERE id=true`)
	return err
}
func (t *channelTx) ApplyUsageProjection(ctx context.Context, b *service.ChannelBinding) error {
	var raw []byte
	if err := t.tx.QueryRowContext(ctx, `SELECT platform_agent_usage_projection(usage_config,$3,$4,$5) FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, b.WorkspaceID, b.AgentID, b.Provider, b.ID, b.IdentityEpoch).Scan(&raw); err != nil {
		return err
	}
	var projection struct {
		Access  service.ChannelAccess  `json:"access"`
		Trigger service.ChannelTrigger `json:"trigger"`
		History service.ChannelHistory `json:"history"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		return err
	}
	b.Access = projection.Access
	b.Trigger = projection.Trigger
	b.History = projection.History
	return nil
}

// Initialize only after the binding was successfully inserted. The same
// transaction updates the employee policy and Hub projection; a duplicate or
// unsuccessful registration cannot activate history. A globally paused policy
// has no effective channels, so selecting the new one does not revive others.
func (t *channelTx) initializeChannelHistory(ctx context.Context, b service.ChannelBinding) error {
	if b.History.Enabled {
		return nil
	}
	_, err := t.tx.ExecContext(ctx, `WITH changed AS (
 UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{history}',
   (usage_config->'history') || jsonb_build_object('enabled',true,'channels',
     (CASE WHEN (usage_config#>>'{history,enabled}')::boolean
       THEN usage_config#>'{history,channels}' ELSE '[]'::jsonb END) || jsonb_build_array($3::text))),
   usage_version=usage_version+1,version=version+1,updated_at=now()
 WHERE workspace_id=$1 AND id=$2
 RETURNING usage_config
 )
 UPDATE platform_channel_bindings SET history=platform_agent_usage_projection(changed.usage_config,$3,$4,$5)->'history'
 FROM changed WHERE platform_channel_bindings.id=$4`, b.WorkspaceID, b.AgentID, b.Provider, b.ID, b.IdentityEpoch)
	return err
}
