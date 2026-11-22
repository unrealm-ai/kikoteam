package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type workNotice struct {
	id                                                int64
	item, workspace, root, task                       string
	conversation, chat, group, inbox                  string
	turn, input, kind, title, summary, result, target string
	allowed                                           bool
}

func (s *Store) deliverWorkNotices(ctx context.Context) error {
	for range 25 {
		ok, err := s.deliverNextWorkNotice(ctx)
		if err != nil || !ok {
			return err
		}
	}
	return nil
}
func (s *Store) deliverNextWorkNotice(ctx context.Context) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Release locks on every exit; commits report their own errors.
	var n workNotice
	err = tx.QueryRowContext(ctx, `SELECT e.id,w.id,w.workspace_id,r.id,COALESCE(c.id::text,''),k.id,COALESCE(t.id::text,''),COALESCE(t.input_id,w.authority_input_id),
 COALESCE(k.source_chat_session_id::text,(SELECT id::text FROM manager_sessions WHERE conversation_id=c.id AND mode='chat' AND lifecycle='active' ORDER BY created_at DESC LIMIT 1),''),
 COALESCE(c.web_group_id::text,''),COALESCE(box.id::text,''),e.kind,w.title,e.summary,
 CASE WHEN t.status='completed' THEN COALESCE(t.result_summary,'') ELSE '' END,COALESCE(a.name,''),
 EXISTS(SELECT FROM platform_agents a JOIN manager_inputs i ON i.id=w.authority_input_id WHERE a.id=r.agent_id AND a.enabled AND a.admission_epoch=i.admission_epoch) AND r.lifecycle='active' AND r.stop_revision=w.root_stop_revision AND manager_input_actor_before_work_items(w.authority_input_id)
 AND (c.id IS NULL OR c.lifecycle='active') AND NOT EXISTS(SELECT FROM manager_sessions dest WHERE dest.id=k.source_chat_session_id AND dest.lifecycle<>'active')
 FROM manager_work_events e JOIN manager_work_items w ON w.id=e.work_item_id JOIN manager_sessions r ON r.id=w.root_session_id
 JOIN manager_tasks k ON k.id=r.task_id LEFT JOIN manager_conversations c ON c.id=r.conversation_id
 LEFT JOIN manager_channel_inbox box ON box.input_id=k.source_chat_input_id LEFT JOIN manager_turns t ON t.id=e.turn_id
 LEFT JOIN platform_agents a ON a.id=e.target_agent_id
 WHERE e.notify AND e.notified_at IS NULL AND (e.kind<>'completed' OR t.finished_at IS NOT NULL)
 AND NOT EXISTS(SELECT FROM manager_work_events prior WHERE prior.work_item_id=e.work_item_id AND prior.id<e.id AND prior.notify AND prior.notified_at IS NULL)
 ORDER BY e.id LIMIT 1 FOR UPDATE OF e SKIP LOCKED`).Scan(&n.id, &n.item, &n.workspace, &n.root, &n.conversation, &n.task, &n.turn, &n.input,
		&n.chat, &n.group, &n.inbox, &n.kind, &n.title, &n.summary, &n.result, &n.target, &n.allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	body := cleanNoticeText(n.text())
	if err = s.publishWorkNotice(ctx, tx, n, body); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_work_events SET notified_at=now() WHERE id=$1`, n.id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (n workNotice) text() string {
	prefix := "事项「" + n.title + "」"
	switch n.kind {
	case "created":
		return prefix + "已交给" + n.target + "处理。"
	case "returned":
		return prefix + "已退回" + n.target + "整改。" + n.summary
	case "failed":
		return prefix + "已结束。" + n.summary
	default:
		text := strings.TrimSpace(n.result)
		if text == "" {
			return prefix + "已验收通过。"
		}
		return prefix + "已验收通过。\n" + text
	}
}
func (s *Store) publishWorkNotice(ctx context.Context, tx *sql.Tx, n workNotice, body string) error {
	// Trigger jobs retain the original per-target snapshot and provider delivery protocol.
	if n.conversation == "" {
		if !n.allowed {
			_, err := tx.ExecContext(ctx, `INSERT INTO manager_work_notice_targets(event_id,conversation_id,status) SELECT $1,conversation_id,'cancelled' FROM manager_trigger_notification_targets WHERE task_id=$2`, n.id, n.task)
			return err
		}
		_, err := tx.ExecContext(ctx, `WITH sent AS (
 INSERT INTO manager_trigger_notifications(id,task_id,source_input_id,source_turn_id,conversation_id,trigger_id,trigger_version,target,text,result_status)
 SELECT gen_random_uuid(),task_id,$2,$3,conversation_id,trigger_id,trigger_version,target,$4,$5 FROM manager_trigger_notification_targets WHERE task_id=$1
 RETURNING id,conversation_id)
 INSERT INTO manager_work_notice_targets(event_id,conversation_id,trigger_notice_id,status) SELECT $6,conversation_id,id,'pending' FROM sent`, n.task, n.input, nullID(n.turn), body, n.kind, n.id)
		return err
	}
	status := "sent"
	var delivery any
	if !n.allowed {
		status = "cancelled"
	} else {
		if n.chat != "" {
			st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: n.workspace, Session: n.chat}}
			if err := st.Event("work_item.notice", nil, nil, map[string]any{"work_item_id": n.item, "source_kind": "work_item_notice", "message_id": service.NewID(), "content": []map[string]string{{"type": "text", "text": body}}}); err != nil {
				return err
			}
		}
		if n.group != "" {
			seq, err := nextWebGroupSeq(ctx, tx, n.group)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO manager_web_group_messages(id,workspace_id,group_id,seq,author_kind,agent_id,display_name,text)
 SELECT gen_random_uuid(),workspace_id,$2,$3,'system',agent_id,'事项交接',$4 FROM manager_sessions WHERE id=$1`, n.root, n.group, seq, body)
			if err != nil {
				return err
			}
		}
		if n.inbox != "" {
			id := service.NewID()
			_, err := tx.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) SELECT $1,id,binding_id,connection_generation,$3,$4 FROM manager_channel_inbox WHERE id=$2`, id, n.inbox, fmt.Sprintf("work_item:%d", n.id), body)
			if err != nil {
				return err
			}
			delivery, status = id, "pending"
		} else if n.chat == "" && n.group == "" {
			status = "unavailable"
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_work_notice_targets(event_id,conversation_id,channel_delivery_id,status) VALUES($1,$2,$3,$4)`, n.id, n.conversation, delivery, status)
	return err
}
