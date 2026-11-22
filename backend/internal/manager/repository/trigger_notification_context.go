package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// ProcessTriggerNotificationContexts never invents inbound messages. Delivery
// success and context admission are independent durable facts.
func (s *Store) ProcessTriggerNotificationContexts(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM manager_trigger_notifications WHERE status='sent' AND context_status='pending' AND COALESCE(target->>'provider','')<>'feishu' ORDER BY created_at,id LIMIT 20`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close preserves errors.
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.admitNotificationContext(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) admitNotificationContext(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = s.admitNotificationContextTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) admitNotificationContextTx(ctx context.Context, tx *sql.Tx, id string) error {
	var err error
	var provider, conversation, agent, workspace, groupLock string
	err = tx.QueryRowContext(ctx, `SELECT n.target->>'provider',c.id,c.agent_id,c.workspace_id,COALESCE(c.web_group_id::text,'') FROM manager_trigger_notifications n JOIN manager_conversations c ON c.id=n.conversation_id WHERE n.id=$1 AND n.status='sent' AND n.context_status='pending'`, id).Scan(&provider, &conversation, &agent, &workspace, &groupLock)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Web delivery and context serialize with normal group admission before
	// allocating a sequence or advancing its history boundary.
	if groupLock != "" {
		var locked string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE id=$1 FOR UPDATE`, groupLock).Scan(&locked); err != nil {
			return err
		}
	}
	// Lock employee and conversation before the session and context ledger.
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT admission_epoch FROM platform_agents WHERE id=$1 FOR SHARE`, agent).Scan(&epoch); err != nil {
		return err
	}
	var boundary int64
	var group *string
	if err = tx.QueryRowContext(ctx, `SELECT web_group_boundary,web_group_id FROM manager_conversations WHERE id=$1 FOR UPDATE`, conversation).Scan(&boundary, &group); err != nil {
		return err
	}
	var text string
	var reminder bool
	var message *string
	err = tx.QueryRowContext(ctx, `SELECT text,web_message_id,(to_jsonb(manager_trigger_notifications)->>'reminder_event_id') IS NOT NULL FROM manager_trigger_notifications WHERE id=$1 AND status='sent' AND context_status='pending' FOR UPDATE`, id).Scan(&text, &message, &reminder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return err
	}
	if !allowed {
		return finishNoticeContext(ctx, tx, id, "suppressed", "authorization_changed")
	}
	if provider != "web" {
		return finishNoticeContext(ctx, tx, id, "unavailable", "provider_history_anchor_unavailable")
	}
	if group == nil || message == nil {
		return finishNoticeContext(ctx, tx, id, "unavailable", "web_anchor_missing")
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `SELECT seq FROM manager_web_group_messages WHERE id=$1 AND group_id=$2 AND workspace_id=$3 AND author_kind='system' AND agent_id=$4`, *message, *group, workspace, agent).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finishNoticeContext(ctx, tx, id, "unavailable", "web_anchor_mismatch")
		}
		return err
	}
	if seq <= boundary {
		return finishNoticeContext(ctx, tx, id, "unavailable", "context_boundary_already_passed")
	}
	t := &sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: workspace, Conversation: conversation}}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat' AND lifecycle='active' FOR UPDATE`, conversation).Scan(&t.target.Session); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finishNoticeContext(ctx, tx, id, "unavailable", "group_chat_session_unavailable")
		}
		return err
	}
	if err = t.refresh(); err != nil {
		return err
	}
	if t.state.Session.Execution.QueuedInputCount >= 100 {
		return nil
	}
	limit, err := notificationHistoryLimit(ctx, tx, agent, "web")
	if err != nil {
		return err
	}
	if limit > 99-t.state.Session.Execution.QueuedInputCount {
		limit = 99 - t.state.Session.Execution.QueuedInputCount
	}
	items, err := notificationWebHistory(ctx, tx, *group, boundary, seq, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		body, e := webGroupInputText(ctx, tx, item)
		if e != nil {
			return e
		}
		kind := "user"
		if item.Kind != "user" {
			kind = "system"
		}
		if _, e = insertNotificationContext(t, id, body, kind, epoch); e != nil {
			return e
		}
	}
	kind := "task_result"
	if reminder {
		kind = "system"
	}
	result, err := insertNotificationContext(t, id, "[系统消息]"+strings.TrimPrefix(text, "[系统消息]"), kind, epoch)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_conversations SET web_group_boundary=$2 WHERE id=$1`, conversation, seq); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET context_status='admitted',context_error=NULL,context_input_id=$2,updated_at=now() WHERE id=$1`, id, result.ID); err != nil {
		return err
	}
	if err = t.SnapshotEvent(); err != nil {
		return err
	}
	return nil
}
func finishNoticeContext(ctx context.Context, tx *sql.Tx, id, status, code string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET context_status=$2,context_error=$3,updated_at=now() WHERE id=$1`, id, status, code); err != nil {
		return err
	}
	return nil
}
func notificationHistoryLimit(ctx context.Context, tx *sql.Tx, agent, provider string) (int, error) {
	var enabled bool
	var limit int
	err := tx.QueryRowContext(ctx, `SELECT (p.history->>'enabled')::boolean,(p.history->>'limit')::integer FROM platform_agents a CROSS JOIN LATERAL (SELECT platform_agent_usage_projection(a.usage_config,$2,NULL,0)->'history' AS history)p WHERE a.id=$1`, agent, provider).Scan(&enabled, &limit)
	if !enabled || limit < 0 {
		limit = 0
	}
	return limit, err
}
func notificationWebHistory(ctx context.Context, tx *sql.Tx, group string, boundary, seq int64, limit int) ([]service.WebGroupMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+webMessageColumns+` FROM(SELECT * FROM manager_web_group_messages WHERE group_id=$1 AND seq>$2 AND seq<$3 ORDER BY seq DESC LIMIT $4)recent ORDER BY seq`, group, boundary, seq, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close preserves errors.
	out := []service.WebGroupMessage{}
	for rows.Next() {
		v, e := scanWebMessage(rows)
		if e != nil {
			return nil, errors.Join(e, rows.Close())
		}
		out = append(out, v)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// Context rows have notification provenance, never a fabricated human/channel
// principal. The database prohibits this identity from triggering execution.
func insertNotificationContext(t *sessionTx, notice, body, kind string, epoch int64) (service.Input, error) {
	disabled := false
	v := service.Input{ID: service.NewID(), NotificationID: &notice, PrincipalType: "notification", SessionID: t.target.Session, Text: body, SourceKind: kind, DeliveryMode: "queue", Status: "queued", TriggerAgentLoop: &disabled, CreatedAt: time.Now().UTC()}
	err := t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, v.SessionID).Scan(&v.Seq)
	if err != nil {
		return v, err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,notification_id,admission_epoch,seq,text,source_kind,delivery_mode,trigger_agent_loop,context_state)VALUES($1,$2,$3,$4,$5,$6,$7,$8,'queue',false,'pending')`, v.ID, t.target.Workspace, v.SessionID, notice, epoch, v.Seq, v.Text, v.SourceKind)
	if err != nil {
		return v, err
	}
	return v, t.Event("input.updated", &v.ID, nil, service.InputFields(v))
}
