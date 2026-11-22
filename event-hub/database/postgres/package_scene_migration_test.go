package postgres

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestPackageSceneMigrationRejectsAuthorizationDowngrade(t *testing.T) {
	_, db := testpg.New(t)
	c, ctx := borrowed(t, db)
	provider, err := goose.NewProviderWithConn(goose.DialectPostgres, c, Files(), goose.WithTableName(VersionTable), goose.WithSlog(quiet()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	// This guard belongs to migration 55 regardless of later migrations.
	const guardedVersion = 55
	if _, err := provider.UpTo(ctx, guardedVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "package scene authorization rollback requires explicit policy and snapshot migration") {
		t.Fatalf("authorization downgrade was not blocked by migration 55: %v", err)
	}
	var version int
	err = c.QueryRowContext(ctx, "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied").Scan(&version)
	if err != nil || version != guardedVersion {
		t.Fatalf("downgrade altered recorded version: %d, %v", version, err)
	}
}
