package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// A fenced pending Web request is visible in the assignee's authorized
// inbox. Mirror that publication to an existing private chat only. A chat
// is never created here and no model turn is scheduled by this record.
const webHumanNoticeCandidates = `SELECT r.id,r.version,r.question,c.id,chat.id,c.workspace_id,c.web_user_id,a.admission_epoch,
 substr(encode(sha256(convert_to('web-human-notice:'||r.id::text||':'||r.version::text,'UTF8')),'hex'),1,32)::uuid
 FROM manager_human_requests r JOIN manager_sessions task ON task.id=r.session_id
 LEFT JOIN manager_conversations source ON source.id=task.conversation_id
 LEFT JOIN manager_task_spaces space ON space.id=task.task_space_id
 JOIN manager_conversations c ON c.workspace_id=r.workspace_id AND c.agent_id=r.agent_id
  AND c.lifecycle='active' AND c.web_user_id::text=r.recipient->>'external_id' AND c.web_group_id IS NULL
 JOIN manager_sessions chat ON chat.conversation_id=c.id AND chat.mode='chat' AND chat.lifecycle='active'
 JOIN platform_agents a ON a.id=r.agent_id AND a.enabled
 WHERE r.recipient->>'provider'='web' AND r.state='pending' AND r.fenced_at IS NOT NULL AND r.proactive_notice_enabled
 AND task.lifecycle='active' AND COALESCE(source.lifecycle,space.lifecycle)='active'
 AND task.active_turn_id IS NULL AND task.stop_revision=task.applied_stop_revision AND task.stop_revision=r.stop_revision
 AND manager_input_actor_allowed(r.authority_input_id)
 AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,true) IS NOT NULL AND ` + humanRecipientStillConfigured + `
 AND NOT EXISTS (SELECT FROM manager_inputs i
  WHERE i.id=substr(encode(sha256(convert_to('web-human-notice:'||r.id::text||':'||r.version::text,'UTF8')),'hex'),1,32)::uuid)
 ORDER BY r.created_at,r.id LIMIT 20 FOR UPDATE OF chat SKIP LOCKED`

type webHumanNotice struct {
	request, conversation, session, workspace, recipient, input string
	version, epoch                                              int64
	question                                                    service.HumanQuestion
}

func materializeWebHumanNotices(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, webHumanNoticeCandidates)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // Read errors checked before writes.
	var notices []webHumanNotice
	for rows.Next() {
		var n webHumanNotice
		var raw []byte
		if err = rows.Scan(&n.request, &n.version, &raw, &n.conversation, &n.session, &n.workspace, &n.recipient, &n.epoch, &n.input); err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &n.question); err != nil {
			return err
		}
		notices = append(notices, n)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, n := range notices {
		if err = insertWebHumanNotice(ctx, tx, n); err != nil {
			return err
		}
	}
	return nil
}

func insertWebHumanNotice(ctx context.Context, tx *sql.Tx, n webHumanNotice) error {
	disabled := false
	input := service.Input{ID: n.input, SessionID: n.session, SubmittedBy: n.recipient, Text: "[系统消息] 已发布" + humanNoticeText(service.HumanShortCode(n.request, n.version), n.question), SourceKind: "system", DeliveryMode: "queue", Status: "queued", TriggerAgentLoop: &disabled, CreatedAt: time.Now().UTC()}
	if err := tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, n.session).Scan(&input.Seq); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,source_kind,delivery_mode,trigger_agent_loop,context_state,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,'system','queue',false,'pending',$8) ON CONFLICT(id) DO NOTHING`, n.input, n.workspace, n.session, n.recipient, n.epoch, input.Seq, input.Text, input.CreatedAt)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	st := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: n.workspace, Session: n.session, Conversation: n.conversation}}
	return st.Event("input.updated", &input.ID, nil, service.InputFields(input))
}
