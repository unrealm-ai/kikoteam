package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
)

func startHumanRejectionWorker(ctx context.Context, store *repository.Store, log *slog.Logger) func() {
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-ticker.C:
				work, stop := context.WithTimeout(child, 5*time.Second)
				err := store.MaterializeHumanRejectionNotices(work)
				stop()
				if err != nil && child.Err() == nil {
					log.Warn("Human rejection notices pending", "event", "human_rejection.notice_retry")
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
