package main

import (
	"context"
	"crypto/rand"
	"os"
	"os/signal"
	"syscall"

	"github.com/unrealm-ai/kikoteam/event-hub/database/postgres"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/app"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/scheduler"
)

var version = "dev"

func main() {
	logger := app.NewLogger().With("version", version, "instance_id", rand.Text(), "process_pid", os.Getpid())
	cfg, err := app.LoadConfig(os.Getenv)
	if err != nil {
		logger.Error("configuration invalid; check EVENT_HUB settings", "event", "config.failed", "reason", err.Error())
		os.Exit(1)
	}
	channels, err := channel.FromEnvironment(os.Getenv, logger)
	if err != nil {
		logger.Error("channel configuration invalid", "event", "config.failed")
		os.Exit(1)
	}
	var work app.Work
	if channels != nil {
		work = app.WorkGroup{channels, &channel.SharedGroupHistoryWorker{Backend: channels.Backend, Lookup: channels.Current, Logger: logger}, &channel.WecomGroupRefreshWorker{Backend: channels.Backend, Lookup: channels.Current, Logger: logger}, &scheduler.TaskReports{Backend: channels.Backend, Logger: logger}, &scheduler.ModelUsage{Backend: channels.Backend, Logger: logger}, &scheduler.Triggers{Backend: channels.Backend, Logger: logger}, &channel.HumanNotices{Backend: channels.Backend, Lookup: channels.Current, Logger: logger}, &channel.TriggerNotifications{Backend: channels.Backend, Lookup: channels.Current, Logger: logger}, &channel.TriggerNotificationContexts{TriggerNotifications: channel.TriggerNotifications{Backend: channels.Backend, Lookup: channels.Current, Logger: logger}}}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, postgres.Files(), work, logger); err != nil {
		logger.Error("event hub exited", append([]any{"event", "process.failed"}, app.ErrorAttrs(err)...)...)
		os.Exit(1) // Also terminates workers that exceeded the shutdown budget.
	}
}
