package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeTriggerExecution(ctx context.Context, g service.TriggerExecution, actor, employee string, running bool) error {
	if actor != "" || g.TriggerID == "" || g.EventID == "" || g.Version < 1 || running && g.InputID == "" || !running && employee == "" {
		return service.Fail("trigger_execution_forbidden", 403, "触发任务运行身份无效")
	}
	var allowed bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_triggers tr JOIN manager_trigger_events ev ON ev.trigger_id=tr.id AND ev.workspace_id=tr.workspace_id AND ev.agent_id=tr.agent_id JOIN platform_agents a ON a.id=tr.agent_id AND a.workspace_id=tr.workspace_id WHERE tr.id=$1 AND tr.workspace_id=$2 AND tr.enabled AND tr.version=$3 AND ev.id=$4 AND ev.binding_version=tr.version AND ev.status IN ('pending','accepted') AND a.enabled AND ($5='' OR a.id::text=$5) AND (NOT $6 OR EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id::text=$7 AND i.trigger_event_id=ev.id AND i.submitted_by IS NULL AND i.channel_principal_id IS NULL AND i.workspace_id=tr.workspace_id AND s.trigger_id=tr.id AND s.trigger_version=tr.version AND s.agent_id=tr.agent_id AND s.mode='task' AND s.lifecycle='active' AND i.status='started' AND i.turn_id=s.active_turn_id AND i.admission_epoch=a.admission_epoch AND s.stop_revision=s.applied_stop_revision AND manager_input_actor_allowed(i.id))))`, g.TriggerID, t.workspace, g.Version, g.EventID, employee, running, g.InputID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.Fail("trigger_execution_forbidden", 403, "触发绑定、版本或执行状态已变更")
	}
	return nil
}
func (t *skillTx) AuthorizeTriggerExecution(ctx context.Context, g service.TriggerExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTriggerExecution(ctx, g, actor, employee, running)
}
func (t *pluginTx) AuthorizeTriggerExecution(ctx context.Context, g service.TriggerExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTriggerExecution(ctx, g, actor, employee, running)
}
func (t *packageTx) AuthorizeTriggerExecution(ctx context.Context, g service.TriggerExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTriggerExecution(ctx, g, actor, employee, running)
}
