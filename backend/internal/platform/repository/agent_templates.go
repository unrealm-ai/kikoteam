package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
)

type TemplateStore struct {
	DB        *sql.DB
	EnsureKey AgentKeyWriter
}
type templateTx struct {
	model  *modelTx
	actor  string
	manage bool
}

func escapeLike(s string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(s)
}
func (s *TemplateStore) With(ctx context.Context, w, a string, manage bool, fn func(service.TemplateTx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort rollback after commit or the original error.
	t := &templateTx{model: &modelTx{tx: tx, workspace: w}, actor: a, manage: manage}
	if w != "" {
		var id string
		if e = tx.QueryRowContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR UPDATE`, w).Scan(&id); e != nil {
			return workspaceDBError(e)
		}
		if e = serviceTemplateMember(ctx, t.model, a, manage); e != nil {
			return e
		}
	} else if manage {
		var yes bool
		if e = tx.QueryRowContext(ctx, `SELECT is_platform_admin FROM platform_accounts WHERE id=$1 FOR SHARE`, a).Scan(&yes); e != nil {
			return e
		}
		if !yes {
			return service.Fail("platform_admin_required", 403, "需要平台管理员权限")
		}
	}
	if e = fn(t); e != nil {
		return e
	}
	return tx.Commit()
}
func serviceTemplateMember(ctx context.Context, t *modelTx, a string, write bool) error {
	m, e := t.Member(ctx, a)
	if e != nil {
		return service.Fail("workspace_forbidden", 403, "没有工作区权限")
	}
	if m.Status != "active" || write && (m.Role != "owner" && m.Role != "admin") {
		return service.Fail("workspace_forbidden", 403, "没有管理权限")
	}
	return nil
}

const templateCols = `t.id,t.scope,COALESCE(t.builtin_key,''),COALESCE(t.owner_workspace_id::text,''),t.version,t.latest_version_id,COALESCE(t.published_version_id::text,''),t.is_listed,t.recommended_rank,t.created_at,t.updated_at,v.id,v.sequence,v.content,v.published_at`

func scanTemplate(row scanner) (service.AgentTemplate, error) {
	var v service.AgentTemplate
	var raw []byte
	e := row.Scan(&v.ID, &v.Scope, &v.BuiltinKey, &v.OwnerWorkspaceID, &v.Version, &v.LatestVersionID, &v.PublishedVersionID, &v.IsListed, &v.RecommendedRank, &v.CreatedAt, &v.UpdatedAt, &v.TemplateVersionID, &v.Sequence, &raw, &v.PublishedAt)
	if e != nil {
		return v, workspaceDBError(e)
	}
	e = json.Unmarshal(raw, &v.Content)
	v.HasUnpublishedChanges = v.LatestVersionID != v.PublishedVersionID
	v.Status = "draft"
	if v.PublishedVersionID != "" {
		v.Status = "unlisted"
		if v.IsListed {
			v.Status = "published"
		}
	}
	return v, e
}
func (t *templateTx) scope() (string, any) {
	if t.model.workspace == "" {
		return "official", nil
	}
	return "workspace", t.model.workspace
}
func (t *templateTx) sanitize(v *service.AgentTemplate) {
	if !t.manage {
		for i := range v.Content.Resources {
			if v.BuiltinKey != "" && v.Content.Resources[i].WorkspaceID == "" {
				continue
			}
			v.Content.Resources[i].WorkspaceID = ""
			v.Content.Resources[i].ID = ""
			v.Content.Resources[i].VersionID = ""
		}
		v.LatestVersionID = ""
		v.HasUnpublishedChanges = false
	}
}
func (t *templateTx) Get(ctx context.Context, id string, manage bool) (service.AgentTemplate, error) {
	scope, w := t.scope()
	pointer := "published_version_id"
	if manage {
		pointer = "latest_version_id"
	}
	v, e := scanTemplate(t.model.tx.QueryRowContext(ctx, `SELECT `+templateCols+` FROM platform_agent_templates t JOIN platform_agent_template_versions v ON v.id=t.`+pointer+` WHERE t.id=$1 AND t.scope=$2 AND t.owner_workspace_id IS NOT DISTINCT FROM $3::uuid`, id, scope, w))
	if e == nil {
		t.sanitize(&v)
	}
	return v, e
}
func (t *templateTx) List(ctx context.Context, q service.TemplateQuery) (service.TemplatePage, error) {
	out := service.TemplatePage{Items: []service.AgentTemplate{}, Page: q.Page, PageSize: q.PageSize}
	scope, w := t.scope()
	pointer := "published_version_id"
	if t.manage {
		pointer = "latest_version_id"
	}
	clause := ` FROM platform_agent_templates t JOIN platform_agent_template_versions v ON v.id=t.` + pointer + ` WHERE t.scope=$1 AND t.owner_workspace_id IS NOT DISTINCT FROM $2::uuid AND ($3 OR t.is_listed) AND ($4='' OR (v.content->>'name'||' '||COALESCE(v.content->>'role','')||' '||COALESCE(v.content->>'deliverables','')) ILIKE $5 ESCAPE '') AND ($6='' OR v.content->>'category'=$6) AND ($7='' OR CASE WHEN t.published_version_id IS NULL THEN 'draft' WHEN t.is_listed THEN 'published' ELSE 'unlisted' END=$7)`
	args := []any{scope, w, t.manage, q.Q, "%" + escapeLike(q.Q) + "%", q.Category, q.Status}
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*)`+clause, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	order := "t.recommended_rank"
	if q.SortBy == "published_at" {
		order = "v.published_at"
	}
	direction := " DESC"
	if q.SortOrder == "asc" {
		direction = " ASC"
	}
	order += direction + " NULLS LAST,v.published_at DESC NULLS LAST,t.id DESC"
	if t.manage {
		order = "t.updated_at DESC,t.id DESC"
	}
	args = append(args, q.PageSize, (q.Page-1)*q.PageSize)
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+templateCols+clause+` ORDER BY `+order+` LIMIT $8 OFFSET $9`, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are checked below.
	for rows.Next() {
		v, e := scanTemplate(rows)
		if e != nil {
			return out, e
		}
		t.sanitize(&v)
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func scanTemplateVersion(row scanner) (service.TemplateVersion, error) {
	var v service.TemplateVersion
	var raw []byte
	e := row.Scan(&v.ID, &v.TemplateID, &v.Sequence, &raw, &v.ChangeNote, &v.CreatedAt, &v.PublishedAt)
	if e != nil {
		return v, workspaceDBError(e)
	}
	e = json.Unmarshal(raw, &v.Content)
	return v, e
}
func (t *templateTx) Version(ctx context.Context, id, version string, manage bool) (service.TemplateVersion, error) {
	scope, w := t.scope()
	if version == "" {
		return service.TemplateVersion{}, service.ErrWorkspaceMissing
	}
	v, e := scanTemplateVersion(t.model.tx.QueryRowContext(ctx, `SELECT v.id,v.template_id,v.sequence,v.content,v.change_note,v.created_at,v.published_at FROM platform_agent_template_versions v JOIN platform_agent_templates t ON t.id=v.template_id WHERE t.id=$1 AND v.id=$2 AND t.scope=$3 AND t.owner_workspace_id IS NOT DISTINCT FROM $4::uuid AND ($5 OR v.published_at IS NOT NULL)`, id, version, scope, w, manage))
	if e == nil && !manage {
		for i := range v.Content.Resources {
			v.Content.Resources[i].WorkspaceID = ""
			v.Content.Resources[i].ID = ""
			v.Content.Resources[i].VersionID = ""
		}
	}
	return v, e
}
func (t *templateTx) Versions(ctx context.Context, id string, q service.TemplateQuery) (service.TemplateVersionPage, error) {
	out := service.TemplateVersionPage{Items: []service.TemplateVersion{}, Page: q.Page, PageSize: q.PageSize}
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_agent_template_versions WHERE template_id=$1 AND ($2 OR published_at IS NOT NULL)`, id, t.manage).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT id FROM platform_agent_template_versions WHERE template_id=$1 AND ($2 OR published_at IS NOT NULL) ORDER BY sequence DESC,id DESC LIMIT $3 OFFSET $4`, id, t.manage, q.PageSize, (q.Page-1)*q.PageSize)
	if e != nil {
		return out, e
	}
	ids := []string{}
	defer rows.Close() //nolint:errcheck // Rows are explicitly closed before reading version details.
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	if closeErr := rows.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return out, e
	}
	for _, version := range ids {
		v, e := t.Version(ctx, id, version, t.manage)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
func (t *templateTx) CheckResources(ctx context.Context, c service.TemplateContent, publish bool) error {
	for index, r := range c.Resources {
		if r.Kind != "skill" && r.Kind != "plugin" {
			return service.Fail("invalid_template_content", 400, "资源类型无效")
		}
		var officialReady bool
		query := "SELECT r.listed AND r.enabled AND r.archived_at IS NULL AND EXISTS (SELECT 1 FROM platform_" + r.Kind + "_versions v WHERE v." + r.Kind + "_id=r.id AND v.id=$2) FROM " + resourceTable(r.Kind) + " r WHERE r.id=$1 AND r.workspace_id IS NULL FOR SHARE"
		err := t.model.tx.QueryRowContext(ctx, query, r.ID, r.VersionID).Scan(&officialReady)
		if err == nil && officialReady {
			c.Resources[index].WorkspaceID = ""
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil || r.WorkspaceID == "" {
			return service.Fail("template_dependency_unavailable", 409, "官方资源不可用")
		}
		if t.model.workspace != "" && r.WorkspaceID != t.model.workspace {
			return service.Fail("invalid_template_content", 400, "不能引用其他工作区资源")
		}
		m := &modelTx{tx: t.model.tx, workspace: r.WorkspaceID}
		var id string
		if e := m.tx.QueryRowContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR UPDATE`, r.WorkspaceID).Scan(&id); e != nil {
			return e
		}
		if e := serviceTemplateMember(ctx, m, t.actor, true); e != nil {
			return e
		}
		var enabled bool
		var e error
		if r.Kind == "skill" {
			v, err := (&skillTx{model: m}).GetVersion(ctx, r.ID, r.VersionID)
			e = err
			_ = v
			if e == nil {
				e = m.tx.QueryRowContext(ctx, `SELECT enabled FROM platform_skills WHERE id=$1`, r.ID).Scan(&enabled)
			}
		} else {
			v, err := (&pluginTx{model: m}).GetVersion(ctx, r.ID, r.VersionID)
			e = err
			enabled = v.Enabled
		}
		if e != nil {
			return service.Fail("template_dependency_unavailable", 409, "固定资源版本不可用")
		}
		if publish && !enabled {
			return service.Fail("template_dependency_unavailable", 409, "固定资源已停用")
		}
	}
	return nil
}
func (t *templateTx) Save(ctx context.Context, v *service.AgentTemplate, c *service.TemplateContent, note string) error {
	var current int64
	e := t.model.tx.QueryRowContext(ctx, `SELECT version FROM platform_agent_templates WHERE id=$1 FOR UPDATE`, v.ID).Scan(&current)
	create := errors.Is(e, sql.ErrNoRows)
	if e != nil && !create {
		return e
	}
	if !create && current != v.Version {
		return service.Fail("template_revision_conflict", 409, "模板已更新")
	}
	sequence := int64(1)
	newContent := create
	if !create && c != nil {
		var old []byte
		var seq int64
		if e = t.model.tx.QueryRowContext(ctx, `SELECT content,sequence FROM platform_agent_template_versions WHERE id=$1`, v.LatestVersionID).Scan(&old, &seq); e != nil {
			return e
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		var equal bool
		if e = t.model.tx.QueryRowContext(ctx, `SELECT $1::jsonb=$2::jsonb`, old, raw).Scan(&equal); e != nil {
			return e
		}
		newContent = !equal
		sequence = seq + 1
	}
	if newContent {
		if !create {
			v.LatestVersionID = service.NewID()
		}
		raw, e := json.Marshal(c)
		if e != nil {
			return e
		}
		if _, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_agent_template_versions(id,template_id,sequence,content,change_note,created_by) VALUES($1,$2,$3,$4,$5,$6)`, v.LatestVersionID, v.ID, sequence, raw, note, t.actor); e != nil {
			return e
		}
		if e = saveTemplateRefs(ctx, t.model.tx, v.LatestVersionID, c.Resources); e != nil {
			return e
		}
	}
	if create {
		_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_agent_templates(id,scope,owner_workspace_id,latest_version_id,created_by) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5)`, v.ID, v.Scope, v.OwnerWorkspaceID, v.LatestVersionID, t.actor)
	} else {
		_, e = t.model.tx.ExecContext(ctx, `UPDATE platform_agent_templates SET latest_version_id=$2,is_listed=$3,recommended_rank=$4,version=version+1,updated_at=now() WHERE id=$1`, v.ID, v.LatestVersionID, v.IsListed, v.RecommendedRank)
	}
	return e
}
func (t *templateTx) Publish(ctx context.Context, v *service.AgentTemplate) error {
	result, e := t.model.tx.ExecContext(ctx, `UPDATE platform_agent_templates SET published_version_id=latest_version_id,is_listed=true,version=version+1,updated_at=now() WHERE id=$1 AND version=$2 AND latest_version_id=$3`, v.ID, v.Version, v.LatestVersionID)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return service.Fail("template_revision_conflict", 409, "模板已更新")
	}
	_, e = t.model.tx.ExecContext(ctx, `UPDATE platform_agent_template_versions SET published_by=COALESCE(published_by,$2),published_at=COALESCE(published_at,now()) WHERE id=$1`, v.LatestVersionID, t.actor)
	return e
}
func (t *templateTx) Delete(ctx context.Context, v service.AgentTemplate) error {
	result, e := t.model.tx.ExecContext(ctx, `DELETE FROM platform_agent_templates WHERE id=$1 AND version=$2 AND published_version_id IS NULL`, v.ID, v.Version)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return service.Fail("template_revision_conflict", 409, "模板已更新")
	}
	if _, e = t.model.tx.ExecContext(ctx, `DELETE FROM platform_agent_template_version_resources WHERE template_version_id IN(SELECT id FROM platform_agent_template_versions WHERE template_id=$1)`, v.ID); e != nil {
		return e
	}
	_, e = t.model.tx.ExecContext(ctx, `DELETE FROM platform_agent_template_versions WHERE template_id=$1`, v.ID)
	return e
}
