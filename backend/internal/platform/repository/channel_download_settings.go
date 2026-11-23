package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type ChannelDownloadSettingsStore struct{ DB *sql.DB }

func (s *ChannelDownloadSettingsStore) ReadAdmin(ctx context.Context, actor string) (service.ChannelDownloadSettings, error) {
	var out service.ChannelDownloadSettings
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(s.version,0),COALESCE(s.validate_public_ip,true)
 FROM platform_accounts a LEFT JOIN platform_channel_download_settings s ON s.singleton
 WHERE a.id=$1 AND a.is_platform_admin`, actor).Scan(&out.Version, &out.ValidatePublicIP)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	return out, err
}

func (s *ChannelDownloadSettingsStore) SaveAdmin(ctx context.Context, actor string, version int64, validatePublicIP bool) (service.ChannelDownloadSettings, error) {
	var out service.ChannelDownloadSettings
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
		row = tx.QueryRowContext(ctx, `INSERT INTO platform_channel_download_settings(singleton,version,validate_public_ip,updated_by)
 VALUES(true,1,$1,$2) ON CONFLICT DO NOTHING RETURNING version,validate_public_ip`, validatePublicIP, actor)
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE platform_channel_download_settings
 SET version=version+1,validate_public_ip=$1,updated_by=$2,updated_at=now()
 WHERE singleton AND version=$3 RETURNING version,validate_public_ip`, validatePublicIP, actor, version)
	}
	err = row.Scan(&out.Version, &out.ValidatePublicIP)
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("channel_download_settings_version_conflict", 409, "附件下载设置已更新，请刷新后重试")
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
