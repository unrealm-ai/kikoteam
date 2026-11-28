package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/defaultplugins"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type EmployeeManagementStore struct {
	DB *sql.DB
}

func managementDenied() error {
	return service.Fail("employee_management_forbidden", 403, "你没有管理这位 AI 员工的权限，请联系管理员")
}

// Authorization for management MCP is separate from a normal task resuming with its original actor.
func liveManagementCaller(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller) (bool, error) {
	if v.Workspace != t.workspace || !service.ValidWorkspaceSubject(v.Actor) || !service.ValidWorkspaceSubject(v.Input) ||
		!service.ValidWorkspaceSubject(v.Employee) {
		return false, managementDenied()
	}
	var assistant bool
	err := t.tx.QueryRowContext(ctx, `SELECT COALESCE(w.assistant_agent_id=s.agent_id,false)
 FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 JOIN platform_workspaces w ON w.id=s.workspace_id
 JOIN manager_conversations c ON c.id=s.conversation_id
 JOIN platform_agents a ON a.id=s.agent_id AND a.workspace_id=w.id
 WHERE w.id=$1 AND s.agent_id=$2 AND i.id=$3 AND s.mode='task' AND s.lifecycle='active'
 AND i.status='started' AND i.turn_id=s.active_turn_id AND s.stop_revision=s.applied_stop_revision
 AND c.lifecycle='active' AND a.enabled AND i.admission_epoch=a.admission_epoch
 AND ($6='' OR (s.id=NULLIF($6,'')::uuid AND s.engine_id=NULLIF($7,'')::uuid
   AND s.assignment_id=NULLIF($8,'')::uuid AND s.generation=$9))
 AND NOT EXISTS(SELECT FROM manager_human_chat_inputs h WHERE h.input_id=i.id)
 AND i.source_kind='user' AND i.trigger_agent_loop AND i.trigger_event_id IS NULL AND i.work_assignment_id IS NULL
 AND i.confirmation_request_id IS NULL AND s.trigger_id IS NULL AND manager_input_actor_allowed(i.id)
 AND NOT EXISTS(SELECT FROM manager_tasks k JOIN manager_inputs orig ON orig.id=k.source_chat_input_id
   WHERE k.id=s.task_id AND orig.source_kind<>'user' AND i.seq=1)
 AND (CASE WHEN $5::boolean THEN i.channel_principal_id=$4::uuid AND i.submitted_by IS NULL AND c.channel_chat_type='direct'
 ELSE i.submitted_by=$4::uuid AND i.channel_principal_id IS NULL AND c.web_user_id=i.submitted_by END)
 FOR SHARE OF s,i`, t.workspace, v.Employee, v.Input, v.Actor, v.Channel, v.Session, v.Engine, v.Assignment, v.Generation).Scan(&assistant)
	if errors.Is(err, sql.ErrNoRows) {
		return false, managementDenied()
	}
	return assistant, err
}

func managementTarget(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller, target string) error {
	assistant, err := liveManagementCaller(ctx, t, v)
	if err != nil {
		return err
	}
	if (!assistant || v.Channel) && target != v.Employee {
		return managementDenied()
	}
	return nil
}

// Acquire the same rows modified by usage/binding changes. Check policy in a
// separate statement after acquiring the locks so it sees a revoke we waited for.
// The caller already holds the workspace lock through ModelStore.
func lockManagementSource(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller, target string) error {
	if !service.ValidWorkspaceSubject(v.Session) || !service.ValidWorkspaceSubject(v.Engine) ||
		!service.ValidWorkspaceSubject(v.Assignment) || v.Generation < 1 || !service.ValidWorkspaceSubject(v.Input) ||
		!service.ValidWorkspaceSubject(v.Employee) || !service.ValidWorkspaceSubject(v.Actor) ||
		!service.ValidWorkspaceSubject(target) || v.Workspace != t.workspace {
		return managementDenied()
	}
	// Match admission: workspace, employees, conversation, channel, session.
	// No-key update also permits the normal FK reads on employees.
	var count int
	err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT id FROM platform_agents WHERE workspace_id=$1
 AND id IN ($2,$3) ORDER BY id FOR NO KEY UPDATE) AS locked`, t.workspace, v.Employee, target).Scan(&count)
	if err != nil {
		return err
	}
	expected := 1
	if v.Employee != target {
		expected++
	}
	if count != expected {
		return managementDenied()
	}
	var id string
	err = t.tx.QueryRowContext(ctx, `SELECT c.id FROM manager_conversations c
 JOIN manager_sessions s ON s.conversation_id=c.id
 WHERE s.id=$1 AND c.workspace_id=$2 FOR SHARE OF c NOWAIT`, v.Session, t.workspace).Scan(&id)
	if err != nil {
		return denyMissingManagementRow(err)
	}
	if v.Channel {
		if err = lockManagementChannel(ctx, t, v); err != nil {
			return err
		}
	}
	err = t.tx.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_inputs i ON i.session_id=s.id
 WHERE s.id=$1 AND i.id=$2 AND s.workspace_id=$3 FOR SHARE OF s,i NOWAIT`, v.Session, v.Input, t.workspace).Scan(&id)
	return denyMissingManagementRow(err)
}

