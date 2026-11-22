package app

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

// WorkGroup shares one elected term; all children stop before releasing it.
type WorkGroup []Work

func (g WorkGroup) Recover(ctx context.Context, s *election.Session) error {
	for _, w := range g {
		if err := w.Recover(ctx, s); err != nil {
			return err
		}
	}
	return nil
}
func (g WorkGroup) Run(ctx context.Context, s *election.Session) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, len(g))
	for _, w := range g {
		go func() { done <- w.Run(ctx, s) }()
	}
	var result error
	for range g {
		err := <-done
		if ctx.Err() == nil && err == nil {
			err = errors.New("elected worker exited unexpectedly")
		}
		result = errors.Join(result, err)
		cancel()
	}
	return result
}
