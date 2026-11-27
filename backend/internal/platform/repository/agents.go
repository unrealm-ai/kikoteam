package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type AgentKeyWriter func(context.Context, *sql.Tx, string, string) error
type AgentStore struct {
	DB        *sql.DB
	EnsureKey AgentKeyWriter
}

func NewAgentStore(db *sql.DB) *AgentStore { return &AgentStore{DB: db} }
func (s *AgentStore) WithWorkspace(ctx context.Context, w string, fn func(service.AgentTx) error) error {
	return (&ModelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ModelTx) error { return fn(&agentTx{model: t.(*modelTx), ensureKey: s.EnsureKey}) })
}

type agentTx struct {
	model     *modelTx
	ensureKey AgentKeyWriter
}

func (t *agentTx) Member(ctx context.Context, a string) (service.Member, error) {
	return t.model.Member(ctx, a)
}
func (t *agentTx) Model(ctx context.Context, id string) (service.ModelProfile, error) {
	return t.model.Get(ctx, id)
}

func (t *agentTx) OfficialModel(ctx context.Context, id string) (m service.ModelProfile, e error) {
	e = t.model.tx.QueryRowContext(ctx, `SELECT name,enabled,validated FROM platform_proxy_models WHERE id=$1 FOR SHARE`, id).Scan(&m.Name, &m.Enabled, &m.Validated)
	return m, workspaceDBError(e)
}
func (t *agentTx) EnsureModelKey(ctx context.Context, id string) error {
	if t.ensureKey == nil {
		return nil
	}
	return t.ensureKey(ctx, t.model.tx, t.model.workspace, id)
}

const agentModelJoins = ` LEFT JOIN platform_model_profiles m ON m.id=a.model_profile_id AND m.workspace_id=a.workspace_id
 LEFT JOIN platform_proxy_models o ON o.id=a.platform_model_id LEFT JOIN platform_agent_model_keys k ON k.agent_id=a.id
 LEFT JOIN platform_accounts p ON p.id=k.payer_account_id`
const agentColumns = `a.id,a.name,a.role,a.avatar,a.instruction,coalesce(a.model_profile_id::text,''),coalesce(a.platform_model_id::text,''),coalesce(m.name,o.name),coalesce(m.enabled AND m.validated,o.enabled AND o.validated),a.enabled,a.version,a.config_version,a.admission_epoch,a.created_at,a.updated_at,a.web_access,COALESCE(a.source_agent_template_version_id::text,''),a.template_parameters,COALESCE((SELECT template_id::text FROM platform_agent_template_versions WHERE id=a.source_agent_template_version_id),''),coalesce(p.id::text,''),coalesce(p.name,'')`