func lockManagementChannel(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller) error {
	var binding, locked string
	err := t.tx.QueryRowContext(ctx, `SELECT channel_binding_id FROM manager_conversations c
 JOIN manager_sessions s ON s.conversation_id=c.id WHERE s.id=$1 AND c.channel_binding_id IS NOT NULL`, v.Session).Scan(&binding)
	if err != nil {
		return denyMissingManagementRow(err)
	}
	err = t.tx.QueryRowContext(ctx, `SELECT id FROM platform_channel_bindings
 WHERE id=$1 AND workspace_id=$2 AND agent_id=$3 FOR SHARE`, binding, t.workspace, v.Employee).Scan(&locked)
	if err != nil {
		return denyMissingManagementRow(err)
	}
	err = t.tx.QueryRowContext(ctx, `SELECT id FROM manager_channel_principals
 WHERE id=$1 AND workspace_id=$2 AND binding_id=$3 FOR SHARE`, v.Actor, t.workspace, binding).Scan(&locked)
	return denyMissingManagementRow(err)
}

func denyMissingManagementRow(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return managementDenied()
	}
	var lockErr *pgconn.PgError
	if errors.As(err, &lockErr) && lockErr.Code == "55P03" {
		return service.Fail("employee_management_busy", 409, "当前任务状态正在更新，请稍后重试")
	}
	return err
}

func authorizeManagementWrite(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller, target string) error {
	if err := lockManagementSource(ctx, t, v, target); err != nil {
		return err
	}
	if err := managementTarget(ctx, t, v, target); err != nil {
		return err
	}
	allowed, err := managementPermission(ctx, t, v, target)
	if err == nil && !allowed {
		return managementDenied()
	}
	return err
}

func managementPermission(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller, target string) (bool, error) {
	if !v.Channel {
		return canManageEmployee(ctx, t.tx, t.workspace, target, v.Actor)
	}
	// Channel maintenance grants are binding-scoped; never compare app-local IDs across bots.
	var binding string
	err := t.tx.QueryRowContext(ctx, `SELECT b.id FROM manager_channel_principals p
 JOIN platform_channel_bindings b ON b.id=p.binding_id AND b.workspace_id=p.workspace_id
 JOIN manager_inputs i ON i.id=$4 AND i.channel_principal_id=p.id
 JOIN platform_agent_channel_maintainers m ON m.workspace_id=p.workspace_id AND m.agent_id=b.agent_id
    AND m.binding_id=p.binding_id AND m.identity_epoch=p.identity_epoch AND m.external_id=p.external_user_id
 WHERE p.workspace_id=$1 AND p.id=$2 AND m.agent_id=$3 AND b.enabled AND b.identity_epoch=p.identity_epoch
   AND b.provider=p.provider AND b.external_tenant_id=p.external_tenant_id
   AND b.identity_verified_generation=b.connection_generation AND i.channel_generation=b.connection_generation
 FOR SHARE OF b`, t.workspace, v.Actor, target, v.Input).Scan(&binding)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *EmployeeManagementStore) inWorkspace(ctx context.Context, v service.EmployeeManagementCaller, f func(*modelTx) error) error {
	return (&ModelStore{DB: s.DB}).WithWorkspace(ctx, v.Workspace, func(t service.ModelTx) error { return f(t.(*modelTx)) })
}

func (s *EmployeeManagementStore) Read(ctx context.Context, v service.EmployeeManagementCaller, target string) (service.Agent, error) {
	var out service.Agent
	err := s.inWorkspace(ctx, v, func(t *modelTx) error {
		if err := managementTarget(ctx, t, v, target); err != nil {
			return err
		}
		var err error
		out, err = (&agentTx{model: t}).Get(ctx, target)
		if err != nil {
			return err
		}
		out.CanManage, err = managementPermission(ctx, t, v, target)
		if err != nil {
			return err
		}
		// Only maintainers can inspect this employee's payer binding.
		payerTarget := ""
		if out.CanManage {
			payerTarget = target
		}
		out.ManagementDefaults, err = defaultplugins.List(ctx, t.tx, t.workspace, payerTarget)
		if err != nil {
			return err
		}
		for i := range out.ManagementDefaults {
			d := &out.ManagementDefaults[i]
			if d.Status == "available" {
				switch {
				case !out.Enabled:
					d.Status = "employee_disabled"
				case !out.CanManage:
					d.Status = "platform_enabled"
				}
			}
		}
		if !out.CanManage {
			return nil
		}
		return t.tx.QueryRowContext(ctx, `SELECT COALESCE('m'||(
 SELECT management_number FROM platform_model_profiles WHERE workspace_id=$1 AND id=NULLIF($2,'')::uuid
 UNION ALL
 SELECT management_number FROM platform_proxy_models WHERE id=NULLIF($3,'')::uuid)::text,'')`,
			t.workspace, out.ModelProfileID, out.PlatformModelID).Scan(&out.ManagementModelRef)
	})
	return out, err
}

