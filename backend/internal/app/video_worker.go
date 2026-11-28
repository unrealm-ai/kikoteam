package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/videogen"
)

// A Hub tick wakes one bounded reconciliation batch; requests never wait for media I/O.
func startVideoWorker(parent context.Context, wake <-chan struct{}, videos *videogen.Service, log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				batch, stop := context.WithTimeout(ctx, 110*time.Second)
				err := videos.Reconcile(batch)
				stop()
				if err != nil && ctx.Err() == nil {
					log.Warn("Video reconciliation deferred", "event", "video.reconcile_failed", "error_code", "video_reconcile_failed")
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
