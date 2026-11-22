package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const directoryEligible = `b.enabled AND (b.provider='dingtalk' OR (b.provider='feishu' AND b.external_tenant_id<>''))`
const directorySource = `d.identity_epoch=b.identity_epoch AND d.connection_generation=b.connection_generation AND d.external_tenant_id=b.external_tenant_id AND (` + directoryEligible + `)`

func (s *ChannelStore) DirectoryStatus(ctx context.Context, b service.ChannelBinding) (service.DirectoryStatus, error) {
	var v service.DirectoryStatus
	err := s.DB.QueryRowContext(ctx, `SELECT CASE WHEN NOT (`+directorySource+`) THEN 'invalidated' WHEN d.lease_until>now() OR d.sync_requested THEN 'syncing' WHEN d.lease_until IS NOT NULL THEN 'failed' WHEN d.full_snapshot AND d.expires_at<=now() THEN 'invalidated' ELSE d.last_result END,d.snapshot_version,d.full_snapshot AND d.expires_at>now() AND (`+directorySource+`),d.synced_at,d.expires_at,CASE WHEN d.lease_until<=now() THEN 'directory_sync_interrupted' ELSE d.error_code END,d.user_count,d.department_count FROM platform_channel_directory_state d JOIN platform_channel_bindings b ON b.id=d.binding_id WHERE b.id=$1 AND b.workspace_id=$2`, b.ID, b.WorkspaceID).Scan(&v.Status, &v.SnapshotVersion, &v.Complete, &v.SyncedAt, &v.ExpiresAt, &v.ErrorCode, &v.UserCount, &v.DepartmentCount)
	if errors.Is(err, sql.ErrNoRows) {
		return service.DirectoryStatus{Status: "never"}, nil
	}
	return v, err
}
func (s *ChannelStore) RequestDirectorySync(ctx context.Context, b service.ChannelBinding) (service.DirectoryStatus, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO platform_channel_directory_state AS d(binding_id,identity_epoch,connection_generation,external_tenant_id)
		SELECT b.id,b.identity_epoch,b.connection_generation,b.external_tenant_id FROM platform_channel_bindings b
		WHERE b.id=$1 AND b.workspace_id=$2 AND b.identity_epoch=$3 AND b.connection_generation=$4
		AND (b.provider='dingtalk' OR b.external_tenant_id=$5) AND (`+directoryEligible+`) FOR SHARE OF b
		ON CONFLICT(binding_id) DO UPDATE SET next_sync_at=now(),sync_requested=CASE WHEN d.lease_until>now() THEN d.sync_requested ELSE true END`, b.ID, b.WorkspaceID, b.IdentityEpoch, b.Generation, b.TenantID)
	if err != nil {
		return service.DirectoryStatus{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return service.DirectoryStatus{}, err
	}
	if n == 0 {
		return service.DirectoryStatus{}, service.Fail("directory_source_changed", 409, "渠道来源已变化")
	}
	return s.DirectoryStatus(ctx, b)
}

// Keep a confirmed scope change queued if an older directory fetch is in flight.
func (s *ChannelStore) QueueDirectoryRecheck(ctx context.Context, b service.ChannelBinding) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE platform_channel_directory_state d SET next_sync_at=now(),sync_requested=true
		FROM platform_channel_bindings b WHERE b.id=$1 AND b.connection_generation=$2 AND d.binding_id=b.id
		AND (`+directorySource+`)`, b.ID, b.Generation)
	return err
}