func (s *EmployeeManagementStore) Employees(ctx context.Context, v service.EmployeeManagementCaller, q service.ManagementQuery) (service.ManagementPage[service.EmployeeSummary], error) {
	return managementList(ctx, s, v, q, false, func(t *modelTx) error { return authorizeAssistantWeb(ctx, t, v) }, `SELECT id,name,role,enabled,created_at FROM platform_agents WHERE workspace_id=$1`,
		func(r *sql.Rows) (service.EmployeeSummary, error) {
			var a service.EmployeeSummary
			e := r.Scan(&a.ID, &a.Name, &a.Role, &a.Enabled)
			return a, e
		},
		"id,name,role,enabled")
}

var managementResources = map[string]string{
	"model": `SELECT id,name,description AS summary,management_number,'workspace' AS source,'' AS version_id,version::text AS version,created_at FROM platform_model_profiles WHERE workspace_id=$1 AND enabled AND validated
 UNION ALL SELECT id,name,'' AS summary,management_number,'platform','',version::text,created_at FROM platform_proxy_models WHERE enabled AND validated`,
	"skill": `SELECT s.id,s.name,COALESCE(NULLIF(s.description,''),v.description) AS summary,CASE WHEN s.workspace_id IS NULL THEN 'platform' ELSE 'workspace' END AS source,
 v.id::text AS version_id,COALESCE(NULLIF(v.version_label,''),v.sequence::text) AS version,s.created_at FROM platform_skills s JOIN platform_skill_versions v ON v.id=s.current_version_id
 WHERE (s.workspace_id=$1 OR s.workspace_id IS NULL) AND s.enabled AND s.listed AND s.archived_at IS NULL`,
	"mcp": `SELECT p.id,p.name,COALESCE(NULLIF(p.description,''),v.description) AS summary,CASE WHEN p.workspace_id IS NULL THEN 'platform' ELSE 'workspace' END AS source,
 v.id::text AS version_id,v.version::text AS version,v.distribution,p.created_at FROM platform_plugins p JOIN platform_plugin_versions v ON v.id=p.current_version_id
 WHERE (p.workspace_id=$1 OR p.workspace_id IS NULL) AND p.enabled AND p.listed AND p.archived_at IS NULL`,
}

func (s *EmployeeManagementStore) Resources(ctx context.Context, v service.EmployeeManagementCaller, target, kind string, q service.ManagementQuery) (service.ManagementPage[service.ManagementResource], error) {
	// Without a target this is model discovery for KIKO Web creation only.
	// The authorizer below checks the trusted live input and KIKO/Web scope.
	if target == "" && kind != "model" {
		return service.ManagementPage[service.ManagementResource]{}, managementDenied()
	}
	base, ok := managementResources[kind]
	if !ok {
		return service.ManagementPage[service.ManagementResource]{}, service.Fail("invalid_resource_type", 400, "资源类型应为 model、skill 或 mcp")
	}
	idColumn := "id"
	distribution := "''"
	if kind == "model" {
		idColumn = "'m'||management_number::text"
	}
	if kind == "mcp" {
		distribution = "distribution"
	}
	return managementList(ctx, s, v, q, kind == "model", func(t *modelTx) error { return authorizeResourceCatalog(ctx, t, v, target) }, base,
		func(r *sql.Rows) (service.ManagementResource, error) {
			var a service.ManagementResource
			e := r.Scan(&a.ID, &a.Name, &a.Summary, &a.Source, &a.VersionID, &a.Version, &a.Distribution)
			return a, e
		},
		idColumn+`,name,left(regexp_replace(summary,'[[:space:]]+',' ','g'),160),source,version_id,version,`+distribution)
}

