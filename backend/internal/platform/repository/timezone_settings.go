package repository

import (
	"context"
	"database/sql"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type TimezoneSettingsStore struct{ DB *sql.DB }

func (s *TimezoneSettingsStore) ReadAdmin(ctx context.Context, actor string) (service.TimezoneSettings, error) {
	var out service.TimezoneSettings
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read transaction.
	if err = requireSettingsAdmin(ctx, tx, actor, false); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT version,timezone FROM platform_timezone_settings WHERE singleton`).Scan(&out.Version, &out.Timezone)
	if err == nil {
		err = tx.Commit()
	}
	return out, err
}

func (s *TimezoneSettingsStore) SaveAdmin(ctx context.Context, actor string, version int64, zone string) (service.TimezoneSettings, error) {
	var out service.TimezoneSettings
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = requireSettingsAdmin(ctx, tx, actor, true); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT version,timezone FROM platform_timezone_settings WHERE singleton FOR UPDATE`).Scan(&out.Version, &out.Timezone); err != nil {
		return out, err
	}
	if out.Version != version {
		return out, service.Fail("platform_timezone_version_conflict", 409, "平台时区已更新，请刷新后重试")
	}
	if out.Timezone == zone {
		return out, tx.Commit()
	}
	if err = tx.QueryRowContext(ctx, `UPDATE platform_timezone_settings SET timezone=$1,version=version+1,updated_by=$2,updated_at=clock_timestamp()
 WHERE singleton RETURNING version,timezone`, zone, actor).Scan(&out.Version, &out.Timezone); err != nil {
		return out, err
	}
	// A tick locks the trigger row and matches the exact due instant. Clearing
	// it makes old ticks stale without invalidating already-created task grants.
	if _, err = tx.ExecContext(ctx, `UPDATE manager_triggers SET config=jsonb_set(config,'{timezone}',to_jsonb($1::text)),
 next_due=NULL,updated_at=statement_timestamp() WHERE kind='schedule'`, zone); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
