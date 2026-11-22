package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeTaskSpaceExecution(ctx context.Context, g service.TaskSpaceExecution, actor, employee string, running bool) error {
	if g.TaskSpaceID == "" || actor == "" || !running && employee == "" || running && g.InputID == "" {
		return service.Fail("task_space_execution_forbidden", 403, "任务空间运行身份无效")
	}
	var allowed bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_task_spaces sp JOIN platform_agents a ON a.workspace_id=sp.workspace_id WHERE sp.id=$1 AND sp.workspace_id=$2 AND ($4='' OR a.id::text=$4) AND manager_task_space_actor_allowed(sp.id,$3,a.id) AND (NOT $5 OR EXISTS(SELECT 1 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id WHERE i.id::text=$6 AND i.submitted_by::text=$3 AND i.channel_principal_id IS NULL AND i.workspace_id=sp.workspace_id AND s.task_space_id=sp.id AND s.conversation_id IS NULL AND s.agent_id=a.id AND s.mode='task' AND s.lifecycle='active' AND manager_input_actor_allowed(i.id) AND i.status='started' AND i.turn_id=s.active_turn_id AND i.admission_epoch=a.admission_epoch AND s.stop_revision=s.applied_stop_revision)))`, g.TaskSpaceID, t.workspace, actor, employee, running, g.InputID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.Fail("task_space_execution_forbidden", 403, "任务空间授权或运行状态已变更")
	}
	return nil
}
func (t *skillTx) AuthorizeTaskSpaceExecution(ctx context.Context, g service.TaskSpaceExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTaskSpaceExecution(ctx, g, actor, employee, running)
}
func (t *pluginTx) AuthorizeTaskSpaceExecution(ctx context.Context, g service.TaskSpaceExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTaskSpaceExecution(ctx, g, actor, employee, running)
}
func (t *packageTx) AuthorizeTaskSpaceExecution(ctx context.Context, g service.TaskSpaceExecution, actor, employee string, running bool) error {
	return t.model.AuthorizeTaskSpaceExecution(ctx, g, actor, employee, running)
}
