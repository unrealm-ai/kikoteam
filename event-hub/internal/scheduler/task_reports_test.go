package scheduler

import (
	"context"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channel"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReportsImmediateRecoveryRetryAndCancellation(t *testing.T) {
	var calls atomic.Int32
	reached := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/v1/task-results/reconcile" || r.Header.Get("Authorization") != "Bearer hub-test" {
			t.Error("unexpected request")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		select {
		case reached <- struct{}{}:
		default:
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	b, err := channel.NewBackend(server.URL, "hub-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := TaskReports{Backend: b, Interval: 10 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, nil) }()
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		t.Fatal("no recovery/retry")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	count := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != count {
		t.Fatal("requests after cancellation")
	}
}
