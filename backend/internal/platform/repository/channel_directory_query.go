package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type directoryCursor struct {
	Version int64  `json:"v"`
	Offset  int    `json:"o"`
	Scope   string `json:"s"`
}

func directoryScope(b service.ChannelBinding, q service.DirectoryQuery, version int64) (string, error) {
	q.Cursor = ""
	q.SnapshotVersion = version
	data, err := json.Marshal(struct {
		Binding string
		Query   service.DirectoryQuery
	}{b.ID, q})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
func (s *ChannelStore) DirectoryPage(ctx context.Context, b service.ChannelBinding, q service.DirectoryQuery) (service.DirectoryPage, error) {
	out := service.DirectoryPage{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operation error; Commit owns success.
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT d.snapshot_version,d.full_snapshot AND d.expires_at>now() AND (`+directorySource+`) FROM platform_channel_directory_state d JOIN platform_channel_bindings b ON b.id=d.binding_id WHERE b.id=$1 AND b.workspace_id=$2`, b.ID, b.WorkspaceID).Scan(&out.SnapshotVersion, &valid)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !valid {
		return out, service.Fail("directory_snapshot_unavailable", 409, "通讯录快照尚未就绪或已过期，请同步")
	}
	if err != nil {
		return out, err
	}
	if q.SnapshotVersion != 0 && q.SnapshotVersion != out.SnapshotVersion {
		return out, service.Fail("directory_snapshot_changed", 409, "通讯录已更新，请重新查询")
	}
	scope, err := directoryScope(b, q, out.SnapshotVersion)
	if err != nil {
		return out, err
	}
	cursor := directoryCursor{Version: out.SnapshotVersion, Scope: scope}
	if q.Cursor != "" {
		data, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		var prior directoryCursor
		if e != nil || json.Unmarshal(data, &prior) != nil || prior.Version != cursor.Version || prior.Scope != cursor.Scope || prior.Offset < 0 || prior.Offset > 100000 {
			return out, service.Fail("directory_snapshot_changed", 409, "分页范围或快照已变化")
		}
		cursor.Offset = prior.Offset
	}
	count := 0
	if q.Kind == "users" {
		rows, e := tx.QueryContext(ctx, `SELECT user_id,display_name,status,department_ids,memberships_known,direct_manager_id,direct_manager_known,aliases FROM platform_channel_directory_users WHERE binding_id=$1 AND ($2='' OR strpos(lower(display_name),lower($2))>0) AND ($3='' OR department_ids ? $3) ORDER BY lower(display_name),user_id LIMIT $4 OFFSET $5`, b.ID, q.Q, q.DepartmentID, q.Limit+1, cursor.Offset)
		if e != nil {
			return out, e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration and explicit close errors are checked below.
		users := []service.DirectoryUser{}
		for rows.Next() {
			var u service.DirectoryUser
			var raw, aliases []byte
			if e = rows.Scan(&u.ID, &u.DisplayName, &u.Status, &raw, &u.MembershipsKnown, &u.DirectManagerID, &u.DirectManagerKnown, &aliases); e != nil {
				return out, errors.Join(e, rows.Close())
			}
			if e = json.Unmarshal(raw, &u.DepartmentIDs); e != nil {
				return out, errors.Join(e, rows.Close())
			}
			if e = json.Unmarshal(aliases, &u.Aliases); e != nil {
				return out, errors.Join(e, rows.Close())
			}
			users = append(users, u)
		}
		if e = rows.Err(); e != nil {
			return out, errors.Join(e, rows.Close())
		}
		if e = rows.Close(); e != nil {
			return out, e
		}
		count = len(users)
		if count > q.Limit {
			users = users[:q.Limit]
		}
		out.Items = users
	} else {
		rows, e := tx.QueryContext(ctx, `SELECT d.department_id,d.display_name,d.parent_id,d.leader_ids,d.leaders_known FROM platform_channel_directory_departments d WHERE d.binding_id=$1 AND ($2='' OR strpos(lower(d.display_name),lower($2))>0) AND (($3<>'' AND d.parent_id=$3) OR ($3='' AND ($2<>'' OR d.parent_id='' OR NOT EXISTS(SELECT 1 FROM platform_channel_directory_departments parent WHERE parent.binding_id=d.binding_id AND parent.department_id=d.parent_id)))) ORDER BY lower(d.display_name),d.department_id LIMIT $4 OFFSET $5`, b.ID, q.Q, q.ParentID, q.Limit+1, cursor.Offset)
		if e != nil {
			return out, e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration and explicit close errors are checked below.
		depts := []service.DirectoryDepartment{}
		for rows.Next() {
			var d service.DirectoryDepartment
			var leaders []byte
			if e = rows.Scan(&d.ID, &d.DisplayName, &d.ParentID, &leaders, &d.LeadersKnown); e != nil {
				return out, errors.Join(e, rows.Close())
			}
			if e = json.Unmarshal(leaders, &d.LeaderIDs); e != nil {
				return out, errors.Join(e, rows.Close())
			}
			depts = append(depts, d)
		}
		if e = rows.Err(); e != nil {
			return out, errors.Join(e, rows.Close())
		}
		if e = rows.Close(); e != nil {
			return out, e
		}
		count = len(depts)
		if count > q.Limit {
			depts = depts[:q.Limit]
		}
		out.Items = depts
	}
	if count > q.Limit {
		cursor.Offset += q.Limit
		data, e := json.Marshal(cursor)
		if e != nil {
			return out, e
		}
		next := base64.RawURLEncoding.EncodeToString(data)
		out.NextCursor = &next
	}
	return out, tx.Commit()
}
