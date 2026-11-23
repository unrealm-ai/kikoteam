package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Catalog resources use the same IDs and immutable versions as employee package pins.
type ResourceCatalog struct {
	Plugins *service.PluginService
	DB      *sql.DB
	Assets  service.SkillAssets
}

func resourceTable(kind string) string {
	if kind == "skill" {
		return "platform_skills"
	}
	return "platform_plugins"
}
func (s *ResourceCatalog) with(ctx context.Context, w, a string, manage bool, fn func(*modelTx) error) error {
	return (&TemplateStore{DB: s.DB}).With(ctx, w, a, manage, func(t service.TemplateTx) error { return fn(t.(*templateTx).model) })
}
func (s *ResourceCatalog) Official(ctx context.Context, a, kind, id, view string, page, size int, query string) (out any, err error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, service.Fail("invalid_page", 400, "分页无效")
	}
	err = s.with(ctx, "", a, false, func(m *modelTx) error {
		var admin bool
		if e := m.tx.QueryRowContext(ctx, "SELECT is_platform_admin FROM platform_accounts WHERE id=$1", a).Scan(&admin); e != nil {
			return e
		}
		scope := "official"
		if admin {
			scope = "manage"
		}
		if kind == "skill" {
			t := &skillTx{m}
			if id == "" {
				items, total, e := t.List(ctx, page, size, service.ResourceListFilter{Scope: scope, Query: query})
				out = service.SkillPage{Items: items, Total: total, Page: page, PageSize: size}
				return e
			}
			row, e := t.Get(ctx, id)
			if e != nil {
				return e
			}
			if !row.Listed && !admin {
				return service.ErrWorkspaceMissing
			}
			if view == "versions" {
				items, total, e := t.Versions(ctx, id, page, size)
				out = map[string]any{"items": items, "total": total, "page": page, "page_size": size}
				return e
			}
			out = row
			return nil
		}
		t := &pluginTx{m}
		if id == "" {
			items, total, e := t.List(ctx, page, size, service.ResourceListFilter{Scope: scope, Query: query})
			out = service.PluginPage{Items: items, Total: total, Page: page, PageSize: size}
			return e
		}
		row, e := t.Get(ctx, id)
		if e != nil {
			return e
		}
		if !row.Listed && !admin {
			return service.ErrWorkspaceMissing
		}
		if view == "versions" {
			items, total, e := t.Versions(ctx, id, page, size)
			out = service.PluginPage{Items: items, Total: total, Page: page, PageSize: size}
			return e
		}
		out = row
		return nil
	})
	return
}

type publicationSource struct {
	skill  service.Skill
	pin    service.SkillVersion
	plugin service.Plugin
}

