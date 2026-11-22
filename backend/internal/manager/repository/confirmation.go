package repository

import (
	"database/sql"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// WithSession already holds the session lock. The reply belongs to the human
// whose input authorized the turn, including after the worker has retired.
func (t *sessionTx) AnswerConfirmation(answer service.Confirmation) (service.Input, bool, error) {
	actor := t.target.Actor
	var existing string
	err := t.tx.QueryRowContext(t.ctx, `SELECT id FROM manager_inputs WHERE session_id=$1 AND confirmation_request_id=$2`, t.target.Session, answer.RequestID).Scan(&existing)
	if err == nil {
		v, err := t.Input(existing)
		if err != nil {
			return service.Input{}, false, err
		}
		if v.SubmittedBy != actor || v.Confirmed == nil || *v.Confirmed != *answer.Confirmed {
			return service.Input{}, false, service.Fail("confirmation_conflict", 409, "该问题已有答复")
		}
		return v, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return service.Input{}, false, err
	}
	var title, message string
	err = t.tx.QueryRowContext(t.ctx, `SELECT q->>'title', q->>'message'
 FROM manager_sessions s CROSS JOIN LATERAL jsonb_array_elements(s.required_actions) q
 JOIN manager_turns t ON t.id=(q->>'turn_id')::uuid JOIN manager_inputs root ON root.id=t.input_id JOIN platform_agents a ON a.id=$4
 WHERE s.id=$1 AND q->>'request_id'=$2 AND t.session_id=s.id AND root.submitted_by=$3 AND manager_input_actor_allowed(root.id)
 AND root.admission_epoch=a.admission_epoch AND s.stop_revision=s.applied_stop_revision
 AND (q->>'expires_at' IS NULL OR (q->>'expires_at')::timestamptz>clock_timestamp())`, t.target.Session, answer.RequestID, actor, t.state.Session.AgentID).Scan(&title, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Input{}, false, service.Fail("confirmation_unavailable", 409, "问题已失效或不属于当前用户")
	}
	if err != nil {
		return service.Input{}, false, err
	}
	choice := "否"
	if *answer.Confirmed {
		choice = "是"
	}
	v, err := t.InsertInput(service.Input{ID: service.NewID(), SessionID: t.target.Session, SubmittedBy: actor, DeliveryMode: "queue", SourceKind: "user", Status: "queued", CreatedAt: time.Now().UTC(), ConfirmationRequestID: &answer.RequestID, Confirmed: answer.Confirmed, Text: title + "\n" + message + "\n用户回答：" + choice})
	if err != nil {
		return v, false, err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET required_actions=(SELECT COALESCE(jsonb_agg(q ORDER BY ord),'[]') FROM jsonb_array_elements(required_actions) WITH ORDINALITY AS x(q,ord) WHERE q->>'request_id'<>$2) WHERE id=$1`, t.target.Session, answer.RequestID)
	return v, false, err
}

// The existing dispatch tick also expires deadlines while no Pi is resident.
const expiredConfirmation = `s.required_actions <> '[]' AND EXISTS (SELECT FROM jsonb_array_elements(s.required_actions) q WHERE (q->>'expires_at')::timestamptz <= now())`

func (t *sessionTx) expireConfirmations() error {
	result, err := t.tx.ExecContext(t.ctx, `UPDATE manager_sessions s SET required_actions=(SELECT COALESCE(jsonb_agg(q ORDER BY ord),'[]') FROM jsonb_array_elements(s.required_actions) WITH ORDINALITY AS x(q,ord) WHERE q->>'expires_at' IS NULL OR (q->>'expires_at')::timestamptz > now()) WHERE s.id=$1 AND `+expiredConfirmation, t.target.Session)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	return t.SnapshotEvent()
}
