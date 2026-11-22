package service

import (
	"context"
	"sync"
)

// AdvanceBackground drives only this process's initial wait and first check.
// Persisted pending registrations from another process are interrupted, never resumed.
// Subsequent diagnostics are driven by the frontend, not this ticker.
func (s *FeishuSetupService) AdvanceBackground(ctx context.Context, onError func(error)) {
	rows := []FeishuRegistration{}
	s.activeRegistrations.Range(func(_, v any) bool {
		r, ok := v.(FeishuRegistration)
		if ok {
			rows = append(rows, r)
		}
		return true
	})
	var done sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, r := range rows {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			done.Wait()
			return
		}
		done.Add(1)
		go func(r FeishuRegistration) {
			defer done.Done()
			defer func() { <-slots }()
			err := s.with(ctx, r.WorkspaceID, r.ActorID, r.AgentID, func(t FeishuSetupTx) error {
				var e error
				r, e = setupOwned(ctx, t, r.ID, r.ActorID, r.AgentID)
				return e
			})
			if err != nil {
				s.activeRegistrations.Delete(r.ID)
				return
			}
			if err := s.advanceRegistration(ctx, r); err != nil && ctx.Err() == nil {
				onError(err)
			}
		}(r)
	}
	done.Wait()
}
func setupPending(state string) bool {
	return state == "starting" || state == "awaiting_confirmation" || state == "polling"
}
func (s *FeishuSetupService) registrationActive(id string) bool {
	_, ok := s.activeRegistrations.Load(id)
	return ok
}
func (s *FeishuSetupService) advanceRegistration(ctx context.Context, r FeishuRegistration) error {
	if setupPending(r.State) && !s.registrationActive(r.ID) {
		_, err := s.finish(ctx, r, func(_ FeishuSetupTx, current *FeishuRegistration) error {
			current.State = "interrupted"
			current.ErrorCode = "feishu_registration_outcome_unknown"
			current.DeviceCipher = nil
			current.VerificationURL = ""
			return nil
		})
		return err
	}
	switch r.State {
	case "awaiting_confirmation":
		result, err := s.Poll(ctx, r.WorkspaceID, r.ActorID, r.AgentID, r.ID)
		if err != nil {
			return err
		}
		if result.Registration == nil {
			return nil
		}
		r = *result.Registration
		if r.State != "credentials_saved" {
			if !setupPending(r.State) {
				s.activeRegistrations.Delete(r.ID)
			}
			return nil
		}
	case "credentials_saved":
	default:
		if !setupPending(r.State) {
			s.activeRegistrations.Delete(r.ID)
		}
		return nil
	}
	// LoadAndDelete permits exactly one initial check even with concurrent ticks.
	if _, claimed := s.activeRegistrations.LoadAndDelete(r.ID); !claimed {
		return nil
	}
	_, err := s.retry(ctx, r.WorkspaceID, r.ActorID, r.AgentID, r.ID, true)
	return err
}

func (s *FeishuSetupService) checkActive(r FeishuRegistration) bool {
	_, local := s.activeChecks.Load(r.ID)
	return local || r.CheckLeaseUntil != nil && s.now().Before(*r.CheckLeaseUntil)
}

// AdvanceScheduledChecks resumes only persisted, safely repeatable checks.
// The per-registration lease is acquired by retry itself.
func (s *FeishuSetupService) AdvanceScheduledChecks(ctx context.Context, onError func(error)) {
	store, ok := s.Store.(interface {
		DueFeishuRegistrations(context.Context) ([]FeishuRegistration, error)
	})
	if !ok {
		return
	}
	rows, err := store.DueFeishuRegistrations(ctx)
	if err != nil {
		onError(err)
		return
	}
	var work sync.WaitGroup
	for _, r := range rows {
		work.Go(func() {
			_, err := s.retry(ctx, r.WorkspaceID, r.ActorID, r.AgentID, r.ID, true)
			if err != nil && ctx.Err() == nil {
				onError(err)
			}
		})
	}
	work.Wait()
}
