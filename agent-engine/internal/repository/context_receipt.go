package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) ContextReceipt(ctx context.Context, w Work, input, state string) error {
	if state != "committed" && state != "absent" && state != "unknown" {
		return errors.New("invalid context receipt")
	}
	return s.transaction(ctx, w, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE manager_inputs SET context_state=$3,context_committed_at=CASE WHEN $3='committed' THEN now() ELSE NULL END WHERE session_id=$1 AND (id=$2 OR batch_input_id=$2) AND context_state IS NOT NULL AND context_state IS DISTINCT FROM 'committed'`, w.SessionID, input, state)
		return err
	})
}

type ContextCandidate struct {
	ID  string
	Seq int64
}

func (s *Store) UnresolvedContextInputs(ctx context.Context, w Work, after int64) ([]ContextCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,seq FROM manager_inputs WHERE session_id=$1 AND started_at IS NOT NULL AND context_state IN ('pending','unknown') AND (batch_input_id IS NULL OR batch_input_id=id) AND seq>$2 ORDER BY seq LIMIT 8`, w.SessionID, after)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Scan errors checked below.
	var ids []ContextCandidate
	for rows.Next() {
		var id ContextCandidate
		if err = rows.Scan(&id.ID, &id.Seq); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
