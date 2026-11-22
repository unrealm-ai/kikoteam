package postgres

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestPreparationFailureAndCanceledInitializationRetainConnection(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	files := fstest.MapFS{"000007_invalid.sql": {Data: []byte("SELECT 'secret SQL body without a goose Up annotation';")}}
	p, err := goose.NewProviderWithConn(goose.DialectPostgres, c, files, goose.WithSlog(quiet()))
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Status(canceled); err == nil {
		t.Fatal("canceled initialization succeeded")
	}
	if _, err := p.Status(ctx); err != nil {
		t.Fatalf("failed initialization poisoned provider: %v", err)
	}
	_, err = p.Up(ctx)
	var preparation *goose.PreparationError
	if !errors.As(err, &preparation) || preparation.Version != 7 {
		t.Fatalf("parse failure did not preserve migration identity: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.PingContext(ctx); err != nil {
		t.Fatalf("failure closed borrowed connection: %v", err)
	}
}

func TestVersionWriteFailureRollsBackMigration(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	files := fstest.MapFS{"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE initial (id integer);")}}
	if err := Migrate(ctx, c, files, quiet()); err != nil {
		t.Fatal(err)
	}
	// Failure occurs specifically when Goose writes the success record, after DDL.
	if _, err := c.ExecContext(ctx, `CREATE FUNCTION reject_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.version_id = 2 THEN RAISE EXCEPTION 'test version write failure'; END IF; RETURN NEW; END;
$$;
CREATE TRIGGER reject_version BEFORE INSERT ON goose_db_version FOR EACH ROW EXECUTE FUNCTION reject_version();`); err != nil {
		t.Fatal(err)
	}
	files["000002_second.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE must_rollback (id integer);")}
	if err := Migrate(ctx, c, files, quiet()); err == nil {
		t.Fatal("expected version insert failure")
	}
	var missing bool
	if err := c.QueryRowContext(ctx, "SELECT to_regclass('must_rollback') IS NULL").Scan(&missing); err != nil || !missing {
		t.Fatalf("DDL was not atomic with version write: missing=%v err=%v", missing, err)
	}
}

func TestInterruptedMigrationNeverReconnectsAndNextSessionResumes(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	files := fstest.MapFS{
		"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE committed (id integer);")},
		"000002_slow.sql": {Data: []byte("-- +goose Up\nCREATE TABLE in_progress (id integer);\nSELECT pg_sleep(1);")},
	}
	p, err := goose.NewProviderWithConn(goose.DialectPostgres, c, files, goose.WithSlog(quiet()))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.Up(ctx); done <- err }()
	for {
		var sleeping bool
		if err := db.QueryRowContext(ctx, "SELECT state='active' AND query LIKE 'SELECT pg_sleep%' FROM pg_stat_activity WHERE pid=$1", pid).Scan(&sleeping); err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("migration ended before interruption: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted migration succeeded")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := p.Up(ctx); err == nil {
		t.Fatal("provider reconnected after losing borrowed connection")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	var firstExists, secondMissing bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('committed') IS NOT NULL, to_regclass('in_progress') IS NULL").Scan(&firstExists, &secondMissing); err != nil || !firstExists || !secondMissing {
		t.Fatalf("interruption lost committed work or kept uncommitted work: %v %v %v", firstExists, secondMissing, err)
	}
	next, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }() // Best-effort cleanup of the disposable recovery connection.
	var acquired bool
	if err := next.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(1234,5678)").Scan(&acquired); err != nil || !acquired {
		t.Fatalf("takeover lock: %v %v", acquired, err)
	}
	if err := Migrate(ctx, next, files, quiet()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := next.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id>0 AND is_applied").Scan(&count); err != nil || count != 2 {
		t.Fatalf("resumed history: %d %v", count, err)
	}
}
