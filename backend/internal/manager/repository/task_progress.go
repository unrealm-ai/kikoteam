package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// A shared sandbox in preparation is not proof of assignment to this task.
const taskPreparingEnvironment = `(s.assignment_id IS NOT NULL AND EXISTS (
 SELECT FROM manager_conversation_sandboxes sb WHERE sb.workspace_id=s.workspace_id
 AND sb.state='preparing' AND (sb.task_id=s.task_id OR sb.conversation_id=s.conversation_id)))`

const taskSettledStop = `(s.stop_revision>0 AND s.stop_revision=s.applied_stop_revision
 AND s.stop_input_seq=s.last_input_seq AND s.background_process_count=0 AND s.active_turn_id IS NULL)`

const taskVisibleTurnStatus = `CASE WHEN COALESCE((SELECT t.has_unknown_tool_results FROM manager_turns t WHERE t.session_id=s.id ORDER BY t.turn_no DESC LIMIT 1),false)
 THEN 'unknown' ELSE COALESCE((SELECT t.status FROM manager_turns t WHERE t.session_id=s.id ORDER BY t.turn_no DESC LIMIT 1),'') END`

const taskProgressHuman = `SELECT CASE r.state WHEN 'preparing' THEN 'preparing_confirmation'
 WHEN 'pending' THEN 'waiting_confirmation' WHEN 'blocked' THEN 'blocked'
 ELSE CASE WHEN r.resume_input_id IS NOT NULL THEN 'waiting_resume' ELSE 'unknown' END END,
 COALESCE(manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false),''),
 left(COALESCE(NULLIF(r.question->>'question',''),r.question->>'goal',''),200)
 FROM manager_human_requests r WHERE r.session_id=$1 AND r.workspace_id=$2 AND r.id::text=$3`

func projectTaskActions(ctx context.Context, tx *sql.Tx, workspace string, v service.Task, progress *service.TaskProgress) error {
	for _, act := range v.Execution.RequiredActions {
		switch act.Type {
		case "work_item":
			progress.Phase = "waiting_handoff"
		case "human_confirmation":
			var detail service.TaskConfirmationProgress
			err := tx.QueryRowContext(ctx, taskProgressHuman, v.SessionID, workspace, act.RequestID).Scan(&progress.Phase, &detail.Recipient, &detail.Question)
			if errors.Is(err, sql.ErrNoRows) {
				progress.Phase = "waiting_confirmation"
				return nil
			}
			if err != nil {
				return err
			}
			progress.Confirmation = &detail
			return nil
		case "confirmation":
			progress.Phase = "waiting_confirmation"
			return nil
		}
	}
	return nil
}

