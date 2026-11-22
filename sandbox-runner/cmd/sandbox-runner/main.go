package main

import (
	"context"
	"fmt"
	"github.com/unrealm-ai/kikoteam/sandbox-runner/internal/runner"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) != 3 || os.Args[1] != "--config" {
		fmt.Fprintln(os.Stderr, "expected --config file")
		os.Exit(1)
	}
	if err := runner.Run(ctx, os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "Runner failed; environment requires reconciliation")
		os.Exit(1)
	}
}
