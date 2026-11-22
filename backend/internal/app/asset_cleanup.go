package app

import (
	"context"
	"log/slog"
	"time"
)

// startAssetCleanup runs bounded, retryable cleanup until server shutdown.
func startAssetCleanup(parent context.Context, cleanup func(context.Context) error, log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			attempt, stop := context.WithTimeout(ctx, time.Minute)
			err := cleanup(attempt)
			stop()
			if err != nil && ctx.Err() == nil {
				log.Warn("Temporary asset cleanup failed; will retry", "event", "assets.cleanup_failed", "error_code", "cleanup_failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			log.Error("Asset cleanup shutdown timed out", "event", "shutdown.assets_timeout", "error_code", "shutdown_timeout")
		}
	}
}
