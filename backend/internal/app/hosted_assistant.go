package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/repository"
)

func startHostedAssistant(parent context.Context, store *repository.HostedAssistantStore, log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			for range 20 {
				attempt, stop := context.WithTimeout(ctx, 10*time.Second)
				worked, err := store.ProvisionNext(attempt)
				stop()
				if err != nil && ctx.Err() == nil {
					log.Warn("Assistant provisioning attempt failed", "event", "assistant.provisioning_failed")
				}
				if !worked || err != nil {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
