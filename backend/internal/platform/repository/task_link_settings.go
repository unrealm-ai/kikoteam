package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type TaskLinkSettingsStore struct{ DB *sql.DB }

func (s *TaskLinkSettingsStore) ReadAdmin(ctx context.Context, actor string) (service.TaskLinkSettings, error) {
	var out service.TaskLinkSettings
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(settings.version,0),COALESCE(settings.origin,''),COALESCE(settings.enabled,true)
 FROM platform_accounts a LEFT JOIN platform_task_link_settings settings ON settings.singleton
 WHERE a.id=$1 AND a.is_platform_admin`, actor).Scan(&out.Version, &out.Origin, &out.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	return out, err
}

func (s *TaskLinkSettingsStore) SaveAdmin(ctx context.Context, actor string, version int64, origin string, enabled bool) (service.TaskLinkSettings, error) {
	var out service.TaskLinkSettings
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = requireSettingsAdmin(ctx, tx, actor, true); err != nil {
		return out, err
	}
	var row *sql.Row
	if version == 0 {
		row = tx.QueryRowContext(ctx, `INSERT INTO platform_task_link_settings(singleton,version,origin,enabled,updated_by)
 VALUES(true,1,$1,$2,$3) ON CONFLICT DO NOTHING RETURNING version,origin,enabled`, origin, enabled, actor)
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE platform_task_link_settings
 SET version=version+1,origin=$1,enabled=$2,updated_by=$3,updated_at=now()
 WHERE singleton AND version=$4 RETURNING version,origin,enabled`, origin, enabled, actor, version)
	}
	err = row.Scan(&out.Version, &out.Origin, &out.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("task_link_settings_version_conflict", 409, "任务链接设置已更新，请刷新后重试")
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
