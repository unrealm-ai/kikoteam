package election

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func testConfig(dsn string) Config {
	return Config{DSN: dsn, ConnectTimeout: time.Second, RetryInterval: 20 * time.Millisecond,
		HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 200 * time.Millisecond}
}

func cleanupSession(t *testing.T, s *Session) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Close(ctx) // The test may have intentionally terminated the backend.
	})
}

func TestElectionHandoffAndQueuedWorkCancellation(t *testing.T) {
	dsn, _ := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	leader, err := Acquire(ctx, testConfig(dsn), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, leader)
	type result struct {
		s   *Session
		err error
	}
	next := make(chan result, 1)
	standby := make(chan struct{}, 1)
	go func() {
		s, err := Acquire(ctx, testConfig(dsn), func(state string) {
			if state == "standby" {
				select {
				case standby <- struct{}{}:
				default:
				}
			}
		})
		next <- result{s, err}
	}()
	select {
	case <-standby:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case got := <-next:
		if got.s != nil {
			cleanupSession(t, got.s)
		}
		t.Fatal("standby elected before release")
	default:
	}
	entered, finish := make(chan struct{}), make(chan struct{})
	active := make(chan error, 1)
	go func() {
		active <- leader.Do(ctx, time.Second, func(context.Context, *sql.Conn) error {
			close(entered)
			<-finish
			return nil
		})
	}()
	<-entered
	var ran atomic.Bool
	queued := make(chan error, 1)
	go func() {
		queued <- leader.Do(ctx, time.Second, func(context.Context, *sql.Conn) error { ran.Store(true); return nil })
	}()
	leader.Stop()
	closeCtx, closeCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	if err := leader.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("closed before worker finished: %v", err)
	}
	closeCancel()
	select {
	case got := <-next:
		if got.s != nil {
			cleanupSession(t, got.s)
		}
		t.Fatal("lock released before active work finished")
	default:
	}
	close(finish)
	<-active //nolint:errcheck // Stop deliberately cancels this operation; this assertion only waits for it to release the gate.
	if err := <-queued; err == nil || ran.Load() {
		t.Fatalf("queued work ran after stop: %v", err)
	}
	if err := leader.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-next:
		if got.err != nil {
			t.Fatal(got.err)
		}
		cleanupSession(t, got.s)
		if got.s.PID() == leader.PID() {
			t.Fatal("new leader retained old session")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestConnectionLossStopsLeaderAndAllowsTakeover(t *testing.T) {
	dsn, db := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := testConfig(dsn)
	leader, err := Acquire(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, leader)
	monitor := make(chan error, 1)
	go func() { monitor <- leader.Monitor(ctx, cfg.HeartbeatInterval, cfg.HeartbeatTimeout) }()
	if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1)", leader.PID()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-monitor:
		if err == nil {
			t.Fatal("monitor missed connection loss")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if leader.Context().Err() == nil {
		t.Fatal("leader context not canceled")
	}
	ran := false
	if err := leader.Do(ctx, time.Second, func(context.Context, *sql.Conn) error { ran = true; return nil }); err == nil || ran {
		t.Fatal("old leader continued work")
	}
	// The ordinary pool is healthy, but cannot be used to replace the lost session.
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, next)
}

func TestHeartbeatDoesNotInterruptMigration(t *testing.T) {
	dsn, _ := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	leader, err := Acquire(ctx, testConfig(dsn), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, leader)
	entered := make(chan struct{})
	query := make(chan error, 1)
	go func() {
		query <- leader.Do(ctx, time.Second, func(ctx context.Context, c *sql.Conn) error {
			close(entered)
			_, err := c.ExecContext(ctx, "SELECT pg_sleep(0.25)")
			return err
		})
	}()
	<-entered
	monitorCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- leader.Monitor(monitorCtx, 10*time.Millisecond, 40*time.Millisecond) }()
	if err := <-query; err != nil {
		t.Fatal(err)
	}
	if leader.Context().Err() != nil {
		t.Fatal("waiting heartbeat expired during migration")
	}
	stop()
	<-done //nolint:errcheck // The monitor was deliberately canceled after the heartbeat assertions above.
}

func TestDrainRejectsQueuedWorkButStillAllowsForcedStop(t *testing.T) {
	dsn, _ := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Acquire(ctx, testConfig(dsn), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, s)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Do(ctx, 4*time.Second, func(ctx context.Context, c *sql.Conn) error {
			close(entered)
			_, err := c.ExecContext(ctx, "SELECT pg_sleep(3)")
			return err
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	s.Drain()
	ran := false
	if err := s.Do(ctx, time.Second, func(context.Context, *sql.Conn) error { ran = true; return nil }); !errors.Is(err, ErrStopping) || ran {
		t.Fatalf("draining session admitted work: %v", err)
	}
	s.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("forced stop was suppressed by Drain")
		}
	case <-time.After(time.Second):
		t.Fatal("forced stop did not cancel admitted SQL")
	}
}
