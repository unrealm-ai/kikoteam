package repository

import (
	"context"
	"database/sql"
	"errors"
)

func completedInputEvents(ctx context.Context, tx *sql.Tx, w Work, t Turn) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM manager_inputs WHERE turn_id=$1 ORDER BY seq`, t.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close and iteration errors are checked before emitting.
	ids := []string{}
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
		if err = inputEvent(ctx, tx, w, id, t.ID); err != nil {
			return err
		}
	}
	return nil
}
