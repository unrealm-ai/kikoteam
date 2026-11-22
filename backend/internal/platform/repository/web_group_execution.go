package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeWebGroupExecution(ctx context.Context, g service.WebGroupExecution, actor, employee string, running bool) error {
	if g.ConversationID == "" || actor == "" || running && g.InputID == "" {
		return service.Fail("web_group_execution_forbidden", 403, "群运行身份无效")
	}
	var allowed bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_conversations c JOIN platform_agents a ON a.id=c.agent_id AND a.workspace_id=c.workspace_id WHERE c.id=$1 AND c.workspace_id=$2 AND c.web_group_id IS NOT NULL AND c.channel_binding_id IS NULL AND c.lifecycle='active' AND a.enabled AND ($4='' OR a.id::text=$4) AND manager_web_group_actor_allowed(c.id,$3) AND (NOT $5 OR EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id::text=$6 AND i.submitted_by=$3 AND i.channel_principal_id IS NULL AND i.workspace_id=c.workspace_id AND manager_input_actor_allowed(i.id) AND s.conversation_id=c.id AND s.agent_id=a.id AND s.lifecycle='active' AND i.status='started' AND i.turn_id=s.active_turn_id AND i.admission_epoch=a.admission_epoch AND s.stop_revision=s.applied_stop_revision)))`, g.ConversationID, t.workspace, actor, employee, running, g.InputID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.Fail("web_group_execution_forbidden", 403, "群成员、员工权限或运行状态已变更")
	}
	return nil
}
func (t *skillTx) AuthorizeWebGroupExecution(ctx context.Context, g service.WebGroupExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeWebGroupExecution(ctx, g, actor, employee, running)
}
func (t *pluginTx) AuthorizeWebGroupExecution(ctx context.Context, g service.WebGroupExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeWebGroupExecution(ctx, g, actor, employee, running)
}
func (t *packageTx) AuthorizeWebGroupExecution(ctx context.Context, g service.WebGroupExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeWebGroupExecution(ctx, g, actor, employee, running)
}
