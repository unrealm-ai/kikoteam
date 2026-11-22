package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strconv"

	"github.com/pressly/goose/v3"
)

const VersionTable = "public.goose_db_version"

// HistoryError identifies an incomplete/older artifact without exposing SQL.
type HistoryError struct{ Version int64 }

func (e *HistoryError) Error() string {
	return fmt.Sprintf("migration file missing for recorded version %d", e.Version)
}

var filePattern = regexp.MustCompile(`^([0-9]{6,})_[a-z0-9_]+\.sql$`)

// ValidateFiles rejects accidental nested migrations, Go migrations and duplicate
// numeric versions. Parsing SQL itself remains Goose's responsibility.
func ValidateFiles(files fs.FS) (map[int64]string, error) {
	versions := map[int64]string{}
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." {
				return fmt.Errorf("nested migration directory: %s", path)
			}
			return nil
		}
		if path == "README.md" {
			return nil
		}
		m := filePattern.FindStringSubmatch(path)
		if m == nil {
			return fmt.Errorf("invalid migration filename: %s", path)
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || v <= 0 {
			return fmt.Errorf("invalid migration number: %s", path)
		}
		if previous, ok := versions[v]; ok {
			return fmt.Errorf("duplicate migration version: %s and %s", previous, path)
		}
		versions[v] = path
		return nil
	})
	return versions, err
}

// Migrate borrows a connection already protected by Event Hub's election lock.
// It never closes the connection, reconnects, or adds another migration lock.
func Migrate(ctx context.Context, conn *sql.Conn, files fs.FS, logger *slog.Logger) error {
	versions, err := ValidateFiles(files)
	if err != nil {
		return err
	}
	if conn == nil {
		return errors.New("migration requires an elected connection")
	}
	if logger == nil {
		logger = slog.Default()
	}
	// Check for missing historical files even when Goose would find no pending work.
	var exists bool
	if err := conn.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", VersionTable).Scan(&exists); err != nil {
		return err
	}
	if exists {
		rows, err := conn.QueryContext(ctx, "SELECT DISTINCT version_id FROM "+VersionTable+" WHERE version_id > 0")
		if err != nil {
			return err
		}
		defer rows.Close() //nolint:errcheck // Fallback on early failure; successful iteration checks rows.Err and rows.Close below.
		for rows.Next() {
			var version int64
			if err := rows.Scan(&version); err != nil {
				return err
			}
			if _, ok := versions[version]; !ok {
				return &HistoryError{Version: version}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(versions) == 0 {
		logger.Info("migrations complete", "event", "migration.completed", "applied_count", 0)
		return nil
	}
	p, err := goose.NewProviderWithConn(goose.DialectPostgres, conn, files,
		goose.WithTableName(VersionTable), goose.WithSlog(logger))
	if err != nil {
		return err
	}
	// Up retains Goose's transaction/version atomicity and rejects out-of-order files.
	results, err := p.Up(ctx)
	var partial *goose.PartialError
	if errors.As(err, &partial) {
		results = partial.Applied
	}
	for _, result := range results {
		logger.Info("migration applied", "event", "migration.applied", "migration_version", result.Source.Version,
			"duration_ms", result.Duration.Milliseconds())
	}
	if err == nil {
		logger.Info("migrations complete", "event", "migration.completed", "applied_count", len(results))
	}
	return err
}