func scanAgent(row scanner, extra ...any) (service.Agent, error) {
	var a service.Agent
	var access, params []byte
	var source, template string
	var payer service.AgentPayer
	columns := []any{&a.ID, &a.Name, &a.Role, &a.Avatar, &a.Instruction, &a.ModelProfileID, &a.PlatformModelID, &a.ModelName, &a.ModelAvailable, &a.Enabled, &a.Version, &a.ConfigVersion, &a.AdmissionEpoch, &a.CreatedAt, &a.UpdatedAt, &access, &source, &params, &template, &payer.ID, &payer.Name}
	e := row.Scan(append(columns, extra...)...)
	if payer.ID != "" {
		a.Payer = &payer
	}
	if e == nil {
		e = json.Unmarshal(access, &a.WebAccess)
	}
	if e == nil && source != "" {
		origin := &service.TemplateOrigin{TemplateID: template, TemplateVersionID: source}
		e = json.Unmarshal(params, &origin.Parameters)
		a.TemplateOrigin = origin
	}
	a.Harness = "pi"
	return a, workspaceDBError(e)
}
func (t *agentTx) Get(ctx context.Context, id string) (service.Agent, error) {
	return scanAgent(t.model.tx.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM platform_agents a`+agentModelJoins+` WHERE a.workspace_id=$1 AND a.id=$2`, t.model.workspace, id))
}

// strpos treats wildcard characters in a search term literally.
const agentSearchScope = ` WHERE a.workspace_id=$1 AND ($2='' OR
    strpos(lower(a.name),lower($2))>0 OR strpos(lower(a.role),lower($2))>0)`

func (t *agentTx) List(ctx context.Context, q service.AgentListQuery) (service.AgentPage, error) {
	page := service.AgentPage{Items: []service.Agent{}, Page: q.Page, PageSize: q.PageSize}
	err := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE enabled),count(*) FILTER (WHERE NOT enabled)
        FROM platform_agents a`+agentSearchScope, t.model.workspace, q.Search).Scan(&page.Counts.Enabled, &page.Counts.Disabled)
	if err != nil {
		return page, err
	}
	page.Total = page.Counts.Enabled + page.Counts.Disabled
	if q.Enabled != nil {
		if *q.Enabled {
			page.Total = page.Counts.Enabled
		} else {
			page.Total = page.Counts.Disabled
		}
	}
	const resources = `,
        (SELECT coalesce(json_agg(b.provider ORDER BY b.provider),'[]')
            FROM platform_channel_bindings b WHERE b.workspace_id=a.workspace_id AND b.agent_id=a.id),
        (SELECT count(*) FROM platform_capability_packages c WHERE c.workspace_id=a.workspace_id AND c.agent_id=a.id),
        (SELECT count(*) FROM manager_triggers t WHERE t.workspace_id=a.workspace_id AND t.agent_id=a.id)`
	// Page the employees before evaluating any per-card aggregates, including on deep pages.
	pageFrom := ` FROM (SELECT a.* FROM platform_agents a` + agentSearchScope +
		` AND ($3::boolean IS NULL OR a.enabled=$3) ORDER BY a.created_at DESC,a.id DESC LIMIT $4 OFFSET $5) a`
	rows, err := t.model.tx.QueryContext(ctx, `SELECT `+agentColumns+resources+pageFrom+agentModelJoins+
		` ORDER BY a.created_at DESC,a.id DESC`, t.model.workspace, q.Search, q.Enabled, q.PageSize, (q.Page-1)*q.PageSize)
	if err != nil {
		return page, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	for rows.Next() {
		var resources service.AgentCardResources
		var channels []byte
		agent, err := scanAgent(rows, &channels, &resources.PackageCount, &resources.TriggerCount)
		if err != nil {
			return page, err
		}
		if err := json.Unmarshal(channels, &resources.Channels); err != nil {
			return page, err
		}
		agent.CardResources = &resources
		page.Items = append(page.Items, agent)
	}
	return page, rows.Err()
}

// Create is shared by manual, template and system provisioning. The creator
// must be the trusted Web account for the new employee, not a service identity.
func (t *agentTx) Create(ctx context.Context, a service.Agent, creator string) error {
	usage, err := json.Marshal(service.InitialAgentUsage())
	if err != nil {
		return err
	}
	w := t.model.workspace
	if _, err := t.model.tx.ExecContext(ctx, `INSERT INTO platform_agents(id,workspace_id,name,role,avatar,instruction,model_profile_id,enabled,version,config_version,admission_epoch,platform_model_id,usage_config)
 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13)`,
		a.ID, w, a.Name, a.Role, a.Avatar, a.Instruction, a.ModelProfileID, a.Enabled, a.Version, a.ConfigVersion, a.AdmissionEpoch, a.PlatformModelID, usage); err != nil {
		return err
	}
	if err := t.saveVersion(ctx, a); err != nil {
		return err
	}
	return t.createDefaultHumanRecipient(ctx, a.ID, creator)
}

func (t *agentTx) createDefaultHumanRecipient(ctx context.Context, agent, creator string) error {
	w := t.model.workspace
	ref, err := json.Marshal(service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: w, AgentID: agent, ExternalID: creator})
	if err != nil {
		return err
	}
	result, err := t.model.tx.ExecContext(ctx, `INSERT INTO platform_agent_human_recipients(agent_id,version,recipients)
 SELECT $2,1,jsonb_build_array(jsonb_build_object('id',$4::text,'name',candidate.name,'scope','','is_default',true,'ref',$3::jsonb))
 FROM (SELECT manager_human_recipient_name($1,$2,$3::jsonb) AS name) candidate WHERE candidate.name IS NOT NULL`,
		w, agent, ref, service.NewID())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("human_recipient_forbidden", 403, "员工创建人当前无权使用员工，无法设为默认确认人")
	}
	return err
}

func (t *agentTx) Update(ctx context.Context, a service.Agent, config bool) error {
	result, err := t.model.tx.ExecContext(ctx, `UPDATE platform_agents SET name=$3,role=$4,avatar=$5,instruction=$6,model_profile_id=NULLIF($7,'')::uuid,platform_model_id=NULLIF($12,'')::uuid,enabled=$8,version=$9,config_version=$10,admission_epoch=$11,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND version=$9-1`,
		a.ID, t.model.workspace, a.Name, a.Role, a.Avatar, a.Instruction, a.ModelProfileID, a.Enabled, a.Version, a.ConfigVersion, a.AdmissionEpoch, a.PlatformModelID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.Fail("agent_version_conflict", 409, "员工已更新，请刷新后重试")
	}
	if config {
		return t.saveVersion(ctx, a)
	}
	if a.WebAccess.Mode != "" {
		raw, err := json.Marshal(a.WebAccess)
		if err != nil {
			return err
		}
		_, err = t.model.tx.ExecContext(ctx, `UPDATE platform_agents SET web_access=$2 WHERE id=$1`, a.ID, raw)
		return err
	}
	return nil
}

func (t *agentTx) saveVersion(ctx context.Context, a service.Agent) error {
	_, err := t.model.tx.ExecContext(ctx, `INSERT INTO platform_agent_versions(workspace_id,agent_id,version,name,role,avatar,instruction,model_profile_id,platform_model_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid)`, t.model.workspace, a.ID, a.ConfigVersion, a.Name, a.Role, a.Avatar, a.Instruction, a.ModelProfileID, a.PlatformModelID)
	return err
}
