package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// ModelStore serializes workspace configuration mutations with membership changes.
type ModelStore struct{ DB *sql.DB }

func NewModelStore(db *sql.DB) *ModelStore { return &ModelStore{DB: db} }
func (s *ModelStore) WithWorkspace(ctx context.Context, workspace string, fn func(service.ModelTx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Rollback after commit or original error is best effort.
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR UPDATE`, workspace).Scan(&id); err != nil {
		return workspaceDBError(err)
	}
	if err = fn(&modelTx{tx: tx, workspace: workspace}); err != nil {
		return err
	}
	return tx.Commit()
}

type modelTx struct {
	tx        *sql.Tx
	workspace string
}

func (t *modelTx) Member(ctx context.Context, user string) (service.Member, error) {
	var m service.Member
	err := t.tx.QueryRowContext(ctx, `SELECT m.user_id,a.name,a.email,m.role,m.status,m.version FROM platform_members m JOIN platform_accounts a ON a.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2`, t.workspace, user).Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.Status, &m.Version)
	return m, workspaceDBError(err)
}

const modelColumns = `id,name,description,protocol,base_url,model_id,version,has_key,vision_status,enabled,is_default,tested_at,credential,credential_revision,validated`

func scanModel(row scanner) (service.ModelProfile, error) {
	var p service.ModelProfile
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Protocol, &p.BaseURL, &p.ModelID, &p.Version, &p.HasKey, &p.VisionStatus, &p.Enabled, &p.IsDefault, &p.TestedAt, &p.Credential, &p.CredentialRevision, &p.Validated)
	return p, workspaceDBError(err)
}
func (t *modelTx) Get(ctx context.Context, id string) (service.ModelProfile, error) {
	return scanModel(t.tx.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM platform_model_profiles WHERE workspace_id=$1 AND id=$2`, t.workspace, id))
}
func (t *modelTx) List(ctx context.Context, page, size int) ([]service.ModelProfile, int, error) {
	items := []service.ModelProfile{}
	var total int
	if err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_model_profiles WHERE workspace_id=$1`, t.workspace).Scan(&total); err != nil {
		return items, 0, err
	}
	rows, err := t.tx.QueryContext(ctx, `SELECT `+modelColumns+` FROM platform_model_profiles WHERE workspace_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, t.workspace, size, (page-1)*size)
	if err != nil {
		return items, 0, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are checked below.
	for rows.Next() {
		p, e := scanModel(rows)
		if e != nil {
			return items, 0, e
		}
		items = append(items, p)
	}
	return items, total, rows.Err()
}
func (t *modelTx) Save(ctx context.Context, p service.ModelProfile, create bool) error {
	if p.Credential == nil {
		p.Credential = []byte{}
	}
	args := []any{p.ID, t.workspace, p.Name, p.Description, p.Protocol, p.BaseURL, p.ModelID, p.Version, p.HasKey, p.VisionStatus, p.Enabled, p.IsDefault, p.TestedAt, p.Credential, p.CredentialRevision, p.Validated}
	var err error
	if create {
		_, err = t.tx.ExecContext(ctx, `INSERT INTO platform_model_profiles(id,workspace_id,name,description,protocol,base_url,model_id,version,has_key,vision_status,enabled,is_default,tested_at,credential,credential_revision,validated) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, args...)
	} else {
		var result sql.Result
		result, err = t.tx.ExecContext(ctx, `UPDATE platform_model_profiles SET name=$3,description=$4,protocol=$5,base_url=$6,model_id=$7,version=$8,has_key=$9,vision_status=$10,enabled=$11,is_default=$12,tested_at=$13,credential=$14,credential_revision=$15,validated=$16 WHERE id=$1 AND workspace_id=$2 AND version=$8-1`, args...)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				return service.Fail("model_version_conflict", 409, "模型配置已更新，请刷新后重试")
			}
		}
	}
	if err != nil {
		return fmt.Errorf("save model profile: %w", err)
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO platform_model_profile_versions(workspace_id,profile_id,version,name,description,protocol,base_url,model_id,vision_status,enabled,tested_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, t.workspace, p.ID, p.Version, p.Name, p.Description, p.Protocol, p.BaseURL, p.ModelID, p.VisionStatus, p.Enabled, p.TestedAt)
	return err
}
func (t *modelTx) ClearDefault(ctx context.Context) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE platform_model_profiles SET is_default=false WHERE workspace_id=$1 AND is_default`, t.workspace)
	return err
}
func (t *modelTx) InsertReceipt(ctx context.Context, r service.ModelReceiptRecord) error {
	result, err := json.Marshal(r.Result)
	if err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(ctx, `DELETE FROM platform_model_test_receipts WHERE workspace_id=$1 AND expires_at<=now()`, t.workspace); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO platform_model_test_receipts(workspace_id,token_hash,actor_id,profile_id,profile_version,fingerprint,tested_at,expires_at,result) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9)`, t.workspace, r.Hash, r.Actor, r.ProfileID, r.Version, r.Fingerprint, r.TestedAt, r.ExpiresAt, result)
	return err
}
func (t *modelTx) Receipt(ctx context.Context, hash string) (service.ModelReceiptRecord, error) {
	var r service.ModelReceiptRecord
	var data []byte
	err := t.tx.QueryRowContext(ctx, `SELECT token_hash,actor_id,COALESCE(profile_id::text,''),profile_version,fingerprint,tested_at,expires_at,result,COALESCE(used_profile_id::text,'') FROM platform_model_test_receipts WHERE workspace_id=$1 AND token_hash=$2`, t.workspace, hash).Scan(&r.Hash, &r.Actor, &r.ProfileID, &r.Version, &r.Fingerprint, &r.TestedAt, &r.ExpiresAt, &data, &r.UsedProfileID)
	if err != nil {
		return r, workspaceDBError(err)
	}
	err = json.Unmarshal(data, &r.Result)
	return r, err
}
func (t *modelTx) ConsumeReceipt(ctx context.Context, hash, id string) error {
	result, err := t.tx.ExecContext(ctx, `UPDATE platform_model_test_receipts SET used_profile_id=$3 WHERE workspace_id=$1 AND token_hash=$2 AND used_profile_id IS NULL`, t.workspace, hash, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("model_test_consumed", 409, "测试回执已使用，请重新测试")
	}
	return err
}

func (s *ModelStore) AllowProbe(ctx context.Context, workspace, actor string) error {
	return (&AuthStore{DB: s.DB}).Allow(ctx, "model-probe:"+workspace+":"+actor, 10, time.Minute)
}
func (t *modelTx) ClaimProbe(ctx context.Context, actor, claim string, lease time.Duration) error {
	result, err := t.tx.ExecContext(ctx, `INSERT INTO platform_model_probe_leases(workspace_id,actor_id,claim_id,expires_at) VALUES($1,$2,$3,now()+($4*interval '1 second')) ON CONFLICT(workspace_id,actor_id) DO UPDATE SET claim_id=EXCLUDED.claim_id,expires_at=EXCLUDED.expires_at WHERE platform_model_probe_leases.expires_at<=now()`, t.workspace, actor, claim, lease.Seconds())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("model_probe_in_progress", 409, "模型测试正在进行，请等待完成后重试")
	}
	return err
}
func (t *modelTx) ReleaseProbe(ctx context.Context, actor, claim string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM platform_model_probe_leases WHERE workspace_id=$1 AND actor_id=$2 AND claim_id=$3`, t.workspace, actor, claim)
	return err
}
