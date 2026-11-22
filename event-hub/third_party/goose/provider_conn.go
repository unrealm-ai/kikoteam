package goose

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
)

// PreparationError identifies a borrowed SQL migration that failed parsing before
// execution. It preserves the version without requiring callers to log raw SQL.
type PreparationError struct {
	Version int64
	Err     error
}

func (e *PreparationError) Error() string {
	return fmt.Sprintf("prepare migration %d: %v", e.Version, e.Err)
}
func (e *PreparationError) Unwrap() error { return e.Err }

// NewProviderWithConn borrows an existing PostgreSQL session for SQL migrations.
// Every database operation uses conn. Close and per-operation cleanup never close
// it. The caller must serialize other uses of conn and provide exclusive migration
// execution (for example, by holding a session advisory lock before constructing
// this provider). Go migrations and additional migration lockers are disallowed.
// Global Go migrations are ignored. Connection errors never trigger reconnection.
func NewProviderWithConn(dialect Dialect, conn *sql.Conn, fsys fs.FS, opts ...ProviderOption) (*Provider, error) {
	if conn == nil {
		return nil, errors.New("conn must not be nil")
	}
	if dialect != DialectPostgres {
		return nil, errors.New("external connection currently supports PostgreSQL only")
	}
	return newConfiguredProvider(dialect, nil, conn, fsys, opts...)
}