func loadPublicationSource(ctx context.Context, m *modelTx, kind string, r service.ResourcePublication, publishing bool) (src publicationSource, err error) {
	var locked string
	err = m.tx.QueryRowContext(ctx, "SELECT id FROM "+resourceTable(kind)+" WHERE id=$1 AND (workspace_id=$2 OR workspace_id IS NULL) AND archived_at IS NULL FOR SHARE", r.ResourceID, m.workspace).Scan(&locked)
	if err != nil {
		return src, workspaceDBError(err)
	}
	if kind == "skill" {
		t := &skillTx{m}
		src.skill, err = t.Get(ctx, r.ResourceID)
		if err != nil {
			return
		}
		if !src.skill.Enabled || !src.skill.Listed || publishing && src.skill.Scope == "official" {
			return src, service.Fail("resource_unavailable", 409, "来源技能不可发布或复制")
		}
		src.pin, err = t.GetVersion(ctx, r.ResourceID, r.VersionID)
	} else {
		src.plugin, err = (&pluginTx{m}).GetVersion(ctx, r.ResourceID, r.VersionID)
		if err == nil && (!src.plugin.Enabled || !src.plugin.Listed || publishing && src.plugin.Scope == "official") {
			err = service.Fail("resource_unavailable", 409, "来源插件不可发布或复制")
		}
	}
	return
}
func (s *ResourceCatalog) Publish(ctx context.Context, a, kind, dest, id string, r service.ResourcePublication) (out any, err error) {
	if r.SourceWorkspaceID == "" || (dest != "" && dest != r.SourceWorkspaceID) {
		return nil, service.Fail("invalid_source", 400, "请选择来源空间和固定版本")
	}
	if (id != "" && r.Version < 1) || (id == "" && r.Version != 0) {
		return nil, service.Fail("version_required", 400, "资源版本无效")
	}
	create := id == ""
	if create {
		id = service.NewID()
	}
	versionID := service.NewID()
	err = s.with(ctx, dest, a, true, func(m *modelTx) error {
		srcTx := &modelTx{tx: m.tx, workspace: r.SourceWorkspaceID}
		if dest == "" {
			var locked string
			if e := m.tx.QueryRowContext(ctx, "SELECT id FROM platform_workspaces WHERE id=$1 FOR UPDATE", r.SourceWorkspaceID).Scan(&locked); e != nil {
				return workspaceDBError(e)
			}
			if e := serviceTemplateMember(ctx, srcTx, a, true); e != nil {
				return e
			}
		}
		src, e := loadPublicationSource(ctx, srcTx, kind, r, dest == "")
		if e != nil {
			return e
		}
		version := int64(1)
		if !create {
			var previous int64
			var builtin bool
			if e := m.tx.QueryRowContext(ctx, "SELECT version,builtin_key IS NOT NULL FROM "+resourceTable(kind)+" WHERE id=$1 AND workspace_id IS NULL AND archived_at IS NULL FOR UPDATE", id).Scan(&previous, &builtin); e != nil {
				return workspaceDBError(e)
			}
			if builtin {
				return service.Fail("builtin_readonly", 409, "仓库内置资源通过部署发布新版本")
			}
			if previous != r.Version {
				return service.Fail("resource_version_conflict", 409, "资源已更新，请刷新")
			}
			version = previous + 1
		}
		if kind == "skill" {
			t := &skillTx{m}
			v := src.pin
			v.Sequence = 1
			if !create {
				old, e := t.Get(ctx, id)
				if e != nil {
					return e
				}
				v.Sequence = old.CurrentVersion.Sequence + 1
			}
			v.ID = versionID
			if src.pin.StorageIdentity != s.Assets.Identity() {
				return service.Fail("skill_storage_changed", 409, "来源资产存储已变更")
			}
			v.KeyPrefix = fmt.Sprintf("official/skills/%s/versions/%s", id, versionID)
			if dest != "" {
				v.KeyPrefix = fmt.Sprintf("workspaces/%s/skills/%s/versions/%s", dest, id, versionID)
			}
			if e := s.Assets.Copy(ctx, src.pin.KeyPrefix, v.KeyPrefix, src.pin.Manifest); e != nil {
				return service.Fail("skill_storage_unavailable", 503, "无法复制并校验技能资产")
			}
			v.Source = service.SkillSource{Type: "official"}
			if dest != "" {
				v.Source.Type = "copy"
			}
			resource := service.Skill{ID: id, Name: src.skill.Name, Description: v.Description, Enabled: true, Version: version, CurrentVersionID: v.ID}
			if e := t.Save(ctx, resource, v, create); e != nil {
				return e
			}
			out, err = t.Get(ctx, id)
			return err
		}
		t := &pluginTx{m}
		p := src.plugin
		if p.OAuth != nil {
			config := *p.OAuth
			config.ID = ""
			config.ClientID = ""
			p.OAuth = &config
		}
		p.ClientCredential = nil
		if p.AuthType == "oauth" {
			if r.OAuth != nil {
				if s.Plugins == nil {
					return service.Fail("oauth_unavailable", 503, "OAuth 尚未配置")
				}
				if e := s.Plugins.ConfigureOAuth(dest, &p, r.OAuth); e != nil {
					return e
				}
			} else if dest == "" {
				return service.Fail("oauth_client_required", 400, "发布时请提供官方 OAuth 应用")
			}
		}
		p.ID = id
		p.VersionID = versionID
		p.Version = version
		p.Enabled = true
		if !create {
			current, e := t.Get(ctx, id)
			if e != nil {
				return e
			}
			p.Enabled = current.Enabled
		}
		if e := t.Save(ctx, p, create, true); e != nil {
			return e
		}
		out, err = t.Get(ctx, id)
		return err
	})
	return
}
func (s *ResourceCatalog) Change(ctx context.Context, a, kind, id string, r service.ResourceStateChange) (out any, err error) {
	if r.Version < 1 || (r.Listed == nil && r.Enabled == nil) {
		return nil, service.Fail("invalid_state", 400, "需要版本和状态")
	}
	err = s.with(ctx, "", a, true, func(m *modelTx) error {
		result, e := m.tx.ExecContext(ctx, "UPDATE "+resourceTable(kind)+" SET listed=COALESCE($3,listed),enabled=COALESCE($4,enabled),version=version+1,updated_at=now() WHERE id=$1 AND workspace_id IS NULL AND version=$2", id, r.Version, r.Listed, r.Enabled)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return service.Fail("resource_version_conflict", 409, "资源不存在或版本已更新")
		}
		if kind == "skill" {
			out, e = (&skillTx{m}).Get(ctx, id)
		} else {
			out, e = (&pluginTx{m}).Get(ctx, id)
		}
		return e
	})
	return
}
func (s *ResourceCatalog) Archive(ctx context.Context, w, a, kind, id string, version int64) error {
	return s.with(ctx, w, a, true, func(m *modelTx) error {
		table := resourceTable(kind)
		col := kind + "_id"
		var rev int64
		if err := m.tx.QueryRowContext(ctx, "SELECT version FROM "+table+" WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL FOR UPDATE", w, id).Scan(&rev); err != nil {
			return workspaceDBError(err)
		}
		if rev != version {
			return service.Fail("resource_version_conflict", 409, "资源已更新")
		}
		var references string
		query := `SELECT COALESCE(string_agg(label,'、'),'') FROM (
			SELECT COALESCE(a.name || ' / ','能力包模板 / ') || p.name AS label
			FROM platform_capability_package_` + kind + `s b
			JOIN platform_capability_packages p ON p.id=b.package_id
			LEFT JOIN platform_agents a ON a.id=p.agent_id
			WHERE b.workspace_id=$1 AND b.` + col + `=$2
			UNION
			SELECT CASE WHEN t.owner_workspace_id=$1 THEN '员工模板 / ' || (v.content->>'name') ELSE '官方员工模板' END
			FROM platform_agent_template_version_resources r
			JOIN platform_agent_template_versions v ON v.id=r.template_version_id
			JOIN platform_agent_templates t ON t.id=v.template_id
			WHERE r.source_workspace_id=$1 AND r.` + col + `=$2
			  AND (t.latest_version_id=v.id OR (t.is_listed AND t.published_version_id=v.id))
			ORDER BY 1 LIMIT 5
		) uses`
		if err := m.tx.QueryRowContext(ctx, query, w, id).Scan(&references); err != nil {
			return err
		}
		if references != "" {
			return service.Fail("resource_in_use", 409, "请先解除资源引用。当前引用包括"+references)
		}
		_, e := m.tx.ExecContext(ctx, "UPDATE "+table+" SET enabled=false,archived_at=now(),updated_at=now(),version=version+1 WHERE id=$1", id)
		return e
	})
}
func (s *ResourceCatalog) Uses(ctx context.Context, w, a, kind, id string, page, size int) (any, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, service.Fail("invalid_page", 400, "分页无效")
	}
	items := []service.ResourceUse{}
	var total int
	err := s.with(ctx, w, a, true, func(m *modelTx) error {
		var exists bool
		if e := m.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM "+resourceTable(kind)+" WHERE id=$2 AND (workspace_id=$1 OR workspace_id IS NULL))", w, id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return service.ErrWorkspaceMissing
		}
		from := " FROM platform_capability_package_" + kind + "s b JOIN platform_capability_packages p ON p.id=b.package_id JOIN platform_agents a ON a.id=p.agent_id"

		extra := "false,''"
		if kind == "plugin" {
			from += " JOIN platform_plugin_versions def ON def.id=b.version_id LEFT JOIN platform_plugin_connections acct ON acct.workspace_id=b.workspace_id AND acct.agent_id IS NOT DISTINCT FROM platform_plugin_connection_owner(b.workspace_id,a.id,b.plugin_id) AND acct.plugin_id=b.plugin_id AND acct.plugin_version_id=b.version_id LEFT JOIN platform_agent_plugin_connection_settings setting ON setting.workspace_id=b.workspace_id AND setting.agent_id=a.id AND setting.plugin_id=b.plugin_id"
			extra = "COALESCE(acct.enabled AND (acct.validated OR def.auth_type='none'),def.auth_type='none'),COALESCE(setting.source,'workspace')"
		}
		from += " WHERE b.workspace_id=$1 AND b." + kind + "_id=$2"
		if e := m.tx.QueryRowContext(ctx, "SELECT count(*)"+from, w, id).Scan(&total); e != nil {
			return e
		}
		rows, e := m.tx.QueryContext(ctx, "SELECT a.id,a.name,p.id,p.name,b.version_id,p.enabled,"+extra+from+" ORDER BY a.id,p.id LIMIT $3 OFFSET $4", w, id, size, (page-1)*size)
		if e != nil {
			return e
		}
		defer rows.Close() //nolint:errcheck // Iteration errors are checked below.
		for rows.Next() {
			var v service.ResourceUse
			if e = rows.Scan(&v.AgentID, &v.AgentName, &v.PackageID, &v.PackageName, &v.VersionID, &v.Enabled, &v.AccountReady, &v.ConnectionSource); e != nil {
				return e
			}
			items = append(items, v)
		}
		return rows.Err()
	})
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, err
}
