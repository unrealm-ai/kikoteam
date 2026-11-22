package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type SkillStore struct{ DB *sql.DB }

func NewSkillStore(db *sql.DB) *SkillStore { return &SkillStore{DB: db} }
func (s *SkillStore) WithWorkspace(ctx context.Context, w string, fn func(service.SkillTx) error) error {
	return (&ModelStore{DB: s.DB}).WithWorkspace(ctx, w, func(t service.ModelTx) error { return fn(&skillTx{model: t.(*modelTx)}) })
}

type skillTx struct{ model *modelTx }

func (t *skillTx) Member(ctx context.Context, a string) (service.Member, error) {
	return t.model.Member(ctx, a)
}

const skillListFilter = `s.archived_at IS NULL AND ((s.workspace_id=NULLIF($1,'')::uuid AND $2 IN ('workspace','all')) OR (s.workspace_id IS NULL AND ($2='manage' OR s.listed AND $2 IN ('official','all')))) AND ($3='' OR strpos(lower(s.name),lower($3))>0 OR strpos(lower(s.description),lower($3))>0 OR s.builtin_key=ANY($4::text[]))`

const skillColumns = `CASE WHEN s.workspace_id IS NULL THEN 'official' ELSE 'workspace' END,coalesce(s.builtin_key,''),s.listed,(SELECT count(DISTINCT pkg.agent_id) FROM platform_capability_package_skills b JOIN platform_capability_packages pkg ON pkg.id=b.package_id WHERE b.skill_id=s.id AND b.workspace_id=NULLIF($1,'')::uuid),s.id,s.name,s.description,s.enabled,s.version,s.current_version_id,s.created_at,s.updated_at,to_jsonb(v)`

