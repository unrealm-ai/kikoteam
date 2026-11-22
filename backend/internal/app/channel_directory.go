package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/dingtalkdirectory"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishudirectory"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func startChannelDirectory(ctx context.Context, s *service.ChannelService, log *slog.Logger) func() {
	child, cancel := context.WithCancel(ctx)
	s.DirectoryWake = make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		fetcher := channelDirectoryFetcher{}
		for {
			for i := 0; i < 8; i++ {
				worked, err := s.SyncNextDirectory(child, fetcher)
				if child.Err() != nil {
					return
				}
				if err != nil {
					log.Warn("Channel directory sync failed", "event", "channel.directory_sync_failed", "error_code", "directory_sync_failed")
				}
				if !worked {
					break
				}
			}
			select {
			case <-child.Done():
				return
			case <-timer.C:
			case <-s.DirectoryWake:
			}
		}
	}()
	return func() { cancel(); <-done }
}

type channelDirectoryFetcher struct{}

func (channelDirectoryFetcher) Fetch(ctx context.Context, app, secret, tenant string) (service.DirectorySnapshot, error) {
	return (feishudirectory.Client{}).Fetch(ctx, app, secret, tenant)
}
func (channelDirectoryFetcher) FetchProvider(ctx context.Context, provider, app, secret, tenant string) (service.DirectorySnapshot, error) {
	switch provider {
	case "feishu":
		return (feishudirectory.Client{}).Fetch(ctx, app, secret, tenant)
	case "dingtalk":
		return (dingtalkdirectory.Client{}).Fetch(ctx, app, secret)
	}
	return service.DirectorySnapshot{}, &service.DirectoryFetchError{Code: "directory_provider_unsupported", Invalidate: true}
}
