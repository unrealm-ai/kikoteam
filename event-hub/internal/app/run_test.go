package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func config(dsn string) Config {
	c, _ := LoadConfig(func(key string) string { //nolint:errcheck // This fixture supplies a valid DSN and uses validated defaults.
		if key == "EVENT_HUB_DATABASE_URL" {
			return dsn
		}
		return ""
	})
	c.HealthAddr = "127.0.0.1:0"
	c.Election.RetryInterval = 20 * time.Millisecond
	c.Election.HeartbeatInterval = 20 * time.Millisecond
	c.Election.HeartbeatTimeout = 200 * time.Millisecond
	c.ShutdownTimeout = 2 * time.Second
	return c
}

type worker struct {
	db      *sql.DB
	started chan int
	stopped chan bool
}

func (w *worker) Recover(ctx context.Context, s *election.Session) error {
	return s.Do(ctx, time.Second, func(ctx context.Context, c *sql.Conn) error {
		var count int
		return c.QueryRowContext(ctx, "SELECT count(*) FROM migrated").Scan(&count)
	})
}
func (w *worker) Run(ctx context.Context, s *election.Session) error {
	w.started <- s.PID()
	<-ctx.Done()
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var held bool
	err := w.db.QueryRowContext(cleanup, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory')", s.PID()).Scan(&held)
	w.stopped <- err == nil && held
	return nil
}

func TestRunMigratesBeforeWorkAndReleasesAfterCleanup(t *testing.T) {
	dsn, db := testpg.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &worker{db: db, started: make(chan int, 1), stopped: make(chan bool, 1)}
	files := fstest.MapFS{"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE migrated (id integer);")}}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config(dsn), files, w, testLogger()) }()
	var pid int
	select {
	case pid = <-w.started:
	case err := <-done:
		t.Fatalf("stopped before work: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("start timeout")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop timeout")
	}
	if held := <-w.stopped; !held {
		t.Fatal("lock released before worker cleanup")
	}
	var held bool
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory')", pid).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("lock survived process cleanup")
	}
}

func TestMigrationFailureDoesNotStartWork(t *testing.T) {
	dsn, db := testpg.New(t)
	w := &worker{db: db, started: make(chan int, 1), stopped: make(chan bool, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, config(dsn), fstest.MapFS{"000001_bad.sql": {Data: []byte("-- +goose Up\nSELECT 1/0;")}}, w, testLogger())
	if err == nil {
		t.Fatal("migration failure was ignored")
	}
	select {
	case <-w.started:
		t.Fatal("worker started after failed migration")
	default:
	}
}

func TestLostSessionCancelsWork(t *testing.T) {
	dsn, db := testpg.New(t)
	w := &worker{db: db, started: make(chan int, 1), stopped: make(chan bool, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, config(dsn), fstest.MapFS{"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE migrated (id integer);")}}, w, testLogger())
	}()
	select {
	case pid := <-w.started:
		if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("start failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost leader returned success")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-w.stopped:
	default:
		t.Fatal("worker not stopped")
	}
}

func TestHealthAndConfiguration(t *testing.T) {
	h := &Health{}
	for _, tc := range []struct {
		phase, path string
		want        int
	}{
		{"standby", "/livez", 200}, {"standby", "/readyz", 200}, {"migrating", "/readyz", 503},
		{"running", "/readyz", 200}, {"stopping", "/readyz", 503}, {"failed", "/livez", 503},
	} {
		h.set(tc.phase, 0, false)
		w := httptest.NewRecorder()
		h.Handler(testLogger()).ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.want || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s %s: %d", tc.phase, tc.path, w.Code)
		}
		if w.Code >= 400 {
			var response struct {
				Error struct {
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error.RequestID != w.Header().Get("X-Request-ID") {
				t.Fatalf("response correlation mismatch: %s", w.Body.String())
			}
		}
	}
	if h.set("running", 12, true) {
		t.Fatal("late worker made failed process ready")
	}
	if _, err := LoadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing DSN accepted")
	}
	if _, err := LoadConfig(func(key string) string {
		if key == "EVENT_HUB_HEARTBEAT_TIMEOUT" {
			return "-1s"
		}
		return "postgres://localhost/db"
	}); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestErrorLoggingDoesNotExposeSQLOrCredentials(t *testing.T) {
	var out strings.Builder
	l := slog.New(slog.NewJSONHandler(&out, nil))
	err := errors.Join(errors.New("postgres://user:secret@host/db"), &pgconn.PgError{Code: "22012", Message: "SELECT secret_body", Detail: "private data"})
	l.Error("operation failed", ErrorAttrs(err)...)
	if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "private") || !strings.Contains(out.String(), "22012") {
		t.Fatal(out.String())
	}
}
