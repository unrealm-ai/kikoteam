package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestAssetCleanupStartsAndCancels(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	stop := startAssetCleanup(context.Background(), func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup has no deadline")
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer stop()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	stop()
	select {
	case <-canceled:
	default:
		t.Fatal("shutdown did not wait for cancellation")
	}
}
