package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
)

// Jobs are immutable authorization snapshots. A changed history boundary causes
// a fresh lease/job, never merges an old fetch into a newer conversation.
func (s *Store) ClaimNotificationContexts(ctx context.Context, limit int) ([]service.ChannelHistoryJob, error) {
	out := []service.ChannelHistoryJob{}
	if limit < 1 || limit > 20 {
		return out, service.Fail("invalid_argument", 400, "批量无效")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM manager_trigger_notifications WHERE status='sent' AND context_status='pending' AND target->>'provider'='feishu' AND provider_message_id IS NOT NULL AND (context_lease_until IS NULL OR context_lease_until<now()) ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked below.
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	for _, id := range ids {
		j, e := s.claimNotificationContext(ctx, id)
		if e != nil {
			return out, e
		}
		if j != nil {
			out = append(out, *j)
		}
	}
	return out, nil
}
func (s *Store) lockNotificationConversation(ctx context.Context, tx *sql.Tx, id string) (*sessionTx, int64, error) {
	t := &sessionTx{ctx: ctx, tx: tx, store: s}
	var agent string
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT c.workspace_id,c.id,c.agent_id FROM manager_trigger_notifications n JOIN manager_conversations c ON c.id=n.conversation_id WHERE n.id=$1`, id).Scan(&t.target.Workspace, &t.target.Conversation, &agent)
	if err != nil {
		return nil, 0, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT admission_epoch FROM platform_agents WHERE id=$1 FOR SHARE`, agent).Scan(&epoch); err != nil {
		return nil, 0, err
	}
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_conversations WHERE id=$1 FOR UPDATE`, t.target.Conversation).Scan(&locked); err != nil {
		return nil, 0, err
	}
	return t, epoch, nil
}
func (s *Store) claimNotificationContext(ctx context.Context, id string) (*service.ChannelHistoryJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	t, _, err := s.lockNotificationConversation(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var j service.ChannelHistoryJob
	err = tx.QueryRowContext(ctx, `SELECT n.id,b.id,b.connection_generation,b.config_revision,b.rules_version,b.external_tenant_id,c.external_chat_id,c.channel_thread_id,n.provider_message_id,COALESCE(n.provider_created_at_ms,0),n.text,n.content_format,COALESCE(h.trigger_message_id,''),COALESCE(h.trigger_created_at_ms,0) FROM manager_trigger_notifications n JOIN manager_conversations c ON c.id=n.conversation_id JOIN platform_channel_bindings b ON b.id=c.channel_binding_id LEFT JOIN manager_channel_history_boundaries h ON h.conversation_id=c.id WHERE n.id=$1 AND n.status='sent' AND n.context_status='pending' AND b.provider='feishu' AND (n.context_lease_until IS NULL OR n.context_lease_until<now()) FOR UPDATE OF n`, id).Scan(&j.ID, &j.BindingID, &j.ConnectionGeneration, &j.ConfigRevision, &j.RulesVersion, &j.ExternalTenantID, &j.ChatID, &j.ThreadID, &j.TriggerMessageID, &j.TriggerCreatedAtMS, &j.TriggerText, &j.TriggerFormat, &j.PreviousTriggerMessageID, &j.PreviousTriggerCreatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, finishFeishuNoticeContext(ctx, tx, id, "suppressed", "authorization_changed")
	}
	var agent string
	if err = tx.QueryRowContext(ctx, `SELECT agent_id FROM manager_conversations WHERE id=$1`, t.target.Conversation).Scan(&agent); err != nil {
		return nil, err
	}
	j.Limit, err = notificationHistoryLimit(ctx, tx, agent, "feishu")
	if err != nil {
		return nil, err
	}
	if j.Limit > 99 {
		j.Limit = 99
	}
	j.LeaseToken = service.NewID()
	j.Strategy = "since_last_trigger"
	j.Allowed = true
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET context_job=$2,context_lease_token=$3,context_lease_until=now()+interval '30 seconds' WHERE id=$1`, id, raw, j.LeaseToken); err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}
func (s *Store) notificationContextJob(ctx context.Context, tx *sql.Tx, id, token string) (service.ChannelHistoryJob, error) {
	var j service.ChannelHistoryJob
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT context_job FROM manager_trigger_notifications WHERE id=$1 AND context_lease_token=$2 AND context_lease_until>now() AND status='sent' AND context_status='pending' FOR UPDATE`, id, token).Scan(&raw)
	if err != nil {
		return j, visibleError(err)
	}
	if err = json.Unmarshal(raw, &j); err != nil {
		return j, err
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return j, err
	}
	if !allowed {
		return j, service.Fail("conflict", 409, "通知授权已变化")
	}
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT b.enabled AND b.connection_generation=$2 AND b.config_revision=$3 AND b.rules_version=$4 AND b.external_tenant_id=$5 AND COALESCE(h.trigger_message_id,'')=$6 AND COALESCE(h.trigger_created_at_ms,0)=$7 FROM manager_trigger_notifications n JOIN manager_conversations c ON c.id=n.conversation_id JOIN platform_channel_bindings b ON b.id=c.channel_binding_id LEFT JOIN manager_channel_history_boundaries h ON h.conversation_id=c.id WHERE n.id=$1`, id, j.ConnectionGeneration, j.ConfigRevision, j.RulesVersion, j.ExternalTenantID, j.PreviousTriggerMessageID, j.PreviousTriggerCreatedAtMS).Scan(&current)
	if err != nil {
		return j, err
	}
	if !current {
		return j, service.Fail("conflict", 409, "通知历史边界已变化")
	}
	return j, nil
}
func (s *Store) AuthorizeNotificationContext(ctx context.Context, id, token string) (service.ChannelHistoryJob, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.ChannelHistoryJob{}, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read authorization transaction.
	if !service.ValidID(id) || !service.ValidID(token) {
		return service.ChannelHistoryJob{}, service.Fail("invalid_argument", 400, "身份无效")
	}
	if _, _, err = s.lockNotificationConversation(ctx, tx, id); err != nil {
		return service.ChannelHistoryJob{}, err
	}
	return s.notificationContextJob(ctx, tx, id, token)
}
func (s *Store) CompleteNotificationContext(ctx context.Context, id string, v service.NotificationContextCompletion) error {
	if !service.ValidID(id) || !service.ValidID(v.LeaseToken) {
		return service.Fail("invalid_argument", 400, "身份无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	t, epoch, err := s.lockNotificationConversation(ctx, tx, id)
	if err != nil {
		return err
	}
	j, err := s.notificationContextJob(ctx, tx, id, v.LeaseToken)
	if err != nil {
		return err
	}
	if v.Snapshot.Status == "unavailable" {
		return finishFeishuNoticeContext(ctx, tx, id, "unavailable", "provider_history_unavailable")
	}
	if (v.Snapshot.Status != "complete" && v.Snapshot.Status != "partial") || v.AnchorID != j.TriggerMessageID || v.ChatID != j.ChatID || v.ThreadID != j.ThreadID || v.AnchorTime <= 0 || (j.TriggerCreatedAtMS > 0 && v.AnchorTime != j.TriggerCreatedAtMS) || len(v.Snapshot.Items) > j.Limit {
		return service.Fail("invalid_argument", 400, "通知锚点无效")
	}
	if v.AnchorTime < j.PreviousTriggerCreatedAtMS || v.AnchorID == j.PreviousTriggerMessageID {
		return finishFeishuNoticeContext(ctx, tx, id, "unavailable", "context_boundary_already_passed")
	}
	if err = validateHistorySnapshotItems(v.Snapshot.Items, "feishu", v.AnchorID, j.PreviousTriggerMessageID, v.AnchorTime, j.PreviousTriggerCreatedAtMS); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat' AND lifecycle='active' FOR UPDATE`, t.target.Conversation).Scan(&t.target.Session); err != nil {
		return err
	}
	if err = t.refresh(); err != nil {
		return err
	}
	if t.state.Session.Execution.QueuedInputCount+len(v.Snapshot.Items)+1 > 100 {
		return service.Fail("conflict", 409, "上下文队列已满")
	}
	for i := range v.Snapshot.Items {
		v.Snapshot.Items[i].Text = redactTaskDetailBearer(v.Snapshot.Items[i].Text)
	}
	for _, item := range v.Snapshot.Items {
		user := item.SenderType == "" || item.SenderType == "user"
		name, mentions, e := t.channelPresentation(item.SenderID, service.ChannelMessageMetadata{SenderName: item.SenderName, SenderTenant: item.SenderTenant, SenderIDType: item.SenderIDType, Mentions: item.Mentions}, user)
		if e != nil {
			return e
		}
		body, kind := renderSystemMessage(item.Text, mentions), "system"
		if user {
			body = renderGroupMessage(name, item.Text, mentions)
			kind = "user"
		}
		if _, e = insertNotificationContext(t, id, body, kind, epoch); e != nil {
			return e
		}
	}
	var resultKind string
	if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN reminder_event_id IS NULL THEN 'task_result' ELSE 'system' END FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&resultKind); err != nil {
		return err
	}
	result, err := insertNotificationContext(t, id, "[系统消息]"+strings.TrimPrefix(j.TriggerText, "[系统消息]"), resultKind, epoch)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_channel_history_boundaries(conversation_id,trigger_message_id,trigger_created_at_ms,input_seq,notification_id)VALUES($1,$2,$3,$4,$5)ON CONFLICT(conversation_id)DO UPDATE SET trigger_message_id=EXCLUDED.trigger_message_id,trigger_created_at_ms=EXCLUDED.trigger_created_at_ms,input_seq=EXCLUDED.input_seq,inbox_id=NULL,notification_id=EXCLUDED.notification_id`, t.target.Conversation, v.AnchorID, v.AnchorTime, result.Seq, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET context_status='admitted',context_input_id=$2,context_snapshot=$3,provider_created_at_ms=$4,provider_chat_id=$5,provider_thread_id=$6,context_error=NULL,context_lease_until=NULL,updated_at=now() WHERE id=$1`, id, result.ID, raw, v.AnchorTime, v.ChatID, v.ThreadID)
	if err != nil {
		return err
	}
	if err = t.SnapshotEvent(); err != nil {
		return err
	}
	return tx.Commit()
}

func finishFeishuNoticeContext(ctx context.Context, tx *sql.Tx, id, status, code string) error {
	if err := finishNoticeContext(ctx, tx, id, status, code); err != nil {
		return err
	}
	return tx.Commit()
}
