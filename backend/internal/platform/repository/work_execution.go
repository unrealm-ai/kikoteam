package repository

import (
	"context"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *modelTx) AuthorizeWorkExecution(ctx context.Context, v service.WorkExecution, employee string) error {
	var allowed bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM manager_inputs i JOIN manager_work_assignments a ON a.id=i.work_assignment_id AND a.session_id=i.session_id
 JOIN manager_sessions s ON s.id=i.session_id
 WHERE i.id=$1 AND i.workspace_id=$2 AND a.id=$3 AND a.source_agent_id=$4 AND ($5='' OR s.agent_id::text=$5)
 AND i.status='started' AND i.turn_id=s.active_turn_id AND s.lifecycle='active'
 AND s.stop_revision=s.applied_stop_revision AND manager_work_input_allowed(i.id))`, v.InputID, t.workspace, v.AssignmentID, v.SourceAgentID, employee).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return service.Fail("work_execution_forbidden", 403, "事项环节或输入已失效")
	}
	return nil
}

func (t *skillTx) AuthorizeWorkExecution(ctx context.Context, v service.WorkExecution, employee string) error {
	return t.model.AuthorizeWorkExecution(ctx, v, employee)
}
func (t *pluginTx) AuthorizeWorkExecution(ctx context.Context, v service.WorkExecution, employee string) error {
	return t.model.AuthorizeWorkExecution(ctx, v, employee)
}
func (t *packageTx) AuthorizeWorkExecution(ctx context.Context, v service.WorkExecution, employee string) error {
	return t.model.AuthorizeWorkExecution(ctx, v, employee)
}
