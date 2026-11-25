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
