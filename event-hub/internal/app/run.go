package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/database/postgres"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

// Work is the future channel/scheduler integration boundary. Recover must finish
// before Run starts. Run must close its external connections and join its goroutines
// before returning after cancellation. It must not retain Session's raw SQL connection.
type Work interface {
	Recover(context.Context, *election.Session) error
	Run(context.Context, *election.Session) error
}

var ErrShutdownTimeout = errors.New("shutdown timed out; process must terminate")

// Run executes one elected term. Once elected, failures exit instead of re-electing
// inside potentially still-running business work. A nil Work runs infrastructure only.
//
//nolint:contextcheck // The elected term and bounded shutdown outlive caller cancellation to drain before unlocking.
func Run(ctx context.Context, cfg Config, files fs.FS, work Work, logger *slog.Logger) (retErr error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	if files == nil {
		return errors.New("migration filesystem is required")
	}
	if _, err := postgres.ValidateFiles(files); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	health := &Health{}
	set := func(phase string, pid int) {
		if health.set(phase, pid, work != nil) {
			logger.Info("lifecycle changed", "event", "lifecycle.changed", "phase", phase, "session_pid", pid)
		}
	}
	set("starting", 0)
	listener, err := net.Listen("tcp", cfg.HealthAddr) //nolint:noctx // Process-owned probe listener; server.Shutdown below bounds its lifetime.
	if err != nil {
		return fmt.Errorf("listen for health checks: %w", err)
	}
	server := &http.Server{Handler: health.Handler(logger), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serverDone := make(chan struct{})
	var shutdownDeadline time.Time
	go func() {
		defer close(serverDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel(fmt.Errorf("health server stopped: %w", err))
		}
	}()
	defer func() {
		if retErr != nil {
			set("failed", 0)
		} else {
			set("stopped", 0)
		}
		if shutdownDeadline.IsZero() {
			shutdownDeadline = time.Now().Add(cfg.ShutdownTimeout)
		}
		shutdown, stopShutdown := context.WithDeadline(context.Background(), shutdownDeadline)
		defer stopShutdown()
		if err := server.Shutdown(shutdown); err != nil {
			retErr = errors.Join(retErr, err, server.Close())
		}
		<-serverDone
	}()
	session, err := election.Acquire(runCtx, cfg.Election, func(phase string) { set(phase, 0) })
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.Join(err, context.Cause(runCtx))
	}
	workCtx, stopWork := context.WithCancel(session.Context())
	defer stopWork()
	workerDone := make(chan error, 1)
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- session.Monitor(workCtx, cfg.Election.HeartbeatInterval, cfg.Election.HeartbeatTimeout)
	}()
	go func() {
		set("migrating", session.PID())
		err := session.Do(workCtx, cfg.MigrationTimeout, func(ctx context.Context, conn *sql.Conn) error {
			logger.Info("migration started", "event", "migration.started", "session_pid", session.PID())
			return postgres.Migrate(ctx, conn, files, logger)
		})
		if err != nil {
			workerDone <- fmt.Errorf("migrate: %w", err)
			return
		}
		if workCtx.Err() != nil {
			workerDone <- workCtx.Err()
			return
		}
		if work != nil {
			set("recovering", session.PID())
			if err := work.Recover(workCtx, session); err != nil {
				workerDone <- fmt.Errorf("recover: %w", err)
				return
			}
		}
		if workCtx.Err() != nil {
			workerDone <- workCtx.Err()
			return
		}
		set("running", session.PID())
		if work == nil {
			<-workCtx.Done()
			workerDone <- nil
			return
		}
		workerDone <- work.Run(workCtx, session)
	}()
	workerFinished, monitorFinished := false, false
	select {
	case <-runCtx.Done():
		if ctx.Err() == nil {
			retErr = context.Cause(runCtx)
		}
	case <-session.Context().Done():
		retErr = context.Cause(session.Context())
	case err := <-workerDone:
		workerFinished = true
		if err == nil {
			err = errors.New("leader worker exited unexpectedly")
		}
		retErr = err
	case err := <-monitorDone:
		monitorFinished = true
		retErr = fmt.Errorf("monitor: %w", err)
	}
	set("stopping", session.PID())
	graceful := ctx.Err() != nil && retErr == nil && session.Context().Err() == nil
	if graceful {
		// Close admission before canceling work. Aborting an admitted pgx query
		// could close the session and let a standby start before channel cleanup.
		session.Drain()
	} else {
		session.Stop()
	}
	stopWork()
	shutdown, stopShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer stopShutdown()
	shutdownDeadline, _ = shutdown.Deadline()
	for !workerFinished || !monitorFinished {
		select {
		case <-shutdown.Done():
			// Do not unlock while a worker may still be producing external effects.
			return errors.Join(retErr, ErrShutdownTimeout)
		case err := <-workerDone:
			workerFinished = true
			retErr = errors.Join(retErr, shutdownError(err))
		case err := <-monitorDone:
			monitorFinished = true
			retErr = errors.Join(retErr, shutdownError(err))
		}
	}
	// Do not hide a lost session behind a simultaneous termination signal.
	if errors.Is(context.Cause(session.Context()), election.ErrLost) {
		retErr = errors.Join(retErr, context.Cause(session.Context()))
	}
	return errors.Join(retErr, session.Close(shutdown))
}

// Only expected cancellation/closed-admission results are normal during shutdown.
// A joined error may also contain a real cleanup failure, so filter leaf errors.
func shutdownError(err error) error {
	if err == nil || err == context.Canceled || err == election.ErrStopping { //nolint:errorlint // Match leaves only; errors.Is on a join would discard unrelated cleanup failures.
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result error
		for _, child := range joined.Unwrap() {
			result = errors.Join(result, shutdownError(child))
		}
		return result
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && shutdownError(wrapped.Unwrap()) == nil {
		return nil
	}
	return err
}
