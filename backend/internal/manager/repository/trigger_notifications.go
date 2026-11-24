package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

var noticeThink = regexp.MustCompile(`(?is)<think\b[^>]*>.*?(?:</think>|$)`)
var noticeSecret = regexp.MustCompile(`(?i)(?:Bearer\s+|(?:api[_-]?key|password|secret|token)\s*[:=]\s*)[^\s,;]+`)

// Channel timelines and quoted messages may include a link the system sent.
var taskDetailBearerURL = regexp.MustCompile(`(?i)https?://[^\s<>/"']+/tasks/[A-Za-z0-9_-]{43}([^A-Za-z0-9_-]|$)`)

func redactTaskDetailBearer(text string) string {
	return taskDetailBearerURL.ReplaceAllString(text, "[任务详情链接]${1}")
}

var noticePath = regexp.MustCompile(`(?:file://\S+|/(?:Users|home|private|tmp|var)/[^\s<>"']+)`)

func noticeSummary(status, text string) (string, []string) {
	if status != "completed" {
		return "[系统消息]本次执行未正常完成（" + status + "），请在任务记录查看。", nil
	}
	const label = "[系统消息]本次执行已结束（不代表整项任务完成）"
	text = strings.Trim(redactNoticeText(text), "\r\n")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return label, nil
	}
	if r := []rune(text); len(r) > 1000 {
		text = string(r[:1000])
		if end := strings.LastIndexByte(text, '\n'); end > 0 {
			text = text[:end]
		}
		text += "…"
	}
	return label + "\n" + text, markdownReplyProviders(status, text)
}

func redactNoticeText(text string) string {
	text = noticeThink.ReplaceAllString(redactTaskDetailBearer(text), "")
	text = noticeSecret.ReplaceAllString(text, "[已隐藏]")
	return noticePath.ReplaceAllString(text, "[文件引用]")
}

func cleanNoticeText(text string) string {
	text = strings.Join(strings.Fields(redactNoticeText(text)), " ")
	r := []rune(text)
	if len(r) > 1000 {
		text = string(r[:1000]) + "…"
	}
	return text
}

// Materialization reports a specific stable execution, never a permanent task
// business state. Later supplementary input may produce its own notice.
func (s *Store) MaterializeTriggerNotifications(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.id FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id JOIN manager_tasks k ON k.id=s.task_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE `+ordinaryTaskResult+` AND k.trigger_id IS NOT NULL AND i.trigger_agent_loop AND s.active_turn_id IS NULL AND s.background_process_count=0 AND s.block_reason IS NULL AND s.stop_revision=s.applied_stop_revision AND NOT COALESCE(t.has_unknown_tool_results,false) AND NOT EXISTS(SELECT 1 FROM manager_inputs q WHERE q.session_id=s.id AND q.status IN ('queued','started')) AND i.seq=(SELECT max(q.seq) FROM manager_inputs q WHERE q.session_id=s.id AND q.trigger_agent_loop) AND (t.input_id=i.id OR i.turn_id IS NULL) AND (t.status IN ('completed','failed','canceled','interrupted') OR (i.turn_id IS NULL AND i.status IN ('rejected','canceled'))) AND EXISTS(SELECT 1 FROM manager_trigger_notification_targets nt WHERE nt.task_id=k.id AND NOT EXISTS(SELECT 1 FROM manager_trigger_notifications n WHERE n.source_input_id=i.id AND n.conversation_id=nt.conversation_id)) ORDER BY i.created_at,i.id LIMIT 50`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked before writes.
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
		if err = s.materializeTriggerNotice(ctx, id); err != nil {
			return err
		}
	}
	return s.materializeWebTriggerNotices(ctx)
}
func (s *Store) materializeTriggerNotice(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var task, status, text string
	var turn sql.NullString
	var ready bool
	err = tx.QueryRowContext(ctx, `SELECT s.task_id,i.turn_id,COALESCE(t.status,i.status),COALESCE(t.result_summary,''),`+ordinaryTaskResult+` AND s.active_turn_id IS NULL AND COALESCE(s.background_process_count=0,false) AND s.block_reason IS NULL AND s.stop_revision=s.applied_stop_revision AND NOT COALESCE(t.has_unknown_tool_results,false) AND NOT EXISTS(SELECT 1 FROM manager_inputs q WHERE q.session_id=s.id AND q.status IN ('queued','started')) AND i.seq=(SELECT max(q.seq) FROM manager_inputs q WHERE q.session_id=s.id AND q.trigger_agent_loop) FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE i.id=$1 FOR SHARE OF s`, id).Scan(&task, &turn, &status, &text, &ready)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	switch status {
	case "completed", "failed", "canceled", "interrupted", "rejected":
	default:
		return nil
	}
	body, markdownProviders := noticeSummary(status, text)
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_trigger_notifications(id,task_id,source_input_id,source_turn_id,conversation_id,trigger_id,trigger_version,target,text,result_status,content_format)
 SELECT gen_random_uuid(),task_id,$2,$3,conversation_id,trigger_id,trigger_version,target,$4,$5,
 CASE WHEN target->>'provider'=ANY($6::text[]) THEN 'markdown' ELSE 'text' END
 FROM manager_trigger_notification_targets WHERE task_id=$1 ON CONFLICT(source_input_id,conversation_id)DO NOTHING`, task, id, turn, body, status, markdownProviders)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) materializeWebTriggerNotices(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM manager_trigger_notifications WHERE status='pending' AND target->>'provider'='web' ORDER BY created_at,id LIMIT 50`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked before writes.
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
		if err = s.sendWebTriggerNotice(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) sendWebTriggerNotice(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var group, w, agent, text, status string
	err = tx.QueryRowContext(ctx, `SELECT target->>'web_group_id',target->>'workspace_id',target->>'agent_id',text,status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&group, &w, &agent, &text, &status)
	if err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE id=$1 FOR UPDATE`, group).Scan(&locked); err != nil {
		return err
	}
	// Recheck after the group lock; concurrent workers cannot send twice.
	if err = tx.QueryRowContext(ctx, `SELECT status FROM manager_trigger_notifications WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	allowed, err := noticeAllowed(ctx, tx, id)
	if err != nil {
		return err
	}
	if !allowed {
		_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='cancelled',error_code='access_revoked',context_status='suppressed',updated_at=now()WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	seq, err := nextWebGroupSeq(ctx, tx, group)
	if err != nil {
		return err
	}
	message := service.NewID()
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_web_group_messages(id,workspace_id,group_id,seq,author_kind,agent_id,display_name,text)VALUES($1,$2,$3,$4,'system',$5,'系统消息',$6)`, message, w, group, seq, agent, text)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_trigger_notifications SET status='sent',web_message_id=$2::uuid,provider_message_id=$2::text,confirmed_at=now(),context_status='pending',updated_at=now()WHERE id=$1`, id, message)
	if err != nil {
		return err
	}
	if err = s.admitNotificationContextTx(ctx, tx, id); err != nil {
		return err
	}
	var contextStatus string
	if err = tx.QueryRowContext(ctx, `SELECT context_status FROM manager_trigger_notifications WHERE id=$1`, id).Scan(&contextStatus); err != nil {
		return err
	}
	if contextStatus == "pending" {
		return service.Fail("notification_context_backpressure", 409, "群上下文队列暂满，通知未送出")
	}
	return tx.Commit()
}
