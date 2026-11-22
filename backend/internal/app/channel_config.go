package app

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"log/slog"
	"os"
	"strings"
)

func loadHubToken() (string, error) {
	v := strings.TrimSpace(os.Getenv("KIKO_HUB_TOKEN"))
	if v == "" {
		return "", nil
	}
	if len(v) < 32 || strings.ContainsAny(v, "\r\n") {
		return "", errors.New("KIKO_HUB_TOKEN must have at least 32 bytes")
	}
	return v, nil
}
func startChannelNotifications(ctx context.Context, s service.ChannelStore, token string, log *slog.Logger) func() {
	target := strings.TrimSpace(os.Getenv("KIKO_HUB_CONTROL_URL"))
	if target == "" || token == "" {
		return func() {}
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n := &service.ChannelNotifier{Store: s, URL: target, Token: token}
		n.Run(child, func() { log.Warn("Channel refresh pending", "event", "channel.notification_pending") })
	}()
	return func() { cancel(); <-done }
}
