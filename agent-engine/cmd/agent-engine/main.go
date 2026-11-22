package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/app"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "agent-engine")
	cfg, err := app.Load()
	if err != nil {
		log.Error("Invalid Engine configuration", "event", "startup.invalid_config", "reason", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = app.Run(ctx, cfg, log); err != nil {
		log.Error("Engine stopped unexpectedly", "event", "runtime.failed")
		os.Exit(1)
	}
}
