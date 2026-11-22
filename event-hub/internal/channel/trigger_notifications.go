package channel

import (
	"context"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"log/slog"
	"time"
)

func (b *Backend) deliveryKind() string {
	if b.humanNoticeDelivery {
		return "human_notice"
	}
	if b.notificationDelivery {
		return "notification"
	}
	return "reply"
}
func (b *Backend) deliveryPath() string {
	if b.humanNoticeDelivery {
		return "/api/internal/v1/human-notices"
	}
	if b.notificationDelivery {
		return "/api/internal/v1/trigger-notifications"
	}
	return "/api/internal/v1/channel-deliveries"
}
func (b *Backend) deliveryQueryPath() string {
	if b.humanNoticeDelivery {
		return "/api/internal/v1/human-notices/queries"
	}
	if b.notificationDelivery {
		return "/api/internal/v1/trigger-notification-queries"
	}
	return "/api/internal/v1/channel-delivery-queries"
}

// TriggerNotifications has a separate durable Manager ledger, while sharing the
// proven provider attempt/evidence protocol. It never invents an inbound event.
type TriggerNotifications struct {
	Backend *Backend
	Lookup  func(string) (Binding, bool)
	Logger  *slog.Logger
}

func (*TriggerNotifications) Recover(context.Context, *election.Session) error { return nil }
func (w *TriggerNotifications) Run(ctx context.Context, _ *election.Session) error {
	b := *w.Backend
	b.notificationDelivery = true
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := RunDeliveries(ctx, &b, w.Lookup)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && w.Logger != nil {
			w.Logger.Warn("Trigger notification delivery pending", "event", "trigger.notification_retry")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// TriggerNotificationContexts resolves sent facts independently of transport.
type TriggerNotificationContexts struct{ TriggerNotifications }

func (w *TriggerNotificationContexts) Run(ctx context.Context, _ *election.Session) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.runContexts(ctx, w.Backend); err != nil && w.Logger != nil {
				w.Logger.Warn("Notification context pending", "event", "trigger.context_retry")
			}
		}
	}
}
