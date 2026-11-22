package postgres

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func borrowed(t *testing.T, db *sql.DB) (*sql.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.ExecContext(ctx, "SELECT pg_advisory_lock(1234, 5678)"); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func TestBorrowedConnectionAndVersionAtomicity(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	var pid int
	if err := c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"000001_create_probe.sql": {Data: []byte(`-- +goose Up
CREATE TABLE probe (session_id integer, locks integer);
INSERT INTO probe SELECT pg_backend_pid(), count(*) FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory';`)}}
	p, err := goose.NewProviderWithConn(goose.DialectPostgres, c, files, goose.WithTableName(VersionTable), goose.WithSlog(quiet()))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, err := p.HasPending(ctx); err != nil || pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if version, err := p.GetDBVersion(ctx); err != nil || version != 1 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	var recorded, locks int
	if err := c.QueryRowContext(ctx, "SELECT session_id, locks FROM probe").Scan(&recorded, &locks); err != nil {
		t.Fatal(err)
	}
	if recorded != pid {
		t.Fatalf("migration changed session: %d != %d", recorded, pid)
	}
	if locks != 1 {
		t.Fatalf("migration acquired additional advisory locks: %d", locks)
	}
	if err := c.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid() AND locktype='advisory'").Scan(&locks); err != nil {
		t.Fatal(err)
	}
	if locks != 1 {
		t.Fatalf("want only election lock, got %d", locks)
	}
	var canAcquire bool
	if err := db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(1234,5678)").Scan(&canAcquire); err != nil {
		t.Fatal(err)
	}
	if canAcquire {
		t.Fatal("Goose released caller's election lock")
	}
	files["000002_fail.sql"] = &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE should_rollback (id integer);
SELECT 1/0;`)}
	if err := Migrate(ctx, c, files, quiet()); err == nil {
		t.Fatal("expected migration failure")
	}
	var rolledBack bool
	if err := c.QueryRowContext(ctx, "SELECT to_regclass('public.should_rollback') IS NULL").Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if !rolledBack {
		t.Fatal("failed DDL survived rollback")
	}
	var count int
	if err := c.QueryRowContext(ctx, "SELECT count(*) FROM public.goose_db_version WHERE version_id=2 AND is_applied").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration recorded as applied")
	}
	files["000002_fail.sql"].Data = []byte("-- +goose Up\nCREATE TABLE recovered (id integer);")
	if err := Migrate(ctx, c, files, quiet()); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, c, files, quiet()); err != nil {
		t.Fatal(err)
	}
	delete(files, "000001_create_probe.sql")
	if err := Migrate(ctx, c, files, quiet()); err == nil {
		t.Fatal("missing historical file was ignored")
	}
	if err := Migrate(ctx, c, fstest.MapFS{}, quiet()); err == nil {
		t.Fatal("empty artifact ignored existing history")
	}
}

func TestNonTransactionalAndComplexSQL(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	files := fstest.MapFS{
		"000001_function.sql": {Data: []byte(`-- +goose Up
CREATE TABLE probe (id integer);
-- +goose StatementBegin
CREATE FUNCTION probe_value() RETURNS integer LANGUAGE plpgsql AS $$
BEGIN RETURN 42; END;
$$;
-- +goose StatementEnd`)},
		"000002_index.sql": {Data: []byte(`-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY probe_id ON probe(id);`)},
	}
	if err := Migrate(ctx, c, files, quiet()); err != nil {
		t.Fatal(err)
	}
	var value int
	if err := c.QueryRowContext(ctx, "SELECT probe_value()").Scan(&value); err != nil || value != 42 {
		t.Fatalf("value=%d err=%v", value, err)
	}
}

type forbiddenLocker struct{}

func (forbiddenLocker) SessionLock(context.Context, *sql.Conn) error   { panic("must never lock") }
func (forbiddenLocker) SessionUnlock(context.Context, *sql.Conn) error { panic("must never unlock") }

var _ lock.SessionLocker = forbiddenLocker{}

func TestExternalProviderRejectsIncompatibleOptions(t *testing.T) {
	files := fstest.MapFS{"000001_test.sql": {Data: []byte("-- +goose Up\nSELECT 1;")}}
	for _, option := range []goose.ProviderOption{
		goose.WithSessionLocker(forbiddenLocker{}), goose.WithDisableVersioning(true), goose.WithIsolateDDL(true),
		goose.WithGoMigrations(goose.NewGoMigration(1, &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error { return nil }}, nil)),
	} {
		if _, err := goose.NewProviderWithConn(goose.DialectPostgres, &sql.Conn{}, files, option); err == nil {
			t.Fatal("incompatible option accepted")
		}
	}
}

func TestValidateFiles(t *testing.T) {
	for _, files := range []fstest.MapFS{
		{"000001_a.sql": {}, "0000001_b.sql": {}}, {"000000_zero.sql": {}}, {"nested/000001_a.sql": {}}, {"000001_a.go": {}},
	} {
		if _, err := ValidateFiles(files); err == nil {
			t.Fatal("invalid migration set accepted")
		}
	}
	if _, err := ValidateFiles(Files()); err != nil {
		t.Fatal(err)
	}
}
