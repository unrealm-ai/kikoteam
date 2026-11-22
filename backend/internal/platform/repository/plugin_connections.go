package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const connectionColumns = `id,COALESCE(agent_id::text,''),plugin_id,plugin_version_id,name,version,enabled,validated,tools,credential,validated_at,owner_type,credential_revision,granted_scopes,expires_at,external_account,auth_method`
const sharedAgentCount = `(SELECT count(DISTINCT pkg.agent_id) FROM platform_capability_package_plugins b JOIN platform_capability_packages pkg ON pkg.id=b.package_id WHERE c.agent_id IS NULL AND b.workspace_id=c.workspace_id AND b.plugin_id=c.plugin_id AND b.version_id=c.plugin_version_id AND pkg.agent_id IS NOT NULL AND platform_plugin_connection_owner(b.workspace_id,pkg.agent_id,b.plugin_id) IS NULL)`

func scanConnection(row scanner, extra ...any) (service.PluginAccount, error) {
	var p service.PluginAccount
	var tools []byte
	dest := []any{&p.ID, &p.AgentID, &p.PluginID, &p.PluginVersionID, &p.Name, &p.Version, &p.Enabled, &p.Validated, &tools, &p.Credential, &p.ValidatedAt, &p.Source, &p.CredentialRevision, &p.GrantedScopes, &p.ExpiresAt, &p.ExternalAccount, &p.AuthMethod}
	e := row.Scan(append(dest, extra...)...)
	if e == nil {
		e = json.Unmarshal(tools, &p.Tools)
	}
	return p, workspaceDBError(e)
}
func (t *pluginTx) Account(ctx context.Context, agent, plugin, version string) (service.PluginAccount, error) {
	if agent == "" && version != "" {
		if err := installPublicConnection(ctx, t.model.tx, t.model.workspace, plugin, version); err != nil {
			return service.PluginAccount{}, err
		}
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+connectionColumns+` FROM platform_plugin_connections WHERE workspace_id=$1 AND agent_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid AND plugin_id=$3 AND (NULLIF($4,'')::uuid IS NULL OR plugin_version_id=NULLIF($4,'')::uuid) LIMIT 2 FOR UPDATE`, t.model.workspace, agent, plugin, version)
	if e != nil {
		return service.PluginAccount{}, e
	}
	defer rows.Close() //nolint:errcheck // rows.Err is checked below.
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return service.PluginAccount{}, e
		}
		return service.PluginAccount{}, service.ErrWorkspaceMissing
	}
	p, e := scanConnection(rows)
	if e != nil {
		return p, e
	}
	if rows.Next() {
		return p, service.Fail("plugin_version_required", 409, "请指定插件版本")
	}
	return p, rows.Err()
}
func (t *pluginTx) ManagedAccount(ctx context.Context, agent, plugin, version string) (service.PluginAccount, error) {
	p, err := t.Account(ctx, agent, plugin, version)
	if err == nil && agent == "" {
		err = t.model.tx.QueryRowContext(ctx, `SELECT `+sharedAgentCount+` FROM platform_plugin_connections c WHERE c.id=$1 AND c.workspace_id=$2`, p.ID, t.model.workspace).Scan(&p.AgentCount)
	}
	return p, err
}
func (t *pluginTx) SaveAccount(ctx context.Context, p service.PluginAccount, create bool) error {
	tools, e := json.Marshal(p.Tools)
	if e != nil {
		return e
	}
	args := []any{t.model.workspace, p.ID, p.AgentID, p.PluginID, p.PluginVersionID, p.Name, p.Version, p.Enabled, p.Validated, tools, p.Credential, p.ValidatedAt, p.GrantedScopes, p.ExpiresAt, p.ExternalAccount, p.AuthMethod}
	query := `UPDATE platform_plugin_connections SET name=$6,version=$7,enabled=$8,validated=$9,tools=$10,credential=$11,validated_at=$12,granted_scopes=$13,expires_at=$14,external_account=$15,auth_method=$16,credential_revision=credential_revision+1 WHERE workspace_id=$1 AND id=$2 AND agent_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid AND plugin_id=$4 AND plugin_version_id=$5 AND version=$7-1`
	if create {
		query = `INSERT INTO platform_plugin_connections(workspace_id,id,agent_id,plugin_id,plugin_version_id,name,version,enabled,validated,tools,credential,validated_at,granted_scopes,expires_at,external_account,auth_method) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT DO NOTHING`
	}
	r, e := t.model.tx.ExecContext(ctx, query, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return service.Fail("plugin_version_conflict", 409, "连接已更新，请刷新")
	}
	return e
}
func (t *pluginTx) Source(ctx context.Context, a, p string) (service.ConnectionSource, error) {
	var s service.ConnectionSource
	err := t.model.tx.QueryRowContext(ctx, `SELECT source,revision FROM platform_agent_plugin_connection_settings WHERE workspace_id=$1 AND agent_id=$2 AND plugin_id=$3`, t.model.workspace, a, p).Scan(&s.Source, &s.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ConnectionSource{Source: "workspace"}, nil
	}
	return s, err
}
func (t *pluginTx) SetSource(ctx context.Context, a, p, actor, source string, expected *int64) (service.ConnectionSource, error) {
	old, e := t.Source(ctx, a, p)
	if e != nil {
		return old, e
	}
	if expected != nil && old.Revision != *expected {
		return old, service.Fail("connection_source_conflict", 409, "账号来源已变更")
	}
	if old.Source == source {
		return old, nil
	}
	old.Source = source
	old.Revision++
	_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_agent_plugin_connection_settings(workspace_id,agent_id,plugin_id,source,revision,updated_by) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,agent_id,plugin_id) DO UPDATE SET source=excluded.source,revision=excluded.revision,updated_by=excluded.updated_by`, t.model.workspace, a, p, source, old.Revision, actor)
	return old, e
}
func (t *pluginTx) SharedAccounts(ctx context.Context) ([]service.PluginAccount, error) {
	out := []service.PluginAccount{}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+connectionColumns+`,`+sharedAgentCount+` FROM platform_plugin_connections c WHERE workspace_id=$1 AND agent_id IS NULL ORDER BY name,id`, t.model.workspace)
	if e != nil {
		return nil, e
	}
	defer rows.Close() //nolint:errcheck // Read errors are checked by rows.Err.
	for rows.Next() {
		var count int
		p, e := scanConnection(rows, &count)
		p.AgentCount = count
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (t *pluginTx) saveClient(ctx context.Context, p service.Plugin) error {
	_, e := t.model.tx.ExecContext(ctx, `INSERT INTO platform_plugin_oauth_clients(id,workspace_id,issuer,client_id,client_secret) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5)`, p.OAuth.ID, t.model.workspace, p.OAuth.Issuer, p.OAuth.ClientID, p.ClientCredential)
	return e
}
func (t *pluginTx) ClientSecret(ctx context.Context, id string) ([]byte, error) {
	var secret []byte
	e := t.model.tx.QueryRowContext(ctx, `SELECT client_secret FROM platform_plugin_oauth_clients WHERE id=$1 AND (workspace_id=$2 OR workspace_id IS NULL)`, id, t.model.workspace).Scan(&secret)
	return secret, workspaceDBError(e)
}
func (t *pluginTx) SaveAttempt(ctx context.Context, a service.OAuthAttempt) error {
	if _, e := t.model.tx.ExecContext(ctx, `DELETE FROM platform_plugin_oauth_attempts WHERE expires_at<now()`); e != nil {
		return e
	}
	_, e := t.model.tx.ExecContext(ctx, `INSERT INTO platform_plugin_oauth_attempts(state_hash,workspace_id,actor_id,session_hash,agent_id,plugin_id,plugin_version_id,account_version,source_revision,name,issuer,verifier,tenant_client_id,github_app_pin,expires_at) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,'')::uuid,$14,now()+interval '10 minutes')`, a.StateHash, t.model.workspace, a.Actor, a.SessionHash, a.Agent, a.Plugin, a.PluginVersion, a.AccountVersion, a.SourceRevision, a.Name, a.Issuer, a.Verifier, a.TenantClientID, a.GitHubAppPin)
	return e
}
func (t *pluginTx) TakeAttempt(ctx context.Context, state, actor, session string) (service.OAuthAttempt, error) {
	a := service.OAuthAttempt{Workspace: t.model.workspace, StateHash: state, Actor: actor, SessionHash: session}
	e := t.model.tx.QueryRowContext(ctx, `DELETE FROM platform_plugin_oauth_attempts WHERE workspace_id=$1 AND state_hash=$2 AND actor_id=$3 AND session_hash=$4 AND expires_at>now() RETURNING COALESCE(agent_id::text,''),plugin_id,plugin_version_id,account_version,source_revision,name,issuer,verifier,coalesce(tenant_client_id::text,''),github_app_pin`, t.model.workspace, state, actor, session).Scan(&a.Agent, &a.Plugin, &a.PluginVersion, &a.AccountVersion, &a.SourceRevision, &a.Name, &a.Issuer, &a.Verifier, &a.TenantClientID, &a.GitHubAppPin)
	return a, workspaceDBError(e)
}
func (t *pluginTx) RefreshAccount(ctx context.Context, p service.PluginAccount) error {
	_, e := t.model.tx.ExecContext(ctx, `UPDATE platform_plugin_connections SET credential=$3,credential_revision=credential_revision+1,expires_at=$4,validated=$5,version=$6 WHERE workspace_id=$1 AND id=$2`, t.model.workspace, p.ID, p.Credential, p.ExpiresAt, p.Validated, p.Version)
	return e
}
func (t *pluginTx) executionAccountReady(ctx context.Context, employee string, plugin service.Plugin) (bool, error) {
	source, err := t.Source(ctx, employee, plugin.ID)
	if err != nil {
		return false, err
	}
	owner := ""
	if source.Source == "agent" {
		owner = employee
	}
	account, err := t.Account(ctx, owner, plugin.ID, plugin.VersionID)
	if errors.Is(err, service.ErrWorkspaceMissing) {
		return plugin.AuthType == "none", nil
	}
	return account.Enabled && (account.Validated || plugin.AuthType == "none"), err
}

// First successful discovery wins, including an empty list. Failed discovery
// leaves no row. Concurrent workers always read back the same inventory.
func (t *pluginTx) SessionTools(ctx context.Context, session, snapshot, version string, discovered []service.MCPTool) ([]service.MCPTool, error) {
	if discovered != nil {
		data, err := json.Marshal(discovered)
		if err != nil {
			return nil, err
		}
		_, err = t.model.tx.ExecContext(ctx, `INSERT INTO manager_session_plugin_tools(session_id,snapshot_id,plugin_version_id,tools)
