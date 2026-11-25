package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const humanRejectionDeliveryPrefix = "human_rejection:"

// Record intent in the answer transaction, without taking a second Session
// lock: the recipient's reply may itself be running in the initiator's chat.
func queueHumanRejectionNotice(ctx context.Context, tx *sql.Tx, id string, version int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_human_rejection_notices(request_id,request_version)
 VALUES($1,$2) ON CONFLICT(request_id) DO NOTHING`, id, version)
	return err
}

// MaterializeHumanRejectionNotices publishes only to the original trusted
// initiator's private route. Request, recipient and arbitrary reply routing
// data are never used to select a destination.
func (s *Store) MaterializeHumanRejectionNotices(ctx context.Context) error {
	for range 20 {
		ok, err := s.materializeHumanRejectionNotice(ctx)
		if err != nil || !ok {
			return err
		}
	}
	return nil
}

func (s *Store) materializeHumanRejectionNotice(ctx context.Context) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	var id string
	err = tx.QueryRowContext(ctx, `SELECT request_id FROM manager_human_rejection_notices
 WHERE outcome IS NULL ORDER BY created_at,request_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	outcome, err := publishHumanRejectionNotice(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_human_rejection_notices SET outcome=$2 WHERE request_id=$1`, id, outcome); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Use the delegation's originating user input, or the task's actual initial
// input for a directly-created task. Never infer an initiator from a later
// supplement, an approver identity, a TaskSpace owner, or group membership.
const humanRejectionNoticeAuthority = `SELECT route.workspace_id,route.id,COALESCE(chat.id::text,''),
 COALESCE(box.id::text,''),COALESCE(box.binding_id::text,''),COALESCE(box.connection_generation,0),
 COALESCE(task.title,''),r.answer->>'text'
 FROM manager_human_rejection_notices delivery
 JOIN manager_human_requests r ON r.id=delivery.request_id AND r.version=delivery.request_version AND r.state='rejected'
 JOIN manager_sessions task ON task.id=r.session_id AND task.workspace_id=r.workspace_id AND task.agent_id=r.agent_id AND task.mode='task'
 JOIN manager_tasks k ON k.id=task.task_id AND k.workspace_id=task.workspace_id AND k.agent_id=task.agent_id
 JOIN manager_conversations route ON route.id=task.conversation_id AND route.id=k.conversation_id
  AND route.workspace_id=task.workspace_id AND route.agent_id=task.agent_id
 JOIN platform_agents a ON a.id=r.agent_id AND a.workspace_id=r.workspace_id AND a.enabled
 JOIN manager_inputs orig ON orig.id=CASE WHEN k.source_chat_session_id IS NOT NULL
   THEN k.source_chat_input_id ELSE task.initial_input_id END
  AND orig.session_id=COALESCE(k.source_chat_session_id,task.id) AND orig.workspace_id=route.workspace_id
 LEFT JOIN manager_sessions chat ON chat.conversation_id=route.id AND chat.workspace_id=route.workspace_id
  AND chat.agent_id=route.agent_id AND chat.mode='chat' AND chat.lifecycle='active'
  AND (k.source_chat_session_id IS NULL OR chat.id=k.source_chat_session_id)
 LEFT JOIN manager_channel_inbox box ON box.input_id=orig.id AND box.status='accepted'
  AND box.conversation_id=route.id AND box.principal_id=orig.channel_principal_id
  AND box.binding_id=route.channel_binding_id AND box.external_tenant_id=route.channel_tenant_id
  AND box.identity_epoch=route.channel_identity_epoch AND box.connection_generation=orig.channel_generation
  AND box.chat_type='direct' AND box.external_chat_id=route.external_chat_id AND box.thread_id=''
 WHERE delivery.request_id=$1 AND task.lifecycle='active' AND route.lifecycle='active'
 AND route.web_group_id IS NULL AND route.channel_thread_id=''
 AND orig.source_kind='user' AND orig.trigger_agent_loop AND orig.admission_epoch=a.admission_epoch
 AND manager_input_actor_allowed(orig.id)
 AND ((route.channel_binding_id IS NULL AND chat.id IS NOT NULL AND route.web_user_id=orig.submitted_by
  AND task.created_by=orig.submitted_by AND platform_agent_web_allowed(route.workspace_id,route.agent_id,orig.submitted_by))
 OR (route.channel_chat_type='direct' AND box.id IS NOT NULL AND task.channel_created_by=box.principal_id
  AND manager_human_channel_inbox_current(box.id,box.principal_id)))
 FOR SHARE OF route,a`

type humanRejectionTarget struct {
	workspace, conversation, chat, inbox, binding, title, reason string
	generation                                                   int64
}

func publishHumanRejectionNotice(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var dst humanRejectionTarget
	err := tx.QueryRowContext(ctx, humanRejectionNoticeAuthority, id).Scan(&dst.workspace, &dst.conversation, &dst.chat, &dst.inbox, &dst.binding, &dst.generation, &dst.title, &dst.reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "suppressed", nil
	}
	if err != nil {
		return "", err
	}
	body := humanRejectionText(dst.title, dst.reason)
	if dst.inbox != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text)
 VALUES(gen_random_uuid(),$1,$2,$3,$4,$5) ON CONFLICT(inbox_id,kind) DO NOTHING`,
			dst.inbox, dst.binding, dst.generation, humanRejectionDeliveryPrefix+id, body)
		return "channel_queued", err
	}
	return publishWebHumanRejectionNotice(ctx, tx, id, dst, body)
}

func publishWebHumanRejectionNotice(ctx context.Context, tx *sql.Tx, id string, dst humanRejectionTarget, body string) (string, error) {
	// Lock the actual member and the private chat before appending. If the
	// user lost workspace access or the chat was archived, publish nothing.
	var chat string
	err := tx.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_conversations c ON c.id=s.conversation_id
 JOIN platform_members m ON m.workspace_id=c.workspace_id AND m.user_id=c.web_user_id
 WHERE s.id=$1 AND c.id=$2 AND s.lifecycle='active' AND m.status='active'
 AND platform_agent_web_allowed(c.workspace_id,c.agent_id,c.web_user_id)
 FOR SHARE OF m FOR NO KEY UPDATE OF s`, dst.chat, dst.conversation).Scan(&chat)
	if errors.Is(err, sql.ErrNoRows) {
		return "suppressed", nil
	}
	if err != nil {
		return "", err
	}
	st := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: dst.workspace, Session: chat, Conversation: dst.conversation}}
	err = st.Event("agent.message", nil, nil, map[string]any{
		"source_kind": "human_rejection_notice", "message_id": id,
		"content": []map[string]string{{"type": "text", "text": body}},
	})
	return "web_published", err
}

func humanRejectionText(title, reason string) string {
	body := "人工确认已拒绝，任务没有继续执行。"
	if title = cleanNoticeText(title); title != "" {
		body = "任务「" + title + "」的人工确认已拒绝，任务没有继续执行。"
	}
	if reason = cleanNoticeText(reason); reason != "" {
		body += "\n拒绝原因：" + reason
	}
	return strings.TrimSpace(body)
}
