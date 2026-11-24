package repository

import (
	"context"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Use the current chat authority and the delivered notification as the read grant.
// A user's wider Task API permissions do not broaden what can be answered in the group.
const chatTriggerTaskResultQuery = `WITH destination AS (
 SELECT s.conversation_id,s.workspace_id,s.agent_id` + delegationFrom + `
 AND s.conversation_id=$7 AND ` + chatIsGroup + `
 AND EXISTS(SELECT FROM manager_inputs u WHERE u.id=$5 AND u.id=` + effectiveDelegatingInput + `
   AND u.source_kind='user' AND u.trigger_agent_loop AND u.admission_epoch=a.admission_epoch
   AND COALESCE(u.submitted_by,u.channel_principal_id)=$6 AND manager_input_actor_allowed(u.id))
)
 SELECT n.id,r.kind,n.result_status,left(n.text,1200),n.confirmed_at,n.created_at,t.started_at,
        COALESCE(t.finished_at,i.finished_at),COALESCE(t.error->>'code','')
 FROM destination d JOIN manager_trigger_notifications n ON n.conversation_id=d.conversation_id
 JOIN manager_triggers r ON r.id=n.trigger_id AND r.workspace_id=d.workspace_id AND r.agent_id=d.agent_id
 JOIN manager_tasks k ON k.id=n.task_id AND k.workspace_id=d.workspace_id AND k.agent_id=d.agent_id AND k.trigger_id=n.trigger_id
 JOIN manager_sessions s ON s.task_id=k.id AND s.workspace_id=d.workspace_id AND s.agent_id=d.agent_id AND s.mode='task'
 JOIN manager_inputs i ON i.id=n.source_input_id AND i.workspace_id=d.workspace_id AND i.session_id=s.id
 LEFT JOIN manager_turns t ON t.id=n.source_turn_id AND t.workspace_id=d.workspace_id AND t.session_id=s.id AND t.input_id=i.id
 WHERE n.status='sent' AND n.confirmed_at IS NOT NULL
 AND n.result_status IN ('completed','failed','canceled','interrupted','rejected')
 AND (n.source_turn_id IS NULL OR t.id IS NOT NULL)
 AND ` + ordinaryTaskResult + `
 AND ($8::uuid IS NULL OR n.id=$8)
 AND ($9::uuid IS NULL OR (n.created_at,n.id)<($10::timestamptz,$9))
 ORDER BY n.created_at DESC,n.id DESC LIMIT $11`

func (s *Store) ChatTriggerTaskResults(ctx context.Context, scope service.DelegationScope, f service.TriggerTaskResultFilter) ([]service.TriggerTaskResult, error) {
	id := scope.Identity
	rows, err := s.DB.QueryContext(ctx, chatTriggerTaskResultQuery, id.SessionID, id.EngineID, id.AssignmentID, id.Generation,
		scope.InputID, scope.ActorID, scope.ConversationID, nullID(f.NotificationID), nullID(f.BeforeID), f.BeforeTime, f.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked below.
	out := []service.TriggerTaskResult{}
	for rows.Next() {
		var result service.TriggerTaskResult
		var code string
		if err = rows.Scan(&result.NotificationID, &result.TriggerKind, &result.Outcome, &result.PublishedText, &result.PublishedAt,
			&result.CreatedAt, &result.StartedAt, &result.FinishedAt, &code); err != nil {
			return nil, err
		}
		result.FailureReason = triggerTaskFailureReason(result.Outcome, code)
		out = append(out, result)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

func triggerTaskFailureReason(outcome, code string) string {
	switch outcome {
	case "failed":
		switch code {
		case "event_persistence_failed":
			return "执行事件保存失败"
		case "work_mcp_unavailable":
			return "事项协作工具不可用"
		}
		return "本次执行失败，群可见记录没有更具体的原因"
	case "interrupted":
		return "本次执行中断"
	case "canceled":
		return "本次执行已取消"
	case "rejected":
		return "本次执行未获受理"
	default:
		return ""
	}
}
