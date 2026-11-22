package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

type activeSQLWork struct {
	started  chan int
	afterSQL chan error
	finish   chan struct{}
}

func (w *activeSQLWork) Recover(context.Context, *election.Session) error { return nil }
func (w *activeSQLWork) Run(ctx context.Context, s *election.Session) error {
	err := s.Do(ctx, time.Second, func(ctx context.Context, c *sql.Conn) error {
		w.started <- s.PID()
		_, err := c.ExecContext(ctx, "SELECT pg_sleep(0.3)")
		return err
	})
	w.afterSQL <- err
	<-w.finish // Simulate channel cleanup after the admitted SQL has finished.
	return err
}

func TestGracefulStopDuringSQLRetainsLockUntilCleanup(t *testing.T) {
	dsn, db := testpg.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	w := &activeSQLWork{started: make(chan int, 1), afterSQL: make(chan error, 1), finish: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config(dsn), fstest.MapFS{}, w, testLogger()) }()
	t.Cleanup(func() {
		cancel()
		close(w.finish)
		select {
		case <-done: //nolint:errcheck // Cleanup joins the worker; the test asserts its observable shutdown behavior separately.
		case <-time.After(3 * time.Second):
			t.Error("cleanup timed out")
		}
	})
	check, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	var pid int
	select {
	case pid = <-w.started:
	case <-check.Done():
		t.Fatal(check.Err())
	}
	for {
		var active bool
		if err := db.QueryRowContext(check, "SELECT state='active' AND query LIKE 'SELECT pg_sleep%' FROM pg_stat_activity WHERE pid=$1", pid).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-check.Done():
			t.Fatal(check.Err())
		}
	}
	cancel()
	select {
	case err := <-w.afterSQL:
		if err != nil {
			t.Errorf("graceful stop aborted admitted SQL: %v", err)
		}
	case <-check.Done():
		t.Fatal(check.Err())
	}
	// Give PostgreSQL time to observe a client-side close; checking immediately
	// after Exec returns can miss premature release while pg_sleep is still active.
	select {
	case <-time.After(350 * time.Millisecond):
	case <-check.Done():
		t.Fatal(check.Err())
	}
	var held bool
	if err := db.QueryRowContext(check, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory')", pid).Scan(&held); err != nil || !held {
		t.Fatalf("lock released before channel cleanup: held=%v err=%v", held, err)
	}
}

type cleanupFailureWork struct {
	started chan struct{}
	err     error
}

func (w *cleanupFailureWork) Recover(context.Context, *election.Session) error { return nil }
func (w *cleanupFailureWork) Run(ctx context.Context, _ *election.Session) error {
	close(w.started)
	<-ctx.Done()
	return w.err
}

func TestShutdownReportsWorkerCleanupFailure(t *testing.T) {
	dsn, _ := testpg.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("channel cleanup failed")
	w := &cleanupFailureWork{started: make(chan struct{}), err: errors.Join(context.Canceled, failure)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config(dsn), fstest.MapFS{}, w, testLogger()) }()
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatal("start timeout")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, failure) || errors.Is(err, context.Canceled) {
			t.Fatalf("cleanup failure was swallowed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop timeout")
	}
}

type stuckWork struct {
	started chan *election.Session
	finish  chan struct{}
	ended   chan struct{}
}

func (w *stuckWork) Recover(context.Context, *election.Session) error { return nil }
func (w *stuckWork) Run(_ context.Context, s *election.Session) error {
	w.started <- s
	<-w.finish // Deliberately violate the cancellation contract to test the last defense.
	close(w.ended)
	return nil
}

func TestShutdownDeadlineDoesNotUnlockUnderRunningWork(t *testing.T) {
	dsn, db := testpg.New(t)
	w := &stuckWork{started: make(chan *election.Session, 1), finish: make(chan struct{}), ended: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config(dsn)
	cfg.ShutdownTimeout = 80 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, fstest.MapFS{}, w, testLogger()) }()
	var session *election.Session
	select {
	case session = <-w.started:
	case err := <-done:
		t.Fatalf("start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("start timed out")
	}
	t.Cleanup(func() {
		close(w.finish)
		<-w.ended
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = session.Close(cleanup)
	})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrShutdownTimeout) {
			t.Fatalf("expected shutdown timeout: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown deadline ignored")
	}
	var held bool
	check, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := db.QueryRowContext(check, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory')", session.PID()).Scan(&held); err != nil || !held {
		t.Fatalf("lock released under uncooperative worker: held=%v err=%v", held, err)
	}
	// Production main exits here; only this test releases the deliberately stuck work.
}
