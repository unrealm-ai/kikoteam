package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Only a PostgreSQL-declared deadlock is known to have aborted the transaction.
// Never retry connection failures or an ambiguous commit result.
func retrySessionTransaction(ctx context.Context, attempt func() error) error {
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt()
		var pg *pgconn.PgError
		if err == nil || n == 2 || !errors.As(err, &pg) || pg.Code != "40P01" {
			return err
		}
		timer := time.NewTimer(time.Duration(n+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
