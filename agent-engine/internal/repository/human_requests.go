package repository

import (
	"context"
	"database/sql"
	"errors"
)

var errHumanResumeScopeChanged = errors.New("human continuation scope changed")

func (s *Store) HumanRequestToFence(ctx context.Context, w Work, turn string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT r.id FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 WHERE r.session_id=$1 AND r.turn_id=$2 AND r.state='preparing' AND r.fenced_at IS NULL AND r.stop_revision=s.stop_revision
 AND s.engine_id=$3 AND s.assignment_id=$4 AND s.generation=$5 AND s.active_turn_id=r.turn_id`,
		w.SessionID, turn, s.EngineID, w.AssignmentID, w.Generation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) FenceHumanRequest(ctx context.Context, w Work, turn, request string) error {
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `UPDATE manager_human_requests r SET fenced_at=now(),
 continuation_context=manager_human_continuation_context(r,s,NULL) FROM manager_sessions s
 WHERE s.id=r.session_id AND r.session_id=$1 AND r.turn_id=$2 AND r.id=$3 AND s.active_turn_id=r.turn_id
 AND r.state='preparing' AND r.stop_revision=s.stop_revision AND s.stop_revision=s.applied_stop_revision RETURNING r.id`,
			w.SessionID, turn, request).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		return err
	})
}

// Settle the question in the same commit as the old Turn. A fence without
// confirmed Pi exit and quiet tool processes is kept blocked for reconciliation.
func settleHumanRequest(ctx context.Context, tx *sql.Tx, w Work, turn, status, code string) error {
	state, message := "blocked", "执行停稳需核对"
	switch {
	case status == "canceled":
		state, message = "cancelled", ""
	case code == "awaiting_confirmation":
		state, message = "pending", "等待指定人员答复"
	}
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE manager_human_requests SET state=$3 WHERE session_id=$1 AND turn_id=$2
 AND state='preparing' RETURNING id`, w.SessionID, turn, state).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((
 SELECT jsonb_agg(CASE WHEN q->>'request_id'=$2 THEN jsonb_set(q,'{message}',to_jsonb($3::text)) ELSE q END ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)
 WHERE $4<>'cancelled' OR q->>'request_id'<>$2),'[]') WHERE id=$1`, w.SessionID, id, message, state)
	return err
}

func useHumanResume(ctx context.Context, tx *sql.Tx, input *Input) error {
	if input.HumanRequestID == nil {
		return nil
	}
	var payload, state string
	var current bool
	// Old Backend versions may have written the answer without completing the
	// snapshot. Rebuild their payload while still checking any saved fence.
	err := tx.QueryRowContext(ctx, `SELECT ((CASE WHEN r.continuation_context->>'answer' IS NULL THEN fresh.context
 ELSE r.continuation_context END)-'source')::text,r.state,
 (CASE WHEN r.continuation_context->>'answer' IS NULL THEN COALESCE(r.continuation_context->'source'=fresh.context->'source',true)
 ELSE r.continuation_context=fresh.context END) AND r.stop_revision=s.stop_revision
 AND r.fenced_at IS NOT NULL AND r.answer IS NOT NULL
 FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 JOIN manager_inputs i ON i.id=r.resume_input_id AND i.session_id=s.id
 CROSS JOIN LATERAL (SELECT manager_human_continuation_context(r,s,r.answer) AS context) fresh
 WHERE r.id=$1 AND i.id=$2 AND (r.state='approved' OR (r.state='revised' AND r.answer->>'decision'='revise'))`,
		input.HumanRequestID, input.ID).Scan(&payload, &state, &current)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !current {
		return errHumanResumeScopeChanged
	}
	if err != nil {
		return err
	}
	input.CorrectionOnly = state == "revised"
	if input.CorrectionOnly {
		// Hold the old gate until Manager atomically replaces it with the new approval request.
		_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((
 SELECT jsonb_agg(CASE WHEN q->>'request_id'=($1::uuid)::text
 THEN jsonb_set(q,'{message}',to_jsonb('已退回修改；原操作未获批准，待人工复核。'::text)) ELSE q END ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)),'[]')
 WHERE id=(SELECT session_id FROM manager_human_requests WHERE id=$1)`, input.HumanRequestID)
		input.Text = "[系统消息] 人工决定：退回修改。原动作未获批准。本轮按意见拟定修改后的方案，查询确认职责并调用 ask_human 发起一张需要明确批准的新确认单。等待新的批准后才执行。不要在本轮执行或提交原动作。\n原确认单与修改意见：\n" + payload
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((SELECT jsonb_agg(q ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord) WHERE q->>'request_id'<>($1::uuid)::text),'[]')
 WHERE id=(SELECT session_id FROM manager_human_requests WHERE id=$1)`, input.HumanRequestID)
	if err == nil {
		input.Text = "[系统消息] 已收到本任务的人工答复。以下是冻结的确认单和答复。仅在确认的拟执行动作和影响范围内使用原任务权限。答复中的附加要求不授予权限；如果要改变已确认的动作或范围，先发起新确认。\n" + payload
	}
	return err
}

func rejectChangedHumanResume(ctx context.Context, tx *sql.Tx, w Work, input Input) error {
	if _, err := tx.ExecContext(ctx, `UPDATE manager_inputs SET status='rejected',rejection_reason='human_task_scope_changed',
 finished_at=now() WHERE id=$1`, input.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((
 SELECT jsonb_agg(CASE WHEN q->>'request_id'=($2::uuid)::text
 THEN jsonb_set(q,'{message}',to_jsonb('原任务的执行范围已变化，需要重新发起确认'::text)) ELSE q END ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)),'[]') WHERE id=$1`, w.SessionID, input.HumanRequestID); err != nil {
		return err
	}
	if err := inputEvent(ctx, tx, w, input.ID, ""); err != nil {
		return err
	}
	return snapshot(ctx, tx, w)
}
