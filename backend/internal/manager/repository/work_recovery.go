package repository

import (
	"context"
	"database/sql"
	"errors"
)

func workEvent(ctx context.Context, tx *sql.Tx, item, kind, agent, target, turn, summary string, notify bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_work_events(work_item_id,kind,agent_id,target_agent_id,turn_id,summary,notify)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, item, kind, nullID(agent), nullID(target), nullID(turn), summary, notify)
	return err
}

// A terminal Turn can close only the assignment which still owns the item.
// Safety fences, queued continuations and uncertain side effects take precedence.
func (s *Store) reconcileWorkOutcome(ctx context.Context) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Release locks on every exit; commits report their own errors.
	var item, root, workspace, agent, turn, reason, kind string
	err = tx.QueryRowContext(ctx, `SELECT w.id,r.id,w.workspace_id,child.agent_id,COALESCE(t.id::text,''),
 CASE WHEN stopped.explicit THEN '接手任务已停止或归档' WHEN auth.revoked THEN '执行授权已失效'
 WHEN pending.uncertain THEN '当前环节已停止，执行结果待核对'
 WHEN t.status='completed' THEN '本轮已结束，缺少提交或验收记录'
 ELSE '当前环节执行失败：'||COALESCE(t.error->>'code','execution_failed') END,
 CASE WHEN stopped.explicit OR auth.revoked OR pending.uncertain OR t.status='completed' THEN 'blocked' ELSE 'failed' END
 FROM manager_work_items w JOIN manager_sessions r ON r.id=w.root_session_id
 JOIN manager_work_assignments a ON a.id=w.current_assignment_id
 JOIN manager_sessions child ON child.id=CASE WHEN w.status='working' THEN a.session_id ELSE r.id END
 JOIN LATERAL (SELECT i.* FROM manager_inputs i WHERE i.session_id=child.id AND i.trigger_agent_loop AND
 (CASE WHEN w.status='working' THEN i.work_assignment_id=a.id ELSE i.work_submission_id=w.current_submission_id OR EXISTS(SELECT FROM manager_human_requests h JOIN manager_inputs original ON original.id=h.authority_input_id WHERE h.id=i.human_request_id AND original.work_submission_id=w.current_submission_id) END)
 ORDER BY i.seq DESC LIMIT 1) i ON true
 JOIN platform_agents employee ON employee.id=child.agent_id
 LEFT JOIN manager_turns t ON t.id=i.turn_id
 CROSS JOIN LATERAL (SELECT child.id<>r.id AND (child.lifecycle<>'active' OR child.stop_revision>0) AS explicit) stopped
 CROSS JOIN LATERAL (SELECT i.status='rejected' OR NOT employee.enabled OR i.admission_epoch<>employee.admission_epoch OR NOT manager_input_actor_allowed(i.id) AS revoked) auth
 CROSS JOIN LATERAL (SELECT t.status='interrupted' OR t.has_unknown_tool_results OR child.background_process_count IS DISTINCT FROM 0
 OR child.block_reason IS NOT NULL OR EXISTS(SELECT FROM manager_work_boundaries b WHERE b.turn_id=t.id AND b.state='blocked') AS uncertain) pending
 WHERE w.status IN ('working','awaiting_acceptance') AND r.lifecycle='active' AND r.stop_revision=w.root_stop_revision
 AND child.active_turn_id IS NULL AND child.stop_revision=child.applied_stop_revision
 AND (stopped.explicit OR auth.revoked OR t.status IN ('failed','completed','interrupted'))
 AND COALESCE(t.error->>'code','') NOT IN ('awaiting_confirmation','awaiting_human_confirmation')
 AND NOT EXISTS(SELECT FROM jsonb_array_elements(child.required_actions) action WHERE action->>'type' IN ('confirmation','human_confirmation'))
 AND NOT EXISTS(SELECT FROM manager_work_boundaries b WHERE b.turn_id=t.id AND b.state IN ('preparing','safe','committed'))
 AND NOT EXISTS(SELECT FROM manager_human_requests h WHERE h.session_id=child.id AND h.state IN ('preparing','pending','blocked'))
 ORDER BY w.id FOR UPDATE OF w,r,child SKIP LOCKED LIMIT 1`).Scan(&item, &root, &workspace, &agent, &turn, &reason, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_work_items SET status=$2,reason=$3,version=version+1,updated_at=now() WHERE id=$1`, item, kind, reason); err != nil {
		return false, err
	}
	if kind == "failed" {
		_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((SELECT jsonb_agg(q ORDER BY ord) FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord) WHERE q->>'type'<>'work_item' OR q->>'request_id'<>$2),'[]') WHERE id=$1`, root, item)
	} else {
		err = updateExistingWorkAction(ctx, tx, root, item, reason)
	}
	if err != nil {
		return false, err
	}
	if err = workEvent(ctx, tx, item, kind, agent, "", turn, reason, kind == "failed"); err != nil {
		return false, err
	}
	if err = workSnapshot(ctx, tx, s, workspace, root); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