func managementList[T any](ctx context.Context, s *EmployeeManagementStore, v service.EmployeeManagementCaller, q service.ManagementQuery, model bool, authorize func(*modelTx) error, base string, scan func(*sql.Rows) (T, error), columns string) (service.ManagementPage[T], error) {
	normalize := q.Normalize
	idFilter := "(NULLIF($2,'')::uuid IS NULL OR id=NULLIF($2,'')::uuid)"
	if model {
		normalize = q.NormalizeModels
		idFilter = "(NULLIF($2,'')::bigint IS NULL OR management_number=NULLIF($2,'')::bigint)"
	}
	if err := normalize(); err != nil {
		return service.ManagementPage[T]{}, err
	}
	id := q.ID
	if model {
		id = service.ManagementModelNumber(id)
	}
	out := service.ManagementPage[T]{Items: []T{}, Page: q.Page, PageSize: q.PageSize}
	err := s.inWorkspace(ctx, v, func(t *modelTx) error {
		if e := authorize(t); e != nil {
			return e
		}
		from := ` FROM (` + base + `) candidates WHERE ` + idFilter + ` AND ($3='' OR strpos(lower(name),lower($3))>0)`
		if e := t.tx.QueryRowContext(ctx, "SELECT count(*)"+from, t.workspace, id, q.Name).Scan(&out.Total); e != nil {
			return e
		}
		rows, e := t.tx.QueryContext(ctx, "SELECT "+columns+from+" ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5", t.workspace, id, q.Name, q.PageSize, (q.Page-1)*q.PageSize)
		if e != nil {
			return e
		}
		defer rows.Close() //nolint:errcheck // Checked during iteration.
		for rows.Next() {
			a, e := scan(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, a)
		}
		return rows.Err()
	})
	return out, err
}

// Web private chats share the same read boundary as the workspace catalog.
func authorizeResourceCatalog(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller, target string) error {
	if target == "" {
		return authorizeAssistantWeb(ctx, t, v)
	}
	if v.Channel {
		if err := managementTarget(ctx, t, v, target); err != nil {
			return err
		}
		allowed, err := managementPermission(ctx, t, v, target)
		if err == nil && !allowed {
			return managementDenied()
		}
		return err
	}
	if err := managementTarget(ctx, t, v, target); err != nil {
		return err
	}
	// Check active workspace membership explicitly. Being allowed to run an
	// employee is not a substitute for workspace catalog access.
	member, err := t.Member(ctx, v.Actor)
	if errors.Is(err, service.ErrWorkspaceMissing) || err == nil && member.Status != "active" {
		return service.Fail("workspace_forbidden", 403, "没有该工作区共享资源的查看权限")
	}
	return err
}

func authorizeAssistantWeb(ctx context.Context, t *modelTx, v service.EmployeeManagementCaller) error {
	assistant, err := liveManagementCaller(ctx, t, v)
	if err != nil {
		return err
	}
	if !assistant || v.Channel {
		return managementDenied()
	}
	member, err := t.Member(ctx, v.Actor)
	if errors.Is(err, service.ErrWorkspaceMissing) || err == nil && member.Status != "active" {
		return managementDenied()
	}
	return err
}

func (s *EmployeeManagementStore) ResolveModel(ctx context.Context, v service.EmployeeManagementCaller, ref string) (id, source string, err error) {
	err = s.inWorkspace(ctx, v, func(t *modelTx) error {
		if e := authorizeAssistantWeb(ctx, t, v); e != nil {
			return e
		}
		if ref != "" {
			return lookupManagementModel(ctx, t, ref, &id, &source)
		}
		return t.tx.QueryRowContext(ctx, `WITH candidates AS (`+candidates+`)
 SELECT id,item->>'source' FROM candidates WHERE preferred ORDER BY rank,id LIMIT 1`, t.workspace).Scan(&id, &source)
	})
	if errors.Is(err, sql.ErrNoRows) {
		if ref != "" {
			err = service.Fail("model_unavailable", 409, "找不到所选模型，请重新查询可用模型")
		} else {
			err = service.Fail("model_unavailable", 409, "请先配置工作区默认模型，或联系平台管理员配置平台默认模型")
		}
	}
	return
}

func lookupManagementModel(ctx context.Context, t *modelTx, ref string, id, source *string) error {
	number := service.ManagementModelNumber(ref)
	if number == "" {
		return service.Fail("invalid_employee_management_request", 400, "请使用模型查询返回的短 ID")
	}
	var available bool
	err := t.tx.QueryRowContext(ctx, `SELECT id,enabled AND validated FROM platform_model_profiles WHERE workspace_id=$1 AND management_number=$2::text::bigint`, t.workspace, number).Scan(id, &available)
	*source = "workspace"
	if errors.Is(err, sql.ErrNoRows) {
		*source = "platform"
		err = t.tx.QueryRowContext(ctx, `SELECT id,enabled AND validated FROM platform_proxy_models WHERE management_number=$1::text::bigint`, number).Scan(id, &available)
	}
	if err == nil && !available {
		return service.Fail("model_unavailable", 409, "所选模型未通过验证或已停用，请重新查询可用模型")
	}
	return err
}
