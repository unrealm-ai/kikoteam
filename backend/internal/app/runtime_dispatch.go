package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
)

func loadRuntimeToken() (string, error) {
	token := os.Getenv("KIKO_ENGINE_TOKEN")
	if token != "" && len(token) < 32 {
		return "", errors.New("invalid runtime credential")
	}
	return token, nil
}
func startRuntimeDispatch(ctx context.Context, store *repository.Store, log *slog.Logger, token string) func() {
	if token == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			work, stop := context.WithTimeout(ctx, 5*time.Second)
			err := errors.Join(store.ReconcileWorkItems(work), store.Dispatch(work), store.ReconcileWorkProgress(work))
			stop()
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("Runtime dispatch temporarily unavailable", "event", "runtime.dispatch_failed", "error_code", "dispatch_failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
