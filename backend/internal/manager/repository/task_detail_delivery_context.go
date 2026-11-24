package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) materializeTaskDetailContexts(ctx context.Context) error {
	// Erase payloads even for deliveries canceled by binding cleanup or left
	// unknown after their lease expired; never retain past the grant lifetime.
	if _, err := s.DB.ExecContext(ctx, `UPDATE manager_task_detail_deliveries x SET sealed_url=NULL
 FROM manager_channel_deliveries d,manager_task_detail_grants g
 WHERE x.delivery_id=d.id AND x.grant_id=g.id AND x.sealed_url IS NOT NULL
 AND (d.status IN ('sent','failed','unknown','cancelled') OR g.revoked_at IS NOT NULL OR g.expires_at<=clock_timestamp())`); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT x.delivery_id, i.session_id FROM manager_task_detail_deliveries x
 JOIN manager_channel_deliveries d ON d.id=x.delivery_id
 JOIN manager_inputs i ON i.id=x.source_input_id
 WHERE d.status='sent' AND x.presentation IS NOT NULL AND x.context_settled_at IS NULL
 ORDER BY d.confirmed_at,d.id LIMIT 25`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close reports failure before writes.
	type item struct{ id, session string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.session); err != nil {
			return errors.Join(err, rows.Close())
		}
		items = append(items, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, v := range items {
		if err = s.admitTaskDetailContext(ctx, v.id, v.session); err != nil {
			return err
		}
	}
	return nil
}

type taskDetailContext struct {
	workspace, principal, presentation, title, scope, task string
	generation, epoch                                      int64
	allowed                                                bool
	queued                                                 int
}

func (s *Store) admitTaskDetailContext(ctx context.Context, id, session string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var locked string
	err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE id=$1 FOR UPDATE`, session).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var n taskDetailContext
	err = tx.QueryRowContext(ctx, `SELECT ss.workspace_id,i.channel_principal_id,i.channel_generation,a.admission_epoch,x.title,g.task_id,g.scope,x.presentation,
 a.enabled AND ss.mode='chat' AND ss.lifecycle='active' AND manager_input_actor_allowed(i.id),
 (SELECT count(*) FROM manager_inputs q WHERE q.session_id=ss.id AND q.status='queued')
 FROM manager_task_detail_deliveries x JOIN manager_channel_deliveries d ON d.id=x.delivery_id
 JOIN manager_task_detail_grants g ON g.id=x.grant_id JOIN manager_inputs i ON i.id=x.source_input_id
 JOIN manager_sessions ss ON ss.id=i.session_id JOIN platform_agents a ON a.id=ss.agent_id
 WHERE x.delivery_id=$1 AND ss.id=$2 AND d.status='sent' AND x.context_settled_at IS NULL AND x.presentation IS NOT NULL
 FOR UPDATE OF x`, id, session).Scan(&n.workspace, &n.principal, &n.generation, &n.epoch, &n.title, &n.task, &n.scope, &n.presentation, &n.allowed, &n.queued)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.allowed {
		if n.queued >= 100 {
			return nil // Retry after there is room; do not delay the real provider receipt.
		}
		if err = s.insertTaskDetailContext(ctx, tx, session, n); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_task_detail_deliveries SET context_settled_at=now() WHERE delivery_id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) insertTaskDetailContext(ctx context.Context, tx *sql.Tx, session string, n taskDetailContext) error {
	var seq int64
	err := tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, session).Scan(&seq)
	if err != nil {
		return err
	}
	kind := "卡片"
	if n.presentation == service.TaskDetailLinkPresentation {
		kind = "链接"
	}
	access := "仅可查看"
	if n.scope == "write" {
		access = "可查看、补充或在允许时停止"
	}
	trigger := false
	body := "[系统消息] 已发送任务「" + n.title + "」（task_id: " + n.task + "）的详情" + kind + "，" + access + "。"
	v := service.Input{ID: service.NewID(), SessionID: session, Seq: seq, Text: body, SourceKind: "system", Status: "queued", DeliveryMode: "queue", TriggerAgentLoop: &trigger, PrincipalType: "channel", ChannelPrincipalID: &n.principal, CreatedAt: time.Now().UTC()}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,admission_epoch,seq,text,created_at,channel_principal_id,channel_generation,context_state,delivery_mode,source_kind,trigger_agent_loop)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending','queue','system',false)`, v.ID, n.workspace, session, n.epoch, seq, body, v.CreatedAt, n.principal, n.generation)
	if err != nil {
		return err
	}
	t := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: n.workspace, Session: session}}
	if err = t.Event("input.updated", &v.ID, nil, service.InputFields(v)); err != nil {
		return err
	}
	return t.SnapshotEvent()
}
