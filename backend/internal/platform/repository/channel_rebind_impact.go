package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *channelTx) ReadRebindImpact(ctx context.Context, binding service.ChannelBinding) (*service.ChannelRebindImpact, error) {
	var raw []byte
	err := t.tx.QueryRowContext(ctx, `WITH current_ref AS (
 SELECT jsonb_build_array(jsonb_build_object('binding_id',$3::text,'identity_epoch',$4::bigint)) AS marker
), packages AS (
 SELECT p.name,bool_or((scope.rule->'deny' @> marker) IS TRUE) AS has_deny
 FROM platform_capability_packages p CROSS JOIN current_ref
 CROSS JOIN LATERAL (VALUES (COALESCE(p.conversation_scope->'private',p.scope)),(p.conversation_scope->'group')) scope(rule)
 WHERE p.workspace_id=$1 AND p.agent_id=$2 AND (
   scope.rule->'allow' @> marker OR scope.rule->'deny' @> marker)
 GROUP BY p.id,p.name
), sharing AS (
 SELECT p.name FROM platform_group_sharing_policies p CROSS JOIN current_ref
 WHERE p.workspace_id=$1 AND p.agent_id=$2 AND (p.sources @> marker OR p.targets @> marker)
), notifications AS (
 SELECT m.name FROM manager_triggers m WHERE m.workspace_id=$1 AND m.agent_id=$2 AND EXISTS (
   SELECT 1 FROM jsonb_array_elements(m.notification_targets) ref
   JOIN manager_conversations c ON c.id::text=ref->>'conversation_id' AND c.workspace_id=m.workspace_id AND c.agent_id=m.agent_id
   WHERE c.channel_binding_id::text=$3 AND c.channel_identity_epoch=$4)
)
SELECT jsonb_build_object(
 'package_names',COALESCE((SELECT jsonb_agg(name ORDER BY name) FROM packages),'[]'::jsonb),
 'package_deny_names',COALESCE((SELECT jsonb_agg(name ORDER BY name) FROM packages WHERE has_deny),'[]'::jsonb),
 'sharing_names',COALESCE((SELECT jsonb_agg(name ORDER BY name) FROM sharing),'[]'::jsonb),
 'notification_names',COALESCE((SELECT jsonb_agg(name ORDER BY name) FROM notifications),'[]'::jsonb))`,
		binding.WorkspaceID, binding.AgentID, binding.ID, binding.IdentityEpoch).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var impact service.ChannelRebindImpact
	if err := json.Unmarshal(raw, &impact); err != nil {
		return nil, err
	}
	access := binding.Access
	impact.AllowEntries = len(access.AllowIDs) + len(access.AllowDepartmentRules) + len(access.AllowChatIDs)
	impact.DenyEntries = len(access.DenyIDs) + len(access.DenyDepartmentRules) + len(access.DenyChatIDs)
	return &impact, nil
}
