package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type RequiredAction struct {
	Type      string     `json:"type"`
	RequestID string     `json:"request_id"`
	TurnID    string     `json:"turn_id"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (s *Store) RequestConfirmation(ctx context.Context, w Work, action RequiredAction) error {
	data, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		var actions json.RawMessage
		err := tx.QueryRowContext(ctx, `UPDATE manager_sessions SET required_actions=required_actions || jsonb_build_array($3::jsonb)
 WHERE id=$1 AND active_turn_id=$2 AND lifecycle='active' AND stop_revision=applied_stop_revision RETURNING required_actions`, w.SessionID, action.TurnID, data).Scan(&actions)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		if err = emit(ctx, tx, w, "session.requires_action", nil, nil, map[string]any{"required_actions": actions}); err != nil {
			return err
		}
		return snapshot(ctx, tx, w)
	})
}

// Claim before writing to the pipe. A crash after this commit has uncertain
// delivery and must never turn the same answer into a second model prompt.
func (s *Store) ClaimConfirmation(ctx context.Context, w Work, t Turn, request string, expires *time.Time) (*bool, error) {
	var answer *bool
	err := s.scopedTransaction(ctx, w, true, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `UPDATE manager_inputs i SET status='started',started_at=now(),turn_id=$2,context_state=NULL
 FROM manager_sessions s,platform_agents a,manager_inputs root
 WHERE s.id=$1 AND i.session_id=s.id AND i.confirmation_request_id=$3 AND i.status='queued' AND ($5::timestamptz IS NULL OR $5>clock_timestamp())
 AND s.active_turn_id=$2 AND s.stop_revision=s.applied_stop_revision AND s.lifecycle='active'
 AND a.id=s.agent_id AND a.enabled AND root.id=$4 AND i.submitted_by=root.submitted_by
 AND i.admission_epoch=a.admission_epoch AND root.admission_epoch=a.admission_epoch
 AND manager_input_actor_allowed(i.id) AND manager_input_actor_allowed(root.id)
 RETURNING i.id,i.confirmed`, w.SessionID, t.ID, request, t.Input.ID, expires).Scan(&id, &answer)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return inputEvent(ctx, tx, w, id, t.ID)
	})
	return answer, err
}

// A timed/aborted dialog can settle between admission and the next polling tick.
// Only a retired turn may hand an unconsumed answer to a fresh prompt.
func rejectClosedConfirmations(ctx context.Context, tx *sql.Tx, w Work, t Turn) error {
	rows, err := tx.QueryContext(ctx, `UPDATE manager_inputs i SET status='rejected',rejection_reason='confirmation_closed',context_state='absent',finished_at=now()
 WHERE i.session_id=$1 AND i.status='queued' AND i.confirmation_request_id IS NOT NULL AND EXISTS(
 SELECT FROM manager_session_events e CROSS JOIN LATERAL jsonb_array_elements(e.event->'required_actions') q
 WHERE e.session_id=i.session_id AND e.event->>'type'='session.requires_action'
 AND q->>'turn_id'=$2 AND q->>'request_id'=i.confirmation_request_id::text) RETURNING i.id`, w.SessionID, t.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration and close are checked before emitting.
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		if err = inputEvent(ctx, tx, w, id, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CanRetireConfirmation(ctx context.Context, w Work, turn string) (bool, error) {
	var quiet bool
	err := s.DB.QueryRowContext(ctx, `SELECT active_turn_id=$2 AND stop_revision=applied_stop_revision AND background_process_count=0 AND block_reason IS NULL AND NOT EXISTS(SELECT FROM manager_capability_calls WHERE session_id=$1 AND status='authorized')
 FROM manager_sessions WHERE id=$1 AND engine_id=$3 AND assignment_id=$4 AND generation=$5`, w.SessionID, turn, s.EngineID, w.AssignmentID, w.Generation).Scan(&quiet)
	return quiet, err
}
