package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestRetrySessionTransactionClassification(t *testing.T) {
	for _, code := range []string{"40P01", "40001", "23505", "08006"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			failure := &pgconn.PgError{Code: code}
			err := retrySessionTransaction(context.Background(), func() error { calls++; return fmt.Errorf("wrapped: %w", failure) })
			want := 1
			if code == "40P01" {
				want = 3
			}
			if calls != want || !errors.Is(err, failure) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
	calls := 0
	err := retrySessionTransaction(context.Background(), func() error {
		calls++
		if calls == 1 {
			return &pgconn.PgError{Code: "40P01"}
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestRetrySessionTransactionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := retrySessionTransaction(ctx, func() error { calls++; cancel(); return &pgconn.PgError{Code: "40P01"} })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
