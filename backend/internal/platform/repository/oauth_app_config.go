package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type OAuthAppStore struct{ DB *sql.DB }

const githubOAuthKey = `hashtext('platform_oauth_app_configs'),hashtext('plugin/github')`

func readGitHubOAuthApp(ctx context.Context, tx *sql.Tx) (c service.OAuthAppConfig, err error) {
	err = tx.QueryRowContext(ctx, `SELECT version,enabled,client_id,client_secret,legacy_grant_pin FROM platform_oauth_app_configs WHERE provider='github' AND purpose='plugin'`).
		Scan(&c.Version, &c.Enabled, &c.ClientID, &c.Ciphertext, &c.LegacyGrantPin)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	return c, err
}

func (t *pluginTx) GitHubOAuthApp(ctx context.Context) (service.OAuthAppConfig, error) {
	if _, err := t.model.tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(`+githubOAuthKey+`)`); err != nil {
		return service.OAuthAppConfig{}, err
	}
	return readGitHubOAuthApp(ctx, t.model.tx)
}

func oauthAppAdmin(ctx context.Context, tx *sql.Tx, actor string, write bool) error {
	query := `SELECT is_platform_admin FROM platform_accounts WHERE id=$1`
	if write {
		query += ` FOR SHARE`
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, query, actor).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return service.Fail("platform_admin_required", 403, "需要平台管理员权限")
	}
	return err
}

func (s *OAuthAppStore) ReadGitHubAdmin(ctx context.Context, actor string) (service.OAuthAppConfig, error) {
	var c service.OAuthAppConfig
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = oauthAppAdmin(ctx, tx, actor, false); err != nil {
		return c, err
	}
	c, err = readGitHubOAuthApp(ctx, tx)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *OAuthAppStore) UpdateGitHubAdmin(ctx context.Context, actor string, apply func(service.OAuthAppConfig) (service.OAuthAppConfig, error)) (service.OAuthAppConfig, error) {
	var c service.OAuthAppConfig
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = oauthAppAdmin(ctx, tx, actor, true); err != nil {
		return c, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+githubOAuthKey+`)`); err != nil {
		return c, err
	}
	c, err = readGitHubOAuthApp(ctx, tx)
	if err != nil {
		return c, err
	}
	c, err = apply(c)
	if err != nil {
		return c, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_oauth_app_configs(provider,purpose,version,enabled,client_id,client_secret,legacy_grant_pin,updated_by)
 VALUES('github','plugin',$1,$2,$3,COALESCE($4::bytea,''::bytea),$5,$6) ON CONFLICT(provider,purpose) DO UPDATE SET
 version=EXCLUDED.version,enabled=EXCLUDED.enabled,client_id=EXCLUDED.client_id,client_secret=EXCLUDED.client_secret,legacy_grant_pin=EXCLUDED.legacy_grant_pin,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		c.Version, c.Enabled, c.ClientID, c.Ciphertext, c.LegacyGrantPin, actor)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
