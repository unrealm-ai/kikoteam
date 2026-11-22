// Command schema replays the embedded migrations in a new disposable database.
// It is a development/CI tool, not a production migration entry point.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/database/postgres"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/app"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

func main() {
	check := flag.Bool("check", false, "compare the generated snapshot with the committed file")
	output := flag.String("output", "database/postgres/schema.sql", "snapshot path")
	flag.Parse()
	if err := run(*output, *check, postgres.Files()); err != nil {
		app.NewLogger().Error("schema generation failed", app.ErrorAttrs(err)...)
		// run returns only sanitized operational errors; raw driver/dump errors are excluded.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output string, check bool, files fs.FS) (retErr error) {
	dsn := os.Getenv("EVENT_HUB_TEST_DSN")
	u, err := url.Parse(dsn)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("EVENT_HUB_TEST_DSN must be a PostgreSQL URL for a disposable test server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	version, err := exec.CommandContext(ctx, "pg_dump", "--version").Output()
	var clientMajor, clientMinor int
	parsed, parseErr := fmt.Sscanf(strings.TrimPrefix(string(version), "pg_dump (PostgreSQL) "), "%d.%d", &clientMajor, &clientMinor)
	if err != nil || parseErr != nil || parsed != 2 || clientMajor != 16 {
		return errors.New("schema generation requires pg_dump 16.x for reproducible output")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("open schema test server failed")
	}
	defer admin.Close() //nolint:errcheck // Disposable test pool; database-drop errors are reported by the cleanup below.
	var serverVersion int
	if err := admin.QueryRowContext(ctx, "SHOW server_version_num").Scan(&serverVersion); err != nil || serverVersion/10000 != 16 {
		return errors.New("schema generation requires a PostgreSQL 16.x test server")
	}
	// An older minor client can expose server-internal options in the dump.
	// For example, pg_dump 16.15 exports non-reproducible pg_attver values
	// from a 16.16 server. Upgrade the client rather than filtering schema SQL.
	if clientMinor < serverVersion%10000 {
		return errors.New("schema generation requires pg_dump 16.x at least as new as the test server; upgrade the client")
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return errors.New("generate temporary database name failed")
	}
	name := "event_hub_schema_" + hex.EncodeToString(random)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		return errors.New("create schema test database failed; check CREATEDB permission")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("drop temporary database %s failed; remove it on the test server", name))
		}
	}()
	u.Path, u.RawPath = "/"+name, ""
	// A URL query parameter must not override the disposable database path.
	query := u.Query()
	query.Del("dbname")
	query.Del("database")
	u.RawQuery = query.Encode()
	cfg, err := app.LoadConfig(func(key string) string {
		if key == "EVENT_HUB_DATABASE_URL" {
			return u.String()
		}
		return ""
	})
	if err != nil {
		return errors.New("schema election configuration failed")
	}
	session, err := election.Acquire(ctx, cfg.Election, nil)
	if err != nil {
		return errors.New("acquire schema test lock failed")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := session.Close(cleanup); err != nil {
			retErr = errors.Join(retErr, errors.New("close schema test session failed"))
		}
	}()
	if err := session.Do(ctx, cfg.MigrationTimeout, func(ctx context.Context, c *sql.Conn) error {
		return postgres.Migrate(ctx, c, files, app.NewLogger())
	}); err != nil {
		app.NewLogger().Error("schema migration failed", app.ErrorAttrs(err)...)
		return errors.New("replay embedded migrations failed")
	}
	dumpURL := *u
	password := ""
	if dumpURL.User != nil {
		password, _ = dumpURL.User.Password()
		dumpURL.User = url.User(dumpURL.User.Username())
	}
	dumpQuery := dumpURL.Query()
	if p := dumpQuery.Get("password"); p != "" {
		password = p
	}
	dumpQuery.Del("password")
	dumpURL.RawQuery = dumpQuery.Encode()
	dump := exec.CommandContext(ctx, "pg_dump", "--dbname="+dumpURL.String(), "--schema-only", "--no-owner", "--no-privileges", "--exclude-table="+postgres.VersionTable)
	// libpq does not expand a URI in PGDATABASE. Pass the URI explicitly, with
	// its password moved to the environment, and never print raw dump errors.
	dump.Env = append(os.Environ(), "PGPASSWORD="+password)
	data, err := dump.Output()
	if err != nil {
		return errors.New("pg_dump of temporary database failed")
	}
	data = normalizeDump(data)
	if check {
		previous, err := os.ReadFile(output)
		if err != nil || !bytes.Equal(previous, data) {
			return errors.New("schema snapshot differs; run make schema and review the diff")
		}
		return nil
	}
	if err := os.WriteFile(output, data, 0644); err != nil {
		return errors.New("write schema snapshot failed")
	}
	return nil
}

// pg_dump 16.16 adds randomized psql guard blocks. Drop only those guard
// lines and their immediately following separator, never whitespace in SQL.
func normalizeDump(data []byte) []byte {
	var lines []string
	skipSeparator := false
	for _, line := range strings.Split(string(data), "\n") {
		if skipSeparator {
			skipSeparator = false
			if line == "" {
				continue
			}
		}
		if strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `) {
			skipSeparator = true
			continue
		}
		if strings.HasPrefix(line, "-- Dumped ") {
			continue
		}
		lines = append(lines, line)
	}
	return []byte("-- Generated by make schema; do not edit. PostgreSQL 16.\n" + strings.TrimSpace(strings.Join(lines, "\n")) + "\n")
}