func (s *ChannelStore) ClaimDirectorySync(ctx context.Context) (*service.DirectoryClaim, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operation error; Commit owns success.
	// Lock the binding before its directory row, as first inbound identity confirmation does.
	var id string
	err = tx.QueryRowContext(ctx, `SELECT b.id FROM platform_channel_bindings b LEFT JOIN platform_channel_directory_state d ON d.binding_id=b.id
		WHERE (`+directoryEligible+`) AND (d.lease_until IS NULL OR d.lease_until<now())
		AND (d.binding_id IS NULL OR d.next_sync_at<=now() OR d.sync_requested OR NOT (`+directorySource+`))
		ORDER BY COALESCE(d.next_sync_at,now()),b.id LIMIT 1 FOR NO KEY UPDATE OF b SKIP LOCKED`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	b, err := scanChannel(tx.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM platform_channel_bindings WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	claim := &service.DirectoryClaim{Binding: b, Token: service.NewID()}
	const sameSource = `(d.identity_epoch,d.connection_generation,d.external_tenant_id)=($2,$3,$4)`
	// Recheck the lease and schedule after locking; another worker may have finished claiming since the candidate scan.
	err = tx.QueryRowContext(ctx, `INSERT INTO platform_channel_directory_state AS d
		(binding_id,identity_epoch,connection_generation,external_tenant_id,lease_token,lease_until,sync_requested)
		VALUES($1,$2,$3,$4,$5,now()+interval '2 minutes',false)
		ON CONFLICT(binding_id) DO UPDATE SET full_snapshot=d.full_snapshot AND (`+sameSource+`),
		snapshot_version=d.snapshot_version+CASE WHEN `+sameSource+` THEN 0 ELSE 1 END,
		identity_epoch=$2,connection_generation=$3,external_tenant_id=$4,lease_token=$5,
		lease_until=EXCLUDED.lease_until,sync_requested=false,error_code=''
		WHERE (d.lease_until IS NULL OR d.lease_until<now()) AND (d.next_sync_at<=now() OR d.sync_requested OR NOT (`+sameSource+`))
		RETURNING binding_id`, id, b.IdentityEpoch, b.Generation, b.TenantID, claim.Token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	return claim, tx.Commit()
}
func lockDirectoryClaim(ctx context.Context, tx *sql.Tx, c service.DirectoryClaim) error {
	var tenant string
	err := tx.QueryRowContext(ctx, `SELECT b.external_tenant_id FROM platform_channel_bindings b
		WHERE b.id=$1 AND b.identity_epoch=$2 AND b.connection_generation=$3 AND (`+directoryEligible+`)
		AND (b.external_tenant_id=$4 OR (b.provider='dingtalk' AND $4='')) FOR SHARE OF b`,
		c.Binding.ID, c.Binding.IdentityEpoch, c.Binding.Generation, c.Binding.TenantID).Scan(&tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("directory_source_changed", 409, "通讯录来源或同步租约已变化")
	}
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(d.lease_token=$2 AND d.lease_until>now(),false) AND d.identity_epoch=$3 AND d.connection_generation=$4 AND d.external_tenant_id=$5
		FROM platform_channel_directory_state d WHERE d.binding_id=$1 FOR UPDATE OF d`, c.Binding.ID, c.Token, c.Binding.IdentityEpoch, c.Binding.Generation, tenant).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return service.Fail("directory_source_changed", 409, "通讯录来源或同步租约已变化")
	}
	return nil
}
func (s *ChannelStore) PublishDirectory(ctx context.Context, c service.DirectoryClaim, v service.DirectorySnapshot) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operation error; Commit owns success.
	if err = lockDirectoryClaim(ctx, tx, c); err != nil {
		return err
	}
	for _, table := range []string{"platform_channel_directory_users", "platform_channel_directory_departments"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE binding_id=$1`, c.Binding.ID); err != nil {
			return err
		}
	}
	for _, u := range v.Users {
		if u.DepartmentIDs == nil {
			u.DepartmentIDs = []string{}
		}
		data, e := json.Marshal(u.DepartmentIDs)
		if e != nil {
			return e
		}
		if u.Aliases == nil {
			u.Aliases = []service.DirectoryAlias{}
		}
		aliases, e := json.Marshal(u.Aliases)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known,direct_manager_id,direct_manager_known,aliases) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, c.Binding.ID, u.ID, u.DisplayName, u.Status, data, u.MembershipsKnown, u.DirectManagerID, u.DirectManagerKnown, aliases); err != nil {
			return err
		}
	}
	for _, d := range v.Departments {
		if d.LeaderIDs == nil {
			d.LeaderIDs = []string{}
		}
		leaders, e := json.Marshal(d.LeaderIDs)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO platform_channel_directory_departments(binding_id,department_id,display_name,parent_id,leader_ids,leaders_known) VALUES($1,$2,$3,$4,$5,$6)`, c.Binding.ID, d.ID, d.DisplayName, d.ParentID, leaders, d.LeadersKnown); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_channel_directory_state SET snapshot_version=snapshot_version+1,full_snapshot=true,last_result='ready',synced_at=now(),expires_at=now()+interval '24 hours',next_sync_at=now()+interval '15 minutes',lease_token=NULL,lease_until=NULL,error_code='',user_count=$2,department_count=$3 WHERE binding_id=$1`, c.Binding.ID, len(v.Users), len(v.Departments))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *ChannelStore) FailDirectory(ctx context.Context, c service.DirectoryClaim, code string, invalidate bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operation error; Commit owns success.
	if err = lockDirectoryClaim(ctx, tx, c); err != nil {
		return err
	}
	if invalidate {
		for _, table := range []string{"platform_channel_directory_users", "platform_channel_directory_departments"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE binding_id=$1`, c.Binding.ID); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_channel_directory_state SET full_snapshot=full_snapshot AND NOT $3,snapshot_version=snapshot_version+CASE WHEN $3 AND full_snapshot THEN 1 ELSE 0 END,last_result=CASE WHEN $3 THEN 'invalidated' ELSE 'failed' END,error_code=$2,next_sync_at=now()+interval '1 minute',lease_token=NULL,lease_until=NULL,user_count=CASE WHEN $3 THEN 0 ELSE user_count END,department_count=CASE WHEN $3 THEN 0 ELSE department_count END WHERE binding_id=$1`, c.Binding.ID, code, invalidate)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (t *channelTx) ValidateDirectorySelections(ctx context.Context, b service.ChannelBinding, previous service.ChannelAccess) error {
	old := map[service.ChannelDepartmentRule]bool{}
	if err := service.NormalizeDepartmentRules(&previous); err != nil {
		return err
	}
	for _, rule := range append(append([]service.ChannelDepartmentRule{}, previous.AllowDepartmentRules...), previous.DenyDepartmentRules...) {
		old[rule] = true
		old[service.ChannelDepartmentRule{DepartmentID: rule.DepartmentID}] = true
	}
	for _, rule := range append(append([]service.ChannelDepartmentRule{}, b.Access.AllowDepartmentRules...), b.Access.DenyDepartmentRules...) {
		id := rule.DepartmentID
		if old[rule] {
			continue
		}
		var exists bool
		err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_channel_directory_departments x JOIN platform_channel_directory_state d ON d.binding_id=x.binding_id WHERE x.binding_id=$1 AND x.department_id=$2 AND d.identity_epoch=$3 AND d.connection_generation=$4 AND d.external_tenant_id=$5 AND d.full_snapshot AND d.expires_at>now())`, b.ID, id, b.IdentityEpoch, b.Generation, b.TenantID).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return service.Fail("directory_selection_unavailable", 409, "新增部门必须来自当前有效通讯录快照")
		}
	}
	return nil
}
