package service

import (
	"context"
	"sync"
)

func (s *DingTalkSetupService) AdvanceBackground(ctx context.Context, onError func(error)) {
	store, ok := s.Store.(interface {
		DueDingTalkRegistrations(context.Context) ([]DingTalkRegistration, error)
	})
	if !ok {
		return
	}
	due, err := store.DueDingTalkRegistrations(ctx)
	if err != nil {
		onError(err)
		return
	}
	var work sync.WaitGroup
	for _, r := range due {
		work.Go(func() {
			if err := s.advanceRegistration(ctx, r); err != nil && ctx.Err() == nil {
				onError(err)
			}
		})
	}
	work.Wait()
}

func (s *DingTalkSetupService) advanceRegistration(ctx context.Context, r DingTalkRegistration) error {
	if r.State == "awaiting_confirmation" {
		status, err := s.Poll(ctx, r.WorkspaceID, r.ActorID, r.AgentID, r.ID)
		if err != nil || status.Registration == nil {
			return err
		}
		r = *status.Registration
	}
	if r.State == "ready" {
		_, err := s.Confirm(ctx, r.WorkspaceID, r.ActorID, r.AgentID, r.ID)
		return err
	}
	return nil
}
