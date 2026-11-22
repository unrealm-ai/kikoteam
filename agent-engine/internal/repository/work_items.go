package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) WorkBoundaryToFence(ctx context.Context, w Work, turn string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT b.id FROM manager_work_boundaries b JOIN manager_sessions s ON s.id=b.session_id
 WHERE b.session_id=$1 AND b.turn_id=$2 AND b.state='preparing' AND b.fenced_at IS NULL AND b.stop_revision=s.stop_revision
 AND s.engine_id=$3 AND s.assignment_id=$4 AND s.generation=$5 AND s.active_turn_id=b.turn_id`, w.SessionID, turn, s.EngineID, w.AssignmentID, w.Generation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) FenceWorkBoundary(ctx context.Context, w Work, turn, id string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		var fenced string
		err := tx.QueryRowContext(ctx, `UPDATE manager_work_boundaries b SET fenced_at=now() FROM manager_sessions s
 WHERE b.id=$1 AND b.session_id=$2 AND b.turn_id=$3 AND s.id=b.session_id AND s.active_turn_id=b.turn_id
 AND b.state='preparing' AND b.stop_revision=s.stop_revision AND s.stop_revision=s.applied_stop_revision RETURNING b.id`, id, w.SessionID, turn).Scan(&fenced)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		return err
	})
}

func settleWorkBoundary(ctx context.Context, tx *sql.Tx, w Work, turn, status, code string) error {
	state, message := "blocked", "事项交接停稳需核对"
	if status == "canceled" {
		state, message = "cancelled", ""
	}
	if code == "awaiting_work_commit" {
		state, message = "safe", "已停稳，等待提交事项"
	}
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE manager_work_boundaries SET state=CASE WHEN $3='safe' AND (fenced_at IS NULL
 OR NOT EXISTS(SELECT FROM manager_sessions s WHERE s.id=$1 AND s.background_process_count=0 AND s.block_reason IS NULL)
 OR EXISTS(SELECT FROM manager_capability_calls c WHERE c.session_id=$1 AND c.status='authorized')) THEN 'blocked' ELSE $3 END
 WHERE session_id=$1 AND turn_id=$2 AND state='preparing' RETURNING id,state`, w.SessionID, turn, state).Scan(&id, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "blocked" {
		message = "事项交接停稳需核对"
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((
 SELECT jsonb_agg(CASE WHEN q->>'request_id'=$2 THEN jsonb_set(q,'{message}',to_jsonb($3::text)) ELSE q END ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)
 WHERE $4<>'cancelled' OR q->>'request_id'<>$2),'[]') WHERE id=$1`, w.SessionID, id, message, state)
	return err
}
