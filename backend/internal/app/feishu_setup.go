package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func startFeishuSetup(ctx context.Context, s *service.FeishuSetupService, log *slog.Logger) func() {
	child, cancel := context.WithCancel(ctx)
	onError := func(err error) {
		code := "feishu_setup_background_failed"
		var failure *service.Error
		if errors.As(err, &failure) {
			code = failure.Code
		}
		log.Warn("Feishu setup background step failed", "event", "channel.feishu_setup_failed", "error_code", code)
	}
	initial := runChannelMaintenance(child, time.Second, func() { s.AdvanceBackground(child, onError) })
	registrations := runChannelMaintenance(child, 5*time.Second, func() { s.AdvanceScheduledChecks(child, onError) })
	bindings := runChannelMaintenance(child, 5*time.Second, func() {
		for i := 0; i < 8 && child.Err() == nil; i++ {
			worked, err := s.CheckNextBinding(child)
			if err != nil && child.Err() == nil {
				onError(err)
			}
			if !worked || err != nil {
				break
			}
		}
	})
	return func() { cancel(); <-initial; <-registrations; <-bindings }
}

func startDingTalkSetup(ctx context.Context, s *service.DingTalkSetupService, log *slog.Logger) func() {
	child, cancel := context.WithCancel(ctx)
	done := runChannelMaintenance(child, 5*time.Second, func() {
		s.AdvanceBackground(child, func(err error) {
			code := "dingtalk_setup_background_failed"
			var failure *service.Error
			if errors.As(err, &failure) {
				code = failure.Code
			}
			log.Warn("DingTalk setup background step failed", "event", "channel.dingtalk_setup_failed", "error_code", code)
		})
	})
	return func() { cancel(); <-done }
}

func runChannelMaintenance(ctx context.Context, interval time.Duration, run func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for ctx.Err() == nil {
			run()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return done
}