func scanSkill(row scanner) (service.Skill, error) {
	var s service.Skill
	var key string
	var raw []byte
	e := row.Scan(&s.Scope, &key, &s.Listed, &s.AssignedAgentCount, &s.ID, &s.Name, &s.Description, &s.Enabled, &s.Version, &s.CurrentVersionID, &s.CreatedAt, &s.UpdatedAt, &raw)
	if e != nil {
		return s, workspaceDBError(e)
	}
	s.Card = service.BuiltinResourceCard("skill", key)
	e = json.Unmarshal(raw, &s.CurrentVersion)
	return s, e
}
func (t *skillTx) Get(ctx context.Context, id string) (service.Skill, error) {
	return scanSkill(t.model.tx.QueryRowContext(ctx, `SELECT `+skillColumns+` FROM platform_skills s JOIN platform_skill_versions v ON v.id=s.current_version_id WHERE (s.workspace_id=NULLIF($1,'')::uuid OR s.workspace_id IS NULL) AND s.archived_at IS NULL AND s.id=$2`, t.model.workspace, id))
}
func (t *skillTx) List(ctx context.Context, p, n int, filter service.ResourceListFilter) ([]service.Skill, int, error) {
	out := []service.Skill{}
	var total int
	keys := service.MatchingCardKeys("skill", filter.Query)
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_skills s WHERE `+skillListFilter, t.model.workspace, filter.Scope, filter.Query, keys).Scan(&total); e != nil {
		return out, 0, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+skillColumns+` FROM platform_skills s JOIN platform_skill_versions v ON v.id=s.current_version_id WHERE `+skillListFilter+` ORDER BY s.created_at DESC,s.id DESC LIMIT $5 OFFSET $6`, t.model.workspace, filter.Scope, filter.Query, keys, n, (p-1)*n)
	if e != nil {
		return out, 0, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	for rows.Next() {
		v, e := scanSkill(rows)
		if e != nil {
			return out, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func scanSkillVersion(row scanner) (service.SkillVersion, error) {
	var v service.SkillVersion
	var m, src []byte
	e := row.Scan(&v.ID, &v.Sequence, &v.VersionLabel, &v.TechnicalName, &v.Description, &m, &src, &v.StorageIdentity, &v.KeyPrefix, &v.CreatedAt)
	if e != nil {
		return v, workspaceDBError(e)
	}
	if e = json.Unmarshal(m, &v.Manifest); e != nil {
		return v, e
	}
	e = json.Unmarshal(src, &v.Source)
	return v, e
}

const skillVersionColumns = `id,sequence,version_label,technical_name,description,manifest,source,storage_identity,key_prefix,created_at`

func (t *skillTx) GetVersion(ctx context.Context, id, version string) (service.SkillVersion, error) {
	return scanSkillVersion(t.model.tx.QueryRowContext(ctx, `SELECT `+skillVersionColumns+` FROM platform_skill_versions WHERE (workspace_id=NULLIF($1,'')::uuid OR workspace_id IS NULL) AND skill_id=$2 AND id=$3`, t.model.workspace, id, version))
}
func (t *skillTx) Versions(ctx context.Context, id string, p, n int) ([]service.SkillVersion, int, error) {
	out := []service.SkillVersion{}
	var total int
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_skill_versions WHERE (workspace_id=NULLIF($1,'')::uuid OR workspace_id IS NULL) AND skill_id=$2`, t.model.workspace, id).Scan(&total); e != nil {
		return out, 0, e
	}
	rows, e := t.model.tx.QueryContext(ctx, `SELECT `+skillVersionColumns+` FROM platform_skill_versions WHERE (workspace_id=NULLIF($1,'')::uuid OR workspace_id IS NULL) AND skill_id=$2 ORDER BY sequence DESC LIMIT $3 OFFSET $4`, t.model.workspace, id, n, (p-1)*n)
	if e != nil {
		return out, 0, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	for rows.Next() {
		v, e := scanSkillVersion(rows)
		if e != nil {
			return out, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}
func (t *skillTx) Import(ctx context.Context, id string) (service.SkillImport, error) {
	var v service.SkillImport
	var m, src []byte
	e := t.model.tx.QueryRowContext(ctx, `SELECT id,actor_id,technical_name,description,manifest,source,storage_identity,key_prefix,ready,expires_at,COALESCE(published_skill_id::text,''),COALESCE(published_version_id::text,''),publish_fingerprint FROM platform_skill_imports WHERE workspace_id=$1 AND id=$2`, t.model.workspace, id).Scan(&v.ID, &v.ActorID, &v.TechnicalName, &v.Description, &m, &src, &v.StorageIdentity, &v.KeyPrefix, &v.Ready, &v.ExpiresAt, &v.PublishedSkillID, &v.PublishedVersionID, &v.PublishFingerprint)
	if e != nil {
		return v, workspaceDBError(e)
	}
	if e = json.Unmarshal(m, &v.Manifest); e != nil {
		return v, e
	}
	e = json.Unmarshal(src, &v.Source)
	if e == nil && v.Source.Type == "agent_template" {
		var plan struct {
			CandidateID        string `json:"candidate_id"`
			CandidateVersionID string `json:"candidate_version_id"`
			CandidatePrefix    string `json:"candidate_prefix"`
			ResourceKey        string `json:"resource_key"`
		}
		e = json.Unmarshal(src, &plan)
		v.Source.CandidateID = plan.CandidateID
		v.Source.CandidateVersionID = plan.CandidateVersionID
		v.Source.CandidatePrefix = plan.CandidatePrefix
		v.Source.ResourceKey = plan.ResourceKey
	}

	return v, e
}
func (t *skillTx) SaveImport(ctx context.Context, v service.SkillImport) error {
	var count int
	if e := t.model.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_skill_imports WHERE workspace_id=$1 AND actor_id=$2 AND expires_at>now() AND published_skill_id IS NULL`, t.model.workspace, v.ActorID).Scan(&count); e != nil {
		return e
	}
	if count >= 20 {
		return service.Fail("skill_import_limit", 429, "待发布导入过多，请先发布或等待过期")
	}
	m, e := json.Marshal(v.Manifest)
	if e != nil {
		return e
	}
	src, e := json.Marshal(v.Source)
	if e != nil {
		return e
	}
	_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_skill_imports(id,workspace_id,actor_id,technical_name,description,manifest,source,storage_identity,key_prefix,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, v.ID, t.model.workspace, v.ActorID, v.TechnicalName, v.Description, m, src, v.StorageIdentity, v.KeyPrefix, v.ExpiresAt)
	return e
}
func (t *skillTx) MarkReady(ctx context.Context, id string) error {
	_, e := t.model.tx.ExecContext(ctx, `UPDATE platform_skill_imports SET ready=true WHERE workspace_id=$1 AND id=$2`, t.model.workspace, id)
	return e
}
func (t *skillTx) MarkPublished(ctx context.Context, id, skill, version, fp string) error {
	_, e := t.model.tx.ExecContext(ctx, `UPDATE platform_skill_imports SET published_skill_id=$3,published_version_id=$4,publish_fingerprint=$5 WHERE workspace_id=$1 AND id=$2`, t.model.workspace, id, skill, version, fp)
	return e
}
func (t *skillTx) Save(ctx context.Context, s service.Skill, v service.SkillVersion, create bool) error {
	var e error
	if create {
		_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_skills(id,workspace_id,name,description,enabled,version,current_version_id) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7)`, s.ID, t.model.workspace, s.Name, s.Description, s.Enabled, s.Version, s.CurrentVersionID)
	} else {
		_, e = t.model.tx.ExecContext(ctx, `UPDATE platform_skills SET description=$3,version=$4,current_version_id=$5,updated_at=now() WHERE workspace_id IS NOT DISTINCT FROM NULLIF($1,'')::uuid AND id=$2`, t.model.workspace, s.ID, s.Description, s.Version, s.CurrentVersionID)
	}
	if e != nil {
		return e
	}
	m, e := json.Marshal(v.Manifest)
	if e != nil {
		return e
	}
	src, e := json.Marshal(v.Source)
	if e != nil {
		return e
	}
	_, e = t.model.tx.ExecContext(ctx, `INSERT INTO platform_skill_versions(id,workspace_id,skill_id,sequence,version_label,technical_name,description,manifest,source,storage_identity,key_prefix) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, v.ID, t.model.workspace, s.ID, v.Sequence, v.VersionLabel, v.TechnicalName, v.Description, m, src, v.StorageIdentity, v.KeyPrefix)
	return e
}
func (t *skillTx) Patch(ctx context.Context, v service.Skill) error {
	_, e := t.model.tx.ExecContext(ctx, `UPDATE platform_skills SET name=$3,enabled=$4,version=$5,updated_at=now() WHERE workspace_id IS NOT DISTINCT FROM NULLIF($1,'')::uuid AND id=$2`, t.model.workspace, v.ID, v.Name, v.Enabled, v.Version)
	return e
}

func (s *SkillStore) CleanupCandidates(ctx context.Context, identity string) ([]service.SkillImport, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,manifest,storage_identity,key_prefix FROM platform_skill_imports WHERE storage_identity=$1 AND NOT cleaned AND (expires_at < now() OR published_skill_id IS NOT NULL) ORDER BY expires_at LIMIT 100`, identity)
	if e != nil {
		return nil, e
	}
	defer rows.Close() //nolint:errcheck // rows.Err checked.
	out := []service.SkillImport{}
	for rows.Next() {
		var v service.SkillImport
		var raw []byte
		if e = rows.Scan(&v.ID, &raw, &v.StorageIdentity, &v.KeyPrefix); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v.Manifest); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *SkillStore) MarkCleaned(ctx context.Context, id string) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_skill_imports SET cleaned=true WHERE id=$1`, id)
	return e
}
