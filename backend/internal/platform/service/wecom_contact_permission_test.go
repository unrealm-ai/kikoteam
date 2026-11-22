package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWecomContactPermissionCacheDeduplicatesAndExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var cache wecomContactPermissionCache
	key := wecomContactPermissionKey{id: "binding", generation: 1, revision: 1}
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	probe := func() WecomContactPermission {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-release:
		}
		return WecomContactPermission{Status: "unavailable"}
	}
	done := make(chan error, 2)
	run := func() {
		v, err := cache.get(ctx, key, probe)
		if v.Status != "unavailable" && err == nil {
			err = Fail("test_outcome", 500, "unexpected outcome")
		}
		done <- err
	}
	go run()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go run()
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate provider call")
	}
	cache.mu.Lock()
	ttl := time.Until(cache.entries[key].expiry)
	cache.entries[key].expiry = time.Time{}
	cache.mu.Unlock()
	if ttl <= 0 || ttl > 5*time.Second {
		t.Fatalf("transient retry delay = %v", ttl)
	}
	if _, err := cache.get(ctx, key, probe); err != nil || calls.Load() != 2 {
		t.Fatal("expired outcome was not retried", err)
	}
}