func taskHandoffProgress(ctx context.Context, tx *sql.Tx, workspace, actor, session string, turn *string) ([]service.TaskHandoffProgress, error) {
	// current_assignment_id points directly to the last hop, not to the first
	// recipient. Reuse the board visibility check before returning its name.
	rows, err := tx.QueryContext(ctx, `SELECT a.name,w.status,child.planning_state,child.lifecycle,child.execution,child.last_turn,child.preparing,child.stopped,child.waiting
 FROM manager_work_items w
 JOIN manager_work_assignments wa ON wa.id=w.current_assignment_id AND wa.work_item_id=w.id
 JOIN platform_agents a ON a.id=wa.target_agent_id AND a.workspace_id=w.workspace_id
 CROSS JOIN LATERAL (SELECT COALESCE((SELECT planning_state FROM manager_tasks WHERE id=s.task_id),'started') AS planning_state,
 s.lifecycle,`+taskExecutionSQL+` AS execution,`+taskVisibleTurnStatus+` AS last_turn,`+taskPreparingEnvironment+` AS preparing,
 COALESCE(`+taskSettledStop+`,false) AS stopped,
 EXISTS (SELECT FROM jsonb_array_elements(s.required_actions) q WHERE q->>'type' IN ('confirmation','human_confirmation')) AS waiting
 `+sessionFrom+` WHERE s.id=wa.session_id AND s.workspace_id=w.workspace_id) child
 WHERE w.workspace_id=$1 AND w.root_session_id=$3 AND
 (w.status IN ('working','awaiting_acceptance','blocked') OR w.root_turn_id=$4::uuid)
 AND `+boardVisible+` ORDER BY w.created_at DESC,w.id DESC LIMIT 20`, workspace, actor, session, turn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // The iteration error is checked below.
	var result []service.TaskHandoffProgress
	for rows.Next() {
		var v service.TaskHandoffProgress
		var status, planning, lifecycle, execution, last string
		var prepping, stopped, waiting bool
		if err = rows.Scan(&v.Assignee, &status, &planning, &lifecycle, &execution, &last, &prepping, &stopped, &waiting); err != nil {
			return nil, err
		}
		v.Phase = handoffPhase(status, planning, lifecycle, execution, last, prepping, stopped, waiting)
		result = append(result, v)
	}
	return result, rows.Err()
}

func handoffPhase(status, planning, lifecycle, execution, last string, prepping, stopped, waiting bool) string {
	switch status {
	case "awaiting_acceptance":
		return "waiting_acceptance"
	case "completed", "failed", "blocked", "cancelled":
		return status
	}
	if waiting && execution != "blocked" {
		return "waiting_confirmation"
	}
	if last == "unknown" && execution == "idle" {
		return "unknown"
	}
	return service.TaskProgressPhase(planning, lifecycle, execution, last, prepping, stopped)
}

func taskResultDeliveryProgress(ctx context.Context, tx *sql.Tx, workspace, actor, task string, turn *string) ([]service.TaskDeliveryProgress, error) {
	if turn == nil {
		return nil, nil
	}
	// A link sent on a normal user turn is not a result delivery. Include
	// only report replies (or report-turn detail cards) and trigger notices.
	rows, err := tx.QueryContext(ctx, `WITH report_scope AS (
 SELECT r.target_input_id,k.source_chat_input_id FROM manager_task_reports r
 JOIN manager_turns t ON t.id=r.source_turn_id JOIN manager_sessions s ON s.id=t.session_id
 JOIN manager_tasks k ON k.id=s.task_id WHERE r.workspace_id=$1 AND k.id=$3 AND t.id=$4
 AND EXISTS(SELECT 1 `+sessionFrom+` WHERE s.id=r.target_session_id AND s.workspace_id=$1 AND `+sessionRead(ctx, actor)+`)
 ), attempts AS (
 SELECT d.provider AS channel,n.conversation_id,d.status,d.confirmed_at,d.updated_at,d.id
 FROM report_scope r JOIN manager_turns reply ON reply.input_id=r.target_input_id
 JOIN manager_channel_inbox n ON n.input_id=r.source_chat_input_id
 JOIN manager_channel_deliveries d ON d.inbox_id=n.id AND d.kind='task_reply:'||reply.id::text
 UNION ALL
 SELECT d.provider,n.conversation_id,d.status,d.confirmed_at,d.updated_at,d.id
 FROM report_scope r JOIN manager_task_detail_deliveries x ON x.source_input_id=r.target_input_id
 JOIN manager_channel_deliveries d ON d.id=x.delivery_id
 JOIN manager_channel_inbox n ON n.id=d.inbox_id AND d.kind LIKE 'task_detail:body:%'
 UNION ALL
 SELECT n.target->>'provider',n.conversation_id,n.status,n.confirmed_at,n.updated_at,n.id FROM manager_trigger_notifications n
 JOIN manager_tasks k ON k.id=n.task_id AND k.workspace_id=$1 WHERE n.task_id=$3 AND n.source_turn_id=$4
 )
 SELECT channel,CASE
 WHEN bool_and(status='sent' AND confirmed_at IS NOT NULL) THEN 'confirmed'
 WHEN bool_or(status='sent' AND confirmed_at IS NOT NULL) THEN 'partial'
 WHEN bool_or(status IN ('pending','leased','sending')) THEN 'pending'
 WHEN bool_or(status IN ('accepted','unknown','sent')) AND NOT bool_or(status IN ('failed','cancelled')) THEN 'unconfirmed'
 WHEN bool_and(status IN ('failed','cancelled')) THEN 'failed'
 ELSE 'unconfirmed' END
 FROM (SELECT DISTINCT ON (channel,conversation_id) channel,status,confirmed_at FROM attempts
 WHERE channel IN ('web','feishu','dingtalk','wecom')
 ORDER BY channel,conversation_id,(status='sent' AND confirmed_at IS NOT NULL) DESC,updated_at DESC,id DESC) selected
 GROUP BY channel ORDER BY channel`, workspace, actor, task, *turn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // The iteration error is checked below.
	var out []service.TaskDeliveryProgress
	for rows.Next() {
		var d service.TaskDeliveryProgress
		if err = rows.Scan(&d.Channel, &d.Phase); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func visibleTaskBlock(phase string, exec service.Execution) string {
	if phase == "blocked" && exec.BlockReason != nil && *exec.BlockReason == "engine_recovery_pending" {
		return "engine_recovery_pending"
	}
	return phase
}

func authenticatedTaskProgress(ctx context.Context, tx *sql.Tx, workspace, actor string, v service.Task, preparing, stopped bool) (*service.TaskProgress, error) {
	var turnID *string
	var last string
	if v.LatestTurn != nil {
		turnID = &v.LatestTurn.ID
		last = v.LatestTurn.Status
	}
	phase := service.TaskProgressPhase(v.PlanningState, v.Lifecycle, v.Execution.State, last, preparing, stopped)
	if v.Execution.State == "idle" && phase != "stopped" && v.Execution.BackgroundProcessCount != nil && *v.Execution.BackgroundProcessCount > 0 {
		phase = "background_active"
	}
	if phase == last && v.LatestTurn != nil && v.LatestTurn.HasUnknownToolResults {
		phase = "unknown"
	}
	p := &service.TaskProgress{Phase: visibleTaskBlock(phase, v.Execution), Assignee: v.Agent.Name}
	if v.Lifecycle == "active" && v.PlanningState == "started" && phase != "stopping" {
		if err := projectTaskActions(ctx, tx, workspace, v, p); err != nil {
			return nil, err
		}
	}
	var err error
	p.Handoffs, err = taskHandoffProgress(ctx, tx, workspace, actor, v.SessionID, turnID)
	if err != nil {
		return nil, err
	}
	// A handoff boundary can end its originating turn successfully while
	// the overall work subsequently fails. Do not call that task completed.
	if p.Phase == "completed" {
		for _, handoff := range p.Handoffs {
			if handoff.Phase == "failed" {
				p.Phase = "handoff_failed"
				break
			}
		}
	}
	p.Delivery, err = taskResultDeliveryProgress(ctx, tx, workspace, actor, v.TaskID, turnID)
	return p, err
}

// Public links preserve their existing grant projection: no employee or
// recipient labels, questions, assignment details or unrelated receipts.
func publicTaskProgress(ctx context.Context, tx *sql.Tx, g service.TaskDetailGrant, st service.SessionState, status string) (*service.PublicTaskProgress, error) {
	phase := visibleTaskBlock(status, st.Session.Execution)
	if status == "queued" {
		var preparing bool
		if err := tx.QueryRowContext(ctx, `SELECT `+taskPreparingEnvironment+` FROM manager_sessions s WHERE s.id=$1`, g.SessionID).Scan(&preparing); err != nil {
			return nil, err
		}
		if preparing {
			phase = "preparing_environment"
		}
	}
	if status == "waiting_for_action" {
		for _, act := range st.Session.Execution.RequiredActions {
			switch act.Type {
			case "work_item":
				phase = "waiting_handoff"
			case "human_confirmation", "confirmation":
				phase = "waiting_confirmation"
				if ownTaskDetail(g) && act.Type == "human_confirmation" {
					err := tx.QueryRowContext(ctx, `SELECT CASE state WHEN 'preparing' THEN 'preparing_confirmation'
 WHEN 'blocked' THEN 'blocked' ELSE CASE WHEN resume_input_id IS NOT NULL THEN 'waiting_resume' ELSE 'waiting_confirmation' END END
 FROM manager_human_requests WHERE session_id=$1 AND id::text=$2 AND workspace_id=$3`, g.SessionID, act.RequestID, g.WorkspaceID).Scan(&phase)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, err
					}
				}
				return &service.PublicTaskProgress{Phase: phase}, nil
			}
		}
	}
	if status == "idle" && g.TriggerNotificationID != "" {
		var last string
		err := tx.QueryRowContext(ctx, `SELECT `+taskVisibleTurnStatus+` FROM manager_sessions s WHERE s.id=$1 AND s.workspace_id=$2`, g.SessionID, g.WorkspaceID).Scan(&last)
		if err != nil {
			return nil, err
		}
		if last == "unknown" {
			phase = last
		} else if last != "" {
			phase = service.TaskProgressPhase("started", "active", status, last, false, false)
		}
	}
	if status == "idle" && ownTaskDetail(g) {
		var last string
		err := tx.QueryRowContext(ctx, `SELECT CASE WHEN t.has_unknown_tool_results THEN 'unknown'
 WHEN t.status='completed' AND EXISTS(SELECT FROM manager_work_items w WHERE w.root_session_id=t.session_id AND w.root_turn_id=t.id AND w.status='failed') THEN 'handoff_failed'
 ELSE t.status END FROM manager_turns t
 JOIN manager_inputs i ON i.id=t.input_id WHERE t.session_id=$1 AND i.channel_principal_id=$2 AND i.channel_generation=$3
 AND manager_input_actor_allowed(i.id)
 AND t.turn_no=(SELECT max(turn_no) FROM manager_turns WHERE session_id=$1)`, g.SessionID, g.SubjectID, g.SubjectGeneration).Scan(&last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if last == "unknown" || last == "handoff_failed" {
			phase = last
		} else {
			phase = service.TaskProgressPhase("started", "active", status, last, false, false)
		}
	}
	return &service.PublicTaskProgress{Phase: phase}, nil
}
