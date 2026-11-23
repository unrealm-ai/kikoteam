package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Resolve both live worker authority and durable attachment grants in the same
// statement. A caller cannot choose a conversation, path, principal or turn.
const inputAttachmentScope = `WITH authority AS (
 SELECT s.id,s.workspace_id,s.agent_id,s.conversation_id
 FROM manager_sessions s
 JOIN manager_conversations c ON c.id=s.conversation_id AND c.workspace_id=s.workspace_id AND c.agent_id=s.agent_id
 JOIN platform_agents a ON a.id=s.agent_id AND a.workspace_id=s.workspace_id
 JOIN manager_turns t ON t.id=s.active_turn_id AND t.session_id=s.id
 JOIN manager_inputs i ON i.id=t.input_id AND i.session_id=s.id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4
 AND t.assignment_id=$3 AND t.generation=$4 AND t.status='running' AND i.status='started' AND i.turn_id=t.id
 AND s.mode='task' AND s.task_id IS NOT NULL AND s.lifecycle='active' AND c.lifecycle='active'
 AND s.stop_revision=s.applied_stop_revision AND a.enabled AND i.admission_epoch=a.admission_epoch
 AND manager_input_actor_allowed(i.id)
 AND EXISTS (SELECT 1 FROM manager_inputs g
 WHERE g.session_id=s.id AND g.source_kind='user' AND g.status IN ('started','processed')
 AND g.submitted_by IS NOT DISTINCT FROM i.submitted_by
 AND g.channel_principal_id IS NOT DISTINCT FROM i.channel_principal_id
 AND g.channel_generation IS NOT DISTINCT FROM i.channel_generation
 AND manager_input_actor_allowed(g.id)
 AND g.attachments @> jsonb_build_array(jsonb_build_object('id',$5::text)))
 ) SELECT q.workspace_id,q.agent_id,q.conversation_id,`

func (s *Store) ReadInputAttachment(ctx context.Context, worker service.DelegationIdentity, attachmentID string) (service.InputAttachmentStatus, service.Conversation, error) {
	var r service.InputAttachmentStatus
	var c service.Conversation
	id, ordinal, ok := service.SplitInputAttachmentID(attachmentID)
	if !ok {
		return r, c, service.Fail("invalid_argument", 400, "无效的附件 ID")
	}
	args := []any{worker.SessionID, worker.EngineID, worker.AssignmentID, worker.Generation, attachmentID, id}
	query := inputAttachmentScope + `m.name,CASE WHEN m.status='downloading' AND m.expires_at<=now() THEN 'failed' ELSE m.status END,
 CASE WHEN m.status='ready' THEN m.path ELSE '' END
 FROM authority q JOIN manager_web_attachments m ON m.id=$6 AND m.workspace_id=q.workspace_id AND m.conversation_id=q.conversation_id`
	if ordinal >= 0 {
		query = inputAttachmentScope + `m.name,
 CASE WHEN m.path IS NOT NULL THEN 'ready' WHEN m.error_code IS NOT NULL THEN 'failed' ELSE 'downloading' END,
 COALESCE(m.path,'')
 FROM authority q JOIN manager_channel_inbox n ON n.id=$6 AND n.conversation_id=q.conversation_id AND n.status='accepted'
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_channel_media m ON m.inbox_id=n.id AND m.ordinal=$7 WHERE ` + mediaAllowed
		args = append(args, ordinal)
	}
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&c.WorkspaceID, &c.AgentID, &c.ID, &r.Name, &r.Status, &r.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return service.InputAttachmentStatus{}, service.Conversation{}, service.Fail("attachment_forbidden", 403, "当前任务不可访问此附件")
	}
	return r, c, err
}