SELECT id,$3,$4,$5 FROM manager_sessions WHERE workspace_id=$1 AND id=$2 ON CONFLICT DO NOTHING`, t.model.workspace, session, snapshot, version, data)
		if err != nil {
			return nil, err
		}
	}
	var data []byte
	err := t.model.tx.QueryRowContext(ctx, `SELECT t.tools FROM manager_session_plugin_tools t JOIN manager_sessions s ON s.id=t.session_id WHERE s.workspace_id=$1 AND s.id=$2 AND t.snapshot_id=$3 AND t.plugin_version_id=$4`, t.model.workspace, session, snapshot, version).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tools []service.MCPTool
	err = json.Unmarshal(data, &tools)
	return tools, err
}

func (t *pluginTx) NoteDiscoveredTools(ctx context.Context, pin service.RuntimeAccountPin, tools []service.MCPTool) error {
	data, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	_, err = t.model.tx.ExecContext(ctx, `UPDATE platform_plugin_connections SET tools=$5,validated=true,validated_at=now()
WHERE workspace_id=$1 AND id=$2 AND version=$3 AND plugin_version_id=$4 AND enabled
AND (NOT validated OR tools IS DISTINCT FROM $5::jsonb)`, t.model.workspace, pin.ID, pin.Version, pin.PluginVersionID, data)
	return err
}
