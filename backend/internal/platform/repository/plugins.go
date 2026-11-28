package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type PluginStore struct{ DB *sql.DB }

func NewPluginStore(db *sql.DB) *PluginStore { return &PluginStore{DB: db} }
func (s *PluginStore) WithWorkspace(ctx context.Context, w string, fn func(service.PluginTx) error) error {
	return (&ModelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ModelTx) error { return fn(&pluginTx{model: t.(*modelTx)}) })
}

type pluginTx struct{ model *modelTx }

func (t *pluginTx) Member(ctx context.Context, a string) (service.Member, error) {
	return t.model.Member(ctx, a)
}
func (t *pluginTx) AgentExists(ctx context.Context, id string) error {
	var found string
	return workspaceDBError(t.model.tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2`, t.model.workspace, id).Scan(&found))
}

const oauthDefinitionColumns = `CASE WHEN v.auth_type='oauth' THEN jsonb_build_object('issuer',v.oauth_issuer,'client_id',COALESCE((SELECT c.client_id FROM platform_plugin_oauth_clients c WHERE c.id=v.oauth_client_id),''),'id',COALESCE(v.oauth_client_id::text,''),'resource',v.oauth_resource,'scopes',v.oauth_scopes) END`
const pluginListFilter = `v.distribution='manual' AND p.archived_at IS NULL AND ((p.workspace_id=NULLIF($1,'')::uuid AND $2 IN ('workspace','all')) OR (p.workspace_id IS NULL AND ($2='manage' OR p.listed AND $2 IN ('official','all')))) AND ($3='' OR strpos(lower(p.name),lower($3))>0 OR strpos(lower(p.description),lower($3))>0 OR p.builtin_key=ANY($4::text[]))`

const sharedStateColumn = `CASE WHEN NULLIF($1,'') IS NULL OR v.auth_type='none' THEN '' ELSE COALESCE((SELECT CASE WHEN c.enabled AND c.validated THEN 'connected' WHEN NOT c.enabled THEN 'revoked' ELSE 'unvalidated' END FROM platform_plugin_connections c WHERE c.workspace_id=NULLIF($1,'')::uuid AND c.plugin_id=p.id AND c.plugin_version_id=v.id AND c.agent_id IS NULL),'missing') END`
const pluginDefinitionCols = `CASE WHEN p.workspace_id IS NULL THEN 'official' ELSE 'workspace' END,coalesce(p.builtin_key,''), ` + sharedStateColumn + `,p.listed,(SELECT count(DISTINCT pkg.agent_id) FROM platform_capability_package_plugins b JOIN platform_capability_packages pkg ON pkg.id=b.package_id WHERE b.plugin_id=p.id AND b.workspace_id=NULLIF($1,'')::uuid),p.id,v.name,v.description,v.endpoint,v.auth_type,v.header_name, ` + oauthDefinitionColumns
const pluginCols = pluginDefinitionCols + `,p.enabled,p.version,v.version,v.id,p.created_at,p.updated_at`
const pluginVersionCols = pluginDefinitionCols + `,p.enabled,v.version,v.version,v.id,v.created_at,v.created_at`

func scanPlugin(row scanner) (service.Plugin, error) {
	var p service.Plugin
	var key string
	var oauth []byte
	e := row.Scan(&p.Scope, &key, &p.SharedConnectionState, &p.Listed, &p.AssignedAgentCount, &p.ID, &p.Name, &p.Description, &p.Endpoint, &p.AuthType, &p.HeaderName, &oauth, &p.Enabled, &p.Version, &p.DefinitionVersion, &p.VersionID, &p.CreatedAt, &p.UpdatedAt)
	p.Card = service.BuiltinResourceCard("plugin", key)
	p.SetAuthMethods()
	if e == nil && len(oauth) > 0 {
		e = json.Unmarshal(oauth, &p.OAuth)
	}
	return p, workspaceDBError(e)
}
func (t *pluginTx) Get(ctx context.Context, id string) (service.Plugin, error) {
	return scanPlugin(t.model.tx.QueryRowContext(ctx, `SELECT `+pluginCols+` FROM platform_plugins p JOIN platform_plugin_versions v ON v.id=p.current_version_id WHERE (p.workspace_id=NULLIF($1,'')::uuid OR p.workspace_id IS NULL) AND p.archived_at IS NULL AND v.distribution='manual' AND p.id=$2`, t.model.workspace, id))
}
func (t *pluginTx) GetVersion(ctx context.Context, id, version string) (service.Plugin, error) {
	p, err := scanPlugin(t.model.tx.QueryRowContext(ctx, `SELECT `+pluginVersionCols+` FROM platform_plugins p JOIN platform_plugin_versions v ON v.plugin_id=p.id WHERE (p.workspace_id=NULLIF($1,'')::uuid OR p.workspace_id IS NULL) AND p.archived_at IS NULL AND p.id=$2 AND v.distribution='manual' AND v.id=$3`, t.model.workspace, id, version))
	if err != nil || id != service.CatalogID("plugin", "fxiaoke") || t.model.workspace == "" {
		return p, err
	}
	return t.tenantOAuth(ctx, p)
}
func (t *pluginTx) List(ctx context.Context, p, n int, filter service.ResourceListFilter) ([]service.Plugin, int, error) {
	items := []service.Plugin{}
	var total int
	keys := service.MatchingCardKeys("plugin", filter.Query)
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_plugins p JOIN platform_plugin_versions v ON v.id=p.current_version_id WHERE `+pluginListFilter, t.model.workspace, filter.Scope, filter.Query, keys).Scan(&total); e != nil {
		return items, 0, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+pluginCols+` FROM platform_plugins p JOIN platform_plugin_versions v ON v.id=p.current_version_id WHERE `+pluginListFilter+` ORDER BY p.created_at DESC,p.id DESC LIMIT $5 OFFSET $6`, t.model.workspace, filter.Scope, filter.Query, keys, n, (p-1)*n)
	if e != nil {
		return items, 0, e
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked.
	for rows.Next() {
		v, e := scanPlugin(rows)
		if e != nil {
			return items, 0, e
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}
func (t *pluginTx) Versions(ctx context.Context, id string, p, n int) ([]service.Plugin, int, error) {
	items := []service.Plugin{}
	var total int
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_plugin_versions WHERE (workspace_id=NULLIF($1,'')::uuid OR workspace_id IS NULL) AND plugin_id=$2`, t.model.workspace, id).Scan(&total); e != nil {
		return items, 0, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+pluginVersionCols+` FROM platform_plugins p JOIN platform_plugin_versions v ON v.plugin_id=p.id WHERE (p.workspace_id=NULLIF($1,'')::uuid OR p.workspace_id IS NULL) AND p.archived_at IS NULL AND p.id=$2 ORDER BY v.version DESC,v.id DESC LIMIT $3 OFFSET $4`, t.model.workspace, id, n, (p-1)*n)
	if e != nil {
		return items, 0, e
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked.
	for rows.Next() {
		v, e := scanPlugin(rows)
		if e != nil {
			return items, 0, e
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}
func (t *pluginTx) Save(ctx context.Context, p service.Plugin, create, content bool) error {
	var e error
	if create {
		_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_plugins(id,workspace_id,name,description,enabled,version,current_version_id) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7)`, p.ID, t.model.workspace, p.Name, p.Description, p.Enabled, p.Version, p.VersionID)
	} else {
		var r sql.Result
		r, e = t.model.tx.ExecContext(ctx, `UPDATE platform_plugins SET name=$3,description=$4,enabled=$5,version=$6,current_version_id=$7,updated_at=now() WHERE workspace_id IS NOT DISTINCT FROM NULLIF($1,'')::uuid AND id=$2 AND version=$6-1`, t.model.workspace, p.ID, p.Name, p.Description, p.Enabled, p.Version, p.VersionID)
		if e == nil {
			var n int64
			n, e = r.RowsAffected()
			if e == nil && n != 1 {
				return service.Fail("plugin_version_conflict", 409, "插件已更新，请刷新")
			}
		}
	}
	if e != nil || !content {
		return e
	}
	var client any
	var resource, scopes, issuer string
	if p.OAuth != nil {
		if p.OAuth.ID != "" {
			client = p.OAuth.ID
		}
		issuer = p.OAuth.Issuer
		resource = p.OAuth.Resource
		scopes = p.OAuth.Scopes
		if p.ClientCredential != nil {
			if e = t.saveClient(ctx, p); e != nil {
				return e
			}
		}
	}
	_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_plugin_versions(id,workspace_id,plugin_id,version,name,description,endpoint,auth_type,header_name,oauth_client_id,oauth_resource,oauth_scopes,oauth_issuer) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, p.VersionID, t.model.workspace, p.ID, p.Version, p.Name, p.Description, p.Endpoint, p.AuthType, p.HeaderName, client, resource, scopes, issuer)
	return e
}
func (s *PluginStore) WithConnection(ctx context.Context, w, id string, fn func(service.PluginTx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // No-op after commit.
	var locked string
	if e = tx.QueryRowContext(ctx, "SELECT id FROM platform_plugin_connections WHERE workspace_id=$1 AND id=$2 FOR UPDATE", w, id).Scan(&locked); e != nil {
		return workspaceDBError(e)
	}
	if e = fn(&pluginTx{model: &modelTx{workspace: w, tx: tx}}); e != nil {
		return e
	}
	return tx.Commit()
}
