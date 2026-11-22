package repository

import (
	"context"
	"database/sql"
	"errors"
)

func updateExistingWorkAction(ctx context.Context, tx *sql.Tx, session, request, message string) error {
	_, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=(
 SELECT COALESCE(jsonb_agg(CASE WHEN q->>'type'='work_item' AND q->>'request_id'=$2
 THEN jsonb_set(q,'{message}',to_jsonb($3::text)) ELSE q END ORDER BY ord),'[]')
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)) WHERE id=$1`, session, request, message)
	return err
}

// Processing status reflects the currently assigned Session and its receipts,
// so the model never has to maintain a separate progress counter.
func (s *Store) ReconcileWorkProgress(ctx context.Context) error {
	for range 25 {
		changed, err := s.reconcileWorkOutcome(ctx)
		if err != nil {
			return err
		}
		if !changed {
			break
		}
	}
	if err := s.deliverWorkNotices(ctx); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	rows, err := tx.QueryContext(ctx, `SELECT w.id,w.workspace_id,root.id,next.message
 FROM manager_work_items w JOIN manager_sessions root ON root.id=w.root_session_id
 JOIN manager_work_assignments a ON a.id=w.current_assignment_id
 JOIN manager_sessions child ON child.id=a.session_id
 CROSS JOIN LATERAL (SELECT CASE
 WHEN w.status='awaiting_acceptance' AND NOT manager_work_review_ready(root.id) THEN '成果已保存，等待原任务模型恢复'
 WHEN w.status='awaiting_acceptance' THEN '成果已提交，等待原任务验收'
 WHEN child.stop_revision>0 OR child.lifecycle<>'active' THEN '接手任务已停止'
 WHEN EXISTS(SELECT FROM manager_work_boundaries b WHERE b.session_id=child.id AND b.state='blocked') THEN '接手执行需核对'
 WHEN EXISTS(SELECT FROM manager_work_boundaries b WHERE b.session_id=child.id AND b.state IN ('preparing','safe')) THEN '接手环节正在收尾'
 WHEN EXISTS(SELECT FROM jsonb_array_elements(child.required_actions) q WHERE q->>'type' IN ('human_confirmation','confirmation')) THEN '接手任务等待人工确认'
 WHEN child.active_turn_id IS NOT NULL THEN '接手员工处理中'
 WHEN EXISTS(SELECT FROM manager_inputs i WHERE i.session_id=child.id AND i.status='queued') THEN '接手任务已排队'
 WHEN EXISTS(SELECT FROM manager_inputs i WHERE i.session_id=child.id AND i.status='rejected') THEN '接手任务受阻'
 ELSE '接手任务已结束，等待成果核对' END AS message) next
 WHERE w.status IN ('working','awaiting_acceptance') AND root.stop_revision=w.root_stop_revision AND root.lifecycle='active'
 AND EXISTS(SELECT FROM jsonb_array_elements(root.required_actions) q
 WHERE q->>'type'='work_item' AND q->>'request_id'=w.id::text AND q->>'message'<>next.message)
 ORDER BY w.id FOR UPDATE OF w,root SKIP LOCKED LIMIT 25`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close preserves iteration errors before updates.
	type progress struct{ id, workspace, session, message string }
	items := []progress{}
	for rows.Next() {
		var p progress
		if err = rows.Scan(&p.id, &p.workspace, &p.session, &p.message); err != nil {
			return errors.Join(err, rows.Close())
		}
		items = append(items, p)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, p := range items {
		if err = updateExistingWorkAction(ctx, tx, p.session, p.id, p.message); err != nil {
			return err
		}
		if err = workSnapshot(ctx, tx, s, p.workspace, p.session); err != nil {
			return err
		}
	}
	return tx.Commit()
}
