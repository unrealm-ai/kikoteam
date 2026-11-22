package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Web rooms are local Manager ingress, independent of Event Hub credentials.
func startWebGroupWorker(ctx context.Context, store service.WebGroupWorker, log *slog.Logger) func() {
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
				for n := 0; n < 20; n++ {
					if err := store.ProcessWebGroupInbox(child); err != nil {
						log.Warn("Web group inbox pending", "event", "web_group.inbox_retry")
						break
					}
				}
				if err := store.MaterializeWebGroupReplies(child); err != nil {
					log.Warn("Web group replies pending", "event", "web_group.reply_retry")
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
