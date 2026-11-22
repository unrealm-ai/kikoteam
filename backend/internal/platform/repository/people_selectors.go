package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type PeopleSelectorStore struct {
	DB     *sql.DB
	Secret []byte
}

func NewPeopleSelectorStore(db *sql.DB, secret []byte) *PeopleSelectorStore {
	return &PeopleSelectorStore{DB: db, Secret: secret}
}
func peopleAuthorize(ctx context.Context, tx *sql.Tx, w, actor, a string) error {
	var ok bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members m JOIN platform_agents a ON a.workspace_id=m.workspace_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.status='active' AND m.role IN ('owner','admin') AND a.id=$3)`, w, actor, a).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return service.Fail("workspace_forbidden", 403, "没有员工配置权限")
	}
	return nil
}

type peopleCursor struct {
	Scope string `json:"scope"`
	After string `json:"after"`
}

func (s *PeopleSelectorStore) cursorScope(w, actor, a string, q service.PeopleQuery) string {
	q.Cursor = ""
	raw := []byte(strings.Join([]string{w, actor, a, q.Provider, q.Purpose, q.Kind, q.Q, q.ParentID, q.DepartmentID, strconv.Itoa(q.Limit), strconv.FormatInt(q.SnapshotVersion, 10)}, "\x00"))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *PeopleSelectorStore) encodeCursor(c peopleCursor) (string, error) {
	if len(s.Secret) < 32 {
		return "", errors.New("selector cursor signing unavailable")
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *PeopleSelectorStore) decodeCursor(raw, scope string) (string, error) {
	bad := service.Fail("invalid_selector_cursor", 400, "分页游标或查询范围无效")
	if raw == "" {
		return "", nil
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(s.Secret) < 32 {
		return "", bad
	}
	data, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return "", bad
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", bad
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write(data)
	var c peopleCursor
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &c) != nil || c.Scope != scope {
		return "", bad
	}
	return c.After, nil
}
func (s *PeopleSelectorStore) SearchWebPeople(ctx context.Context, w, actor, a string, q service.PeopleQuery) (service.PeoplePage, error) {
	out := service.PeoplePage{Items: []service.PeopleCandidate{}}
	scope := s.cursorScope(w, actor, a, q)
	after, e := s.decodeCursor(q.Cursor, scope)
	if e != nil {
		return out, e
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if e = peopleAuthorize(ctx, tx, w, actor, a); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT m.user_id,ac.name,m.status FROM platform_members m JOIN platform_accounts ac ON ac.id=m.user_id WHERE m.workspace_id=$1 AND m.status='active' AND ($2='' OR strpos(lower(ac.name),lower($2))>0 OR strpos(lower(ac.email),lower($2))>0) AND ($3='' OR m.user_id::text>$3) ORDER BY m.user_id LIMIT $4`, w, q.Q, after, q.Limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close() //nolint:errcheck // Iteration error checked below.
	for rows.Next() {
		var id, name, status string
		if e = rows.Scan(&id, &name, &status); e != nil {
			return out, e
		}
		out.Items = append(out.Items, service.PeopleCandidate{Ref: service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: w, AgentID: a, ExternalID: id}, DisplayName: name, Status: status, Selectable: true})
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if e = rows.Close(); e != nil {
		return out, e
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		c, err := s.encodeCursor(peopleCursor{Scope: scope, After: out.Items[len(out.Items)-1].Ref.ExternalID})
		if err != nil {
			return out, err
		}
		out.NextCursor = &c
	}
	return out, tx.Commit()
}
func (s *PeopleSelectorStore) ResolvePerson(ctx context.Context, w, actor, a string, r service.SelectionRef) (service.PeopleCandidate, error) {
	out := service.PeopleCandidate{Ref: r, DisplayName: r.ExternalID, Status: "unavailable", Selectable: false, DisabledReason: "person_unavailable"}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if e = peopleAuthorize(ctx, tx, w, actor, a); e != nil {
		return out, e
	}
	if r.Provider == "web" {
		if r.Kind != "user" || r.BindingID != "" || r.IdentityEpoch != 0 || r.Generation != 0 || r.TenantID != "" {
			return out, service.Fail("invalid_people_refs", 400, "Web身份引用无效")
		}
		e = tx.QueryRowContext(ctx, `SELECT ac.name,m.status FROM platform_members m JOIN platform_accounts ac ON ac.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id::text=$2`, w, r.ExternalID).Scan(&out.DisplayName, &out.Status)
	} else {
		e = s.resolveChannelPerson(ctx, tx, w, a, r, &out)
	}
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	} else if e == nil {
		out.Selectable = out.Status == "active" || r.Provider != "web" && out.Status == "unknown"
		if out.Selectable {
			out.DisabledReason = ""
		}
	}
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *PeopleSelectorStore) resolveChannelPerson(ctx context.Context, tx *sql.Tx, w, a string, r service.SelectionRef, out *service.PeopleCandidate) error {
	b, e := scanChannel(tx.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM platform_channel_bindings WHERE id::text=$1 AND workspace_id=$2 AND agent_id=$3 AND provider=$4`, r.BindingID, w, a, r.Provider))
	if e != nil {
		return e
	}
	oldWecomProof := r.Provider == "wecom" && r.Generation > 0 && r.Generation < b.Generation
	if !b.Enabled || !b.IdentityConfirmed || b.IdentityEpoch != r.IdentityEpoch || (b.Generation != r.Generation && !oldWecomProof) || b.TenantID != r.TenantID || service.PeopleIdentityNamespace(b, r.Kind) != r.Namespace {
		out.DisabledReason = "selector_source_changed"
		return sql.ErrNoRows
	}
	if r.Provider == "wecom" {
		if r.Kind != "user" {
			return service.Fail("selector_capability_unsupported", 400, "企微不支持部门选择")
		}
		if b.IdentityVerifiedGeneration != b.Generation {
			return sql.ErrNoRows
		}
		e = tx.QueryRowContext(ctx, `SELECT COALESCE(c.display_name,m.search_user_id),'unknown' FROM platform_wecom_identity_mappings m LEFT JOIN platform_wecom_contacts c ON c.binding_id=m.binding_id AND c.identity_epoch=m.identity_epoch AND c.connection_generation=m.connection_generation AND c.search_user_id=m.search_user_id WHERE m.binding_id=$1 AND m.identity_epoch=$2 AND m.connection_generation=$3 AND m.callback_user_id=$4
 AND ($3=$5 OR EXISTS(SELECT FROM platform_wecom_identity_mappings old WHERE old.binding_id=m.binding_id AND old.identity_epoch=m.identity_epoch
 AND old.connection_generation=$5 AND old.search_user_id=m.search_user_id AND old.callback_user_id=m.callback_user_id))`, b.ID, b.IdentityEpoch, b.Generation, r.ExternalID, r.Generation).Scan(&out.DisplayName, &out.Status)
		if e == nil {
			out.Ref.Generation = b.Generation
			out.VerificationStatus = "verified"
		}
		return e
	}
	if r.Provider != "feishu" && r.Provider != "dingtalk" {
		return service.Fail("invalid_selector_provider", 400, "渠道无效")
	}
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT d.full_snapshot AND d.expires_at>now() AND (`+directorySource+`) FROM platform_channel_directory_state d JOIN platform_channel_bindings b ON b.id=d.binding_id WHERE b.id=$1`, b.ID).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		out.DisabledReason = "directory_snapshot_unavailable"
		return sql.ErrNoRows
	}
	if r.Kind == "department" {
		return tx.QueryRowContext(ctx, `SELECT display_name,'active' FROM platform_channel_directory_departments WHERE binding_id=$1 AND department_id=$2`, b.ID, r.ExternalID).Scan(&out.DisplayName, &out.Status)
	}
	return tx.QueryRowContext(ctx, `SELECT display_name,status FROM platform_channel_directory_users WHERE binding_id=$1 AND user_id=$2`, b.ID, r.ExternalID).Scan(&out.DisplayName, &out.Status)
}
