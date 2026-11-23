package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Resolve both live worker authority and durable attachment grants in the same
// statement. A caller cannot choose a conversation, path, principal or turn.
const inputAttachmentScope = `WITH authority AS (
 SELECT s.id,s.workspace_id,s.agent_id,s.conversation_id,i.submitted_by,i.channel_principal_id,i.channel_generation
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
 ), grants AS (
 SELECT q.*,g.seq,g.attachments FROM authority q
 JOIN manager_inputs g ON g.session_id=q.id AND g.source_kind='user' AND g.status IN ('started','processed')
 AND g.submitted_by IS NOT DISTINCT FROM q.submitted_by
 AND g.channel_principal_id IS NOT DISTINCT FROM q.channel_principal_id
 AND g.channel_generation IS NOT DISTINCT FROM q.channel_generation
 AND manager_input_actor_allowed(g.id)
 )`

const attachmentByID = inputAttachmentScope + ` SELECT q.workspace_id,q.agent_id,q.conversation_id,`
const hasAttachmentGrant = `EXISTS (
 SELECT FROM grants g WHERE g.id=q.id AND g.attachments @> jsonb_build_array(jsonb_build_object('id',$5::text)))`

const inputAttachmentCandidateLimit = 32

// Select only exact stored references. The suspected ID is not a query input.
// Parse stored references defensively before UUID/integer casts. Both media
// branches enforce the same durable grant and current source gates as a read.
const inputAttachmentCandidates = inputAttachmentScope + `,
 refs AS MATERIALIZED (
 SELECT g.*,ref.pos,ref.value->>'id' AS ref_id
 FROM grants g CROSS JOIN LATERAL jsonb_array_elements(g.attachments) WITH ORDINALITY ref(value,pos)
 WHERE ref.value->>'id' ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(:([0-9]|[12][0-9]|3[01]))?$'
 ), allowed AS (
 SELECT q.ref_id,m.name,q.seq,q.pos
 FROM refs q JOIN manager_web_attachments m
 ON position(':' IN q.ref_id)=0 AND m.id=split_part(q.ref_id,':',1)::uuid
 AND m.workspace_id=q.workspace_id AND m.conversation_id=q.conversation_id
 UNION ALL
 SELECT q.ref_id,m.name,q.seq,q.pos
 FROM refs q JOIN manager_channel_inbox n
 ON n.id=split_part(q.ref_id,':',1)::uuid AND n.conversation_id=q.conversation_id AND n.status='accepted'
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_channel_media m ON m.inbox_id=n.id AND m.ordinal=NULLIF(split_part(q.ref_id,':',2),'')::integer
 WHERE ` + mediaAllowed + `
 ), distinct_refs AS (
 SELECT DISTINCT ON (ref_id) ref_id,name,seq,pos FROM allowed ORDER BY ref_id,seq DESC,pos
 ), bounded AS (
 SELECT * FROM distinct_refs ORDER BY seq DESC,pos,ref_id LIMIT $5
 )
 SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('id',ref_id,'name',name) ORDER BY seq DESC,pos,ref_id) FROM bounded),'[]'::jsonb)
 FROM authority`

func (s *Store) ReadInputAttachment(ctx context.Context, worker service.DelegationIdentity, attachmentID string) (service.InputAttachmentStatus, service.Conversation, error) {
	var r service.InputAttachmentStatus
	var c service.Conversation
	id, ordinal, ok := service.SplitInputAttachmentID(attachmentID)
	if !ok {
		return r, c, s.recoverInputAttachment(ctx, worker, service.Fail("invalid_argument", 400, "无效的附件 ID"))
	}
	args := []any{worker.SessionID, worker.EngineID, worker.AssignmentID, worker.Generation, attachmentID, id}
	query := attachmentByID + `m.name,CASE WHEN m.status='downloading' AND m.expires_at<=now() THEN 'failed' ELSE m.status END,
 CASE WHEN m.status='ready' THEN m.path ELSE '' END
 FROM authority q JOIN manager_web_attachments m ON m.id=$6 AND m.workspace_id=q.workspace_id AND m.conversation_id=q.conversation_id
 WHERE ` + hasAttachmentGrant
	if ordinal >= 0 {
		query = attachmentByID + `m.name,
 CASE WHEN m.path IS NOT NULL THEN 'ready' WHEN m.error_code IS NOT NULL THEN 'failed' ELSE 'downloading' END,
 COALESCE(m.path,'')
 FROM authority q JOIN manager_channel_inbox n ON n.id=$6 AND n.conversation_id=q.conversation_id AND n.status='accepted'
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_channel_media m ON m.inbox_id=n.id AND m.ordinal=$7 WHERE ` + mediaAllowed + ` AND ` + hasAttachmentGrant
		args = append(args, ordinal)
	}
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&c.WorkspaceID, &c.AgentID, &c.ID, &r.Name, &r.Status, &r.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return r, c, s.recoverInputAttachment(ctx, worker, service.Fail("attachment_forbidden", 403, "当前任务不可访问此附件"))
	}
	return r, c, err
}

func (s *Store) recoverInputAttachment(ctx context.Context, worker service.DelegationIdentity, cause error) error {
	var raw []byte
	err := s.DB.QueryRowContext(ctx, inputAttachmentCandidates,
		worker.SessionID, worker.EngineID, worker.AssignmentID, worker.Generation, inputAttachmentCandidateLimit+1).Scan(&raw)
	if err != nil {
		// Recovery is advisory. Preserve the original lookup error if authority
		// was stale or supplemental data could not be safely read.
		return cause
	}
	var candidates []service.InputAttachmentCandidate
	if err := json.Unmarshal(raw, &candidates); err != nil {
		return cause
	}
	truncated := len(candidates) > inputAttachmentCandidateLimit
	if truncated {
		candidates = candidates[:inputAttachmentCandidateLimit]
	}
	advice := "当前没有可用的附件引用；不要猜测 ID、改用其他文件或反复查询。"
	if len(candidates) != 0 {
		advice = "仅在可确认是用户指定的附件时，复制候选的完整 ID 重试。不要猜测或自行替换附件；不明确时请确认。"
	}
	return &service.InputAttachmentLookupError{Cause: cause, Recovery: service.InputAttachmentRecovery{
		Candidates: candidates, Truncated: truncated, Advice: advice,
	}}
}
