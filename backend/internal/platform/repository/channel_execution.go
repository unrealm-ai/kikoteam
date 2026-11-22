package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeChannelExecution(ctx context.Context, c service.ChannelExecution, employee string, running bool) error {
	if c.PrincipalID == "" || c.ConversationID == "" || c.Generation < 1 || running && c.InputID == "" {
		return service.Fail("channel_execution_forbidden", 403, "渠道运行身份无效")
	}
	var allowed bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM manager_conversations c JOIN platform_agents a ON a.workspace_id=c.workspace_id AND a.id=c.agent_id
 JOIN manager_channel_principals p ON p.workspace_id=c.workspace_id AND p.binding_id=c.channel_binding_id
 JOIN platform_channel_bindings b ON b.id=p.binding_id AND b.workspace_id=p.workspace_id AND b.agent_id=a.id
 WHERE c.id=$1 AND c.workspace_id=$2 AND p.id=$3 AND a.enabled AND c.lifecycle='active'
 AND ($4='' OR a.id::text=$4) AND b.provider=p.provider AND b.provider IN ('feishu','dingtalk','wecom') AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)
 AND (CASE WHEN $8 THEN $6 AND manager_human_chat_input_allowed(NULLIF($7,'')::uuid)
 ELSE manager_channel_actor_allowed(p.id,c.id,$5) END)
 AND (NOT $6 OR EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 WHERE i.id::text=$7 AND i.channel_principal_id=p.id AND i.channel_generation=$5 AND i.workspace_id=c.workspace_id
 AND ($8 OR manager_input_actor_allowed(i.id)) AND s.conversation_id=c.id AND s.agent_id=a.id AND s.lifecycle='active' AND i.status='started'
 AND i.turn_id=s.active_turn_id AND i.admission_epoch=a.admission_epoch AND s.stop_revision=s.applied_stop_revision)))`, c.ConversationID, t.workspace, c.PrincipalID, employee, c.Generation, running, c.InputID, c.ConfirmationOnly).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.Fail("channel_execution_forbidden", 403, "渠道使用权限已变更或撤销")
	}
	return nil
}
func (t *skillTx) AuthorizeChannelExecution(ctx context.Context, c service.ChannelExecution, employee string, running bool) error {
	if c.ConfirmationOnly {
		return service.Fail("channel_execution_forbidden", 403, "该轮仅允许处理人工确认")
	}
	return t.model.AuthorizeChannelExecution(ctx, c, employee, running)
}
func (t *pluginTx) AuthorizeChannelExecution(ctx context.Context, c service.ChannelExecution, employee string, running bool) error {
	if c.ConfirmationOnly {
		return service.Fail("channel_execution_forbidden", 403, "该轮仅允许处理人工确认")
	}
	return t.model.AuthorizeChannelExecution(ctx, c, employee, running)
}
func (t *packageTx) AuthorizeChannelExecution(ctx context.Context, c service.ChannelExecution, employee string, running bool) error {
	if c.ConfirmationOnly {
		return service.Fail("channel_execution_forbidden", 403, "该轮仅允许处理人工确认")
	}
	return t.model.AuthorizeChannelExecution(ctx, c, employee, running)
}
