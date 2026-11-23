package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Claim consumes the unique attempt before returning it. Response loss, process
// exit and ambiguous external results intentionally never cause another attempt.
func (s *Store) ClaimChannelReaction(ctx context.Context) (*service.ChannelReactionClaim, error) {
	var out service.ChannelReactionClaim
	err := s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM manager_channel_inbox
 WHERE reaction_claim_token IS NULL AND reaction_ready_at>now()-interval '2 minutes'
 ORDER BY reaction_ready_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
 )
 UPDATE manager_channel_inbox n SET reaction_claim_token=gen_random_uuid()
 FROM candidate c WHERE n.id=c.id RETURNING n.id,n.reaction_claim_token`).Scan(&out.InboxID, &out.ClaimToken)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}

func (s *Store) AuthorizeChannelReaction(ctx context.Context, inbox, claim string) (*service.ChannelReactionTarget, error) {
	if !service.ValidID(inbox) || !service.ValidID(claim) {
		return nil, service.Fail("invalid_argument", 400, "接收反应标识无效")
	}
	var out service.ChannelReactionTarget
	err := s.DB.QueryRowContext(ctx, `SELECT b.id,b.provider,b.connection_generation,b.external_tenant_id,b.bot_open_id,n.external_chat_id,n.provider_message_id
 FROM manager_channel_inbox n
 JOIN manager_inputs i ON i.id=n.input_id
 JOIN manager_conversations c ON c.id=n.conversation_id
 JOIN manager_sessions ss ON ss.id=i.session_id AND ss.conversation_id=c.id
 JOIN platform_channel_bindings b ON b.id=n.binding_id AND c.channel_binding_id=b.id
 JOIN platform_agents a ON a.id=b.agent_id AND c.agent_id=a.id
 WHERE n.id=$1 AND n.reaction_claim_token=$2 AND n.reaction_ready_at>now()-interval '2 minutes'
 AND n.status='accepted' AND i.source_kind='user' AND i.trigger_agent_loop AND i.channel_principal_id=n.principal_id
 AND NOT EXISTS(SELECT FROM manager_human_chat_inputs h WHERE h.input_id=i.id)
 AND a.enabled AND c.lifecycle='active' AND ss.lifecycle='active'
 AND b.enabled AND (b.provider='feishu' OR (b.provider='dingtalk' AND b.identity_verified_generation=b.connection_generation))
 AND b.connection_generation=n.connection_generation AND b.identity_epoch=n.identity_epoch AND b.external_tenant_id=n.external_tenant_id AND b.bot_open_id<>''
 AND manager_channel_actor_allowed(n.principal_id,n.conversation_id,n.connection_generation)
 AND (b.provider='dingtalk' OR EXISTS (
   SELECT FROM platform_feishu_permission_checks p
   WHERE p.binding_id=b.id AND p.connection_generation=b.connection_generation
   AND p.diagnostics->>'scope_status'='pass'
   AND (p.diagnostics->>'checked_at')::timestamptz>now()-interval '10 minutes'
   AND (p.diagnostics->'permissions' @> '[{"scope":"im:message.reactions:write_only","status":"granted"}]'
        OR p.diagnostics->'permissions' @> '[{"scope":"im:message","status":"granted"}]')
 ))`, inbox, claim).Scan(&out.BindingID, &out.Provider, &out.Generation, &out.TenantID, &out.BotID, &out.ChatID, &out.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}
