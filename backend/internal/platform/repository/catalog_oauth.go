package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (t *pluginTx) tenantOAuth(ctx context.Context, p service.Plugin) (service.Plugin, error) {
	var o service.PluginOAuth
	var visible []byte
	err := t.model.tx.QueryRowContext(ctx, `SELECT s.endpoint,s.scopes,to_json(s.read_tools),c.id,c.client_id,c.issuer FROM platform_catalog_oauth_settings s JOIN platform_plugin_oauth_clients c ON c.id=s.client_id AND c.workspace_id=s.workspace_id WHERE s.workspace_id=$1 AND s.plugin_version_id=$2`, t.model.workspace, p.VersionID).Scan(&o.Resource, &o.Scopes, &visible, &o.ID, &o.ClientID, &o.Issuer)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err == nil {
		p.Endpoint = o.Resource
		err = json.Unmarshal(visible, &o.ReadTools)
		p.OAuth = &o
	}
	return p, err
}
func (t *pluginTx) SaveTenantOAuth(ctx context.Context, p service.Plugin) error {
	var active bool
	if err := t.model.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM platform_plugin_connections WHERE workspace_id=$1 AND plugin_version_id=$2 AND enabled) `, t.model.workspace, p.VersionID).Scan(&active); err != nil {
		return err
	}
	if active {
		return service.Fail("oauth_client_in_use", 409, "请先撤销此空间内该插件版本的已授权连接，再更换租户应用")
	}
	if err := t.saveClient(ctx, p); err != nil {
		return err
	}
	var previous string
	err := t.model.tx.QueryRowContext(ctx, "SELECT client_id FROM platform_catalog_oauth_settings WHERE workspace_id=$1 AND plugin_version_id=$2 FOR UPDATE", t.model.workspace, p.VersionID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = t.model.tx.ExecContext(ctx, `INSERT INTO platform_catalog_oauth_settings(workspace_id,plugin_version_id,client_id,endpoint,scopes,read_tools) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,plugin_version_id) DO UPDATE SET client_id=excluded.client_id, endpoint=excluded.endpoint, scopes=excluded.scopes,read_tools=excluded.read_tools`, t.model.workspace, p.VersionID, p.OAuth.ID, p.Endpoint, p.OAuth.Scopes, p.OAuth.ReadTools); err != nil {
		return err
	}
	if previous != "" {
		if _, err = t.model.tx.ExecContext(ctx, "DELETE FROM platform_plugin_oauth_attempts WHERE workspace_id=$1 AND plugin_version_id=$2", t.model.workspace, p.VersionID); err != nil {
			return err
		}
		_, err = t.model.tx.ExecContext(ctx, "DELETE FROM platform_plugin_oauth_clients WHERE id=$1 AND workspace_id=$2", previous, t.model.workspace)
	}
	return err
}
