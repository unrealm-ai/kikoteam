package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const delegationTables = ` FROM manager_sessions s JOIN manager_conversations c ON c.id=s.conversation_id JOIN platform_agents a ON a.id=s.agent_id JOIN manager_turns t ON t.id=s.active_turn_id JOIN manager_inputs i ON i.id=t.input_id`
const delegationFrom = delegationTables + delegationFilter
const delegationFilter = ` WHERE manager_input_actor_allowed(i.id) AND NOT EXISTS(SELECT FROM manager_human_chat_inputs h WHERE h.input_id=i.id) AND s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND s.mode='chat' AND s.lifecycle='active' AND c.lifecycle='active' AND a.enabled AND i.status='started' AND i.admission_epoch=a.admission_epoch AND (s.stop_revision=s.applied_stop_revision OR i.seq>s.stop_input_seq)`

const effectiveDelegatingInput = `COALESCE((SELECT u.id FROM manager_inputs u WHERE u.turn_id=t.id AND u.source_kind='user' AND u.trigger_agent_loop AND u.context_state='committed' ORDER BY u.seq DESC LIMIT 1),i.id)`

const chatIsGroup = `(c.web_group_id IS NOT NULL OR COALESCE(c.channel_chat_type='group',false))`

func (s *Store) DelegationSource(ctx context.Context, v service.DelegationIdentity) (service.DelegationScope, error) {
	out := service.DelegationScope{Identity: v}
	var principal *string
	var generation int64
	var webGroup bool
	err := s.DB.QueryRowContext(ctx, `SELECT s.workspace_id,COALESCE(i.submitted_by,i.channel_principal_id),s.conversation_id,s.agent_id,a.name,a.role,i.channel_principal_id,COALESCE(i.channel_generation,0),`+effectiveDelegatingInput+`,c.web_group_id IS NOT NULL,`+chatIsGroup+`,i.source_kind='task_report' AND NOT EXISTS(SELECT 1 FROM manager_inputs u WHERE u.turn_id=t.id AND u.source_kind='user' AND u.trigger_agent_loop AND u.context_state='committed')`+delegationFrom, v.SessionID, v.EngineID, v.AssignmentID, v.Generation).Scan(&out.WorkspaceID, &out.ActorID, &out.ConversationID, &out.AgentID, &out.Name, &out.Role, &principal, &generation, &out.InputID, &webGroup, &out.Group, &out.ReportOnly)
	if webGroup {
		out.WebGroup = &service.WebGroupExecution{ConversationID: out.ConversationID}
	}
	if principal != nil {
		out.Channel = &service.ChannelActor{ID: *principal, Generation: generation}
	}
	return out, visibleError(err)
}
func (s *Store) lockDelegationSource(ctx context.Context, tx *sql.Tx, target service.Target) error {
	v := target.Source
	var workspace, actor, conversation, inputID string
	err := tx.QueryRowContext(ctx, `SELECT s.workspace_id,COALESCE(i.submitted_by,i.channel_principal_id),s.conversation_id,`+effectiveDelegatingInput+delegationFrom+` AND (i.source_kind='user' OR EXISTS(SELECT 1 FROM manager_inputs u WHERE u.turn_id=t.id AND u.source_kind='user' AND u.context_state='committed')) FOR SHARE OF s`, v.SessionID, v.EngineID, v.AssignmentID, v.Generation).Scan(&workspace, &actor, &conversation, &inputID)
	if err != nil {
		return visibleError(err)
	}
	if workspace != target.Workspace || actor != target.Actor || conversation != target.Conversation {
		return service.Fail("resource_not_found", 404, "委派来源不匹配")
	}
	if target.SourceInputID != "" && target.SourceInputID != inputID {
		return service.Fail("delegation_input_changed", 409, "本次委派的来源输入已改变")
	}
	return nil
}
func (s *Store) checkDelegationTx(ctx context.Context, tx *sql.Tx, target service.Target) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_tasks t ON t.id=s.task_id WHERE s.id=$1 AND s.workspace_id=$2 AND s.mode='task' AND t.source_chat_session_id=$3`, target.Session, target.Workspace, target.Source.SessionID).Scan(&id)
	return visibleError(err)
}
func (s *Store) DelegatedSession(ctx context.Context, scope service.DelegationScope, id string) (service.Session, error) {
	return s.delegatedSessionByIdentity(ctx, scope, id, false)
}

func (s *Store) DelegatedTaskSession(ctx context.Context, scope service.DelegationScope, id string) (service.Session, error) {
	return s.delegatedSessionByIdentity(ctx, scope, id, true)
}

func (s *Store) delegatedSessionByIdentity(ctx context.Context, scope service.DelegationScope, id string, taskID bool) (service.Session, error) {
	var found string
	column := "s.id"
	if taskID {
		column = "t.id"
	}
	err := s.DB.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_tasks t ON t.id=s.task_id WHERE s.workspace_id=$1 AND s.conversation_id=$2 AND s.mode='task' AND t.source_chat_session_id=$4 AND `+column+`=$3`, scope.WorkspaceID, scope.ConversationID, id, scope.Identity.SessionID).Scan(&found)
	if err != nil {
		return service.Session{}, visibleError(err)
	}
	return s.Session(ctx, scope.WorkspaceID, scope.ActorID, found)
}
func (s *Store) LatestTurn(ctx context.Context, w, actor, session string) (*service.Turn, error) {
	if _, err := s.Session(ctx, w, actor, session); err != nil {
		return nil, err
	}
	v, err := scanTurn(s.DB.QueryRowContext(ctx, `SELECT `+turnColumns+` FROM manager_turns WHERE session_id=$1 ORDER BY turn_no DESC LIMIT 1`, session))
	var e *service.Error
	if errors.As(err, &e) && e.Status == 404 {
		return nil, nil
	}
	return &v, err
}
