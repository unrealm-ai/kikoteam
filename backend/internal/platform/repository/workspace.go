package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type WorkspaceStore struct{ DB *sql.DB }

func NewWorkspaceStore(db *sql.DB) *WorkspaceStore { return &WorkspaceStore{DB: db} }
func workspaceDBError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrWorkspaceMissing
	}
	return err
}
func (s *WorkspaceStore) List(ctx context.Context, user string, page, size int) ([]service.Workspace, int, error) {
	items := []service.Workspace{}
	var total int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM platform_members WHERE user_id=$1 AND status='active'`, user).Scan(&total)
	if err != nil {
		return items, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT w.id,w.name,w.description,m.role,w.version FROM platform_workspaces w JOIN platform_members m ON m.workspace_id=w.id WHERE m.user_id=$1 AND m.status='active' ORDER BY w.created_at DESC,w.id DESC LIMIT $2 OFFSET $3`, user, size, (page-1)*size)
	if err != nil {
		return items, 0, err
	}
	defer rows.Close() //nolint:errcheck // Read-only cursor cleanup; iteration errors are checked below.
	for rows.Next() {
		var w service.Workspace
		if err = rows.Scan(&w.ID, &w.Name, &w.Description, &w.Role, &w.Version); err != nil {
			return items, 0, err
		}
		items = append(items, w)
	}
	return items, total, rows.Err()
}
func (s *WorkspaceStore) Create(ctx context.Context, user string, w service.Workspace) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort rollback after commit or original transaction error.
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_workspaces(id,name,description) VALUES($1,$2,$3)`, w.ID, w.Name, w.Description)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'owner')`, w.ID, user)
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	return tx.Commit()
}
func (s *WorkspaceStore) WithWorkspace(ctx context.Context, id string, fn func(service.WorkspaceTx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Best-effort rollback after commit or original transaction error.
	w := service.Workspace{}
	err = tx.QueryRowContext(ctx, `SELECT id,name,description,version FROM platform_workspaces WHERE id=$1 FOR UPDATE`, id).Scan(&w.ID, &w.Name, &w.Description, &w.Version)
	if err != nil {
		return workspaceDBError(err)
	}
	if err = fn(&workspaceTx{tx: tx, workspace: w}); err != nil {
		return err
	}
	return tx.Commit()
}

const invitationColumns = `i.id,i.workspace_id,i.inviter_id,COALESCE(i.email,''),i.role,i.status,i.expires_at,a.email,w.name,COALESCE(i.shared_token_hash,i.token_hash),i.shared_token_hash IS NOT NULL,i.kind,i.created_at,COALESCE((SELECT d.status FROM platform_invitation_deliveries d WHERE d.invitation_id=i.id),'not_applicable')`

type scanner interface{ Scan(...any) error }

func scanInvitation(row scanner) (service.Invitation, error) {
	var i service.Invitation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.InviterID, &i.Email, &i.Role, &i.Status, &i.ExpiresAt, &i.ContactEmail, &i.WorkspaceName, &i.TokenHash, &i.Shared, &i.Kind, &i.CreatedAt, &i.DeliveryStatus)
	return i, workspaceDBError(err)
}
func (s *WorkspaceStore) FindInvitation(ctx context.Context, hash string) (service.Invitation, error) {
	return scanInvitation(s.DB.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM platform_invitations i JOIN platform_accounts a ON a.id=i.inviter_id JOIN platform_workspaces w ON w.id=i.workspace_id WHERE (i.shared_token_hash IS NULL AND i.token_hash=$1) OR i.shared_token_hash=$1 ORDER BY (i.status='pending') DESC,(i.status='revoked') DESC,i.id LIMIT 1`, hash))
}
func (t *workspaceTx) AccountEmail(ctx context.Context, user string) (string, bool, error) {
	var email string
	var verified bool
	err := t.tx.QueryRowContext(ctx, `SELECT email,email_verified FROM platform_accounts WHERE id=$1`, user).Scan(&email, &verified)
	return email, verified, workspaceDBError(err)
}

type workspaceTx struct {
	tx        *sql.Tx
	workspace service.Workspace
}

func (t *workspaceTx) Workspace() service.Workspace { return t.workspace }
func (t *workspaceTx) Member(ctx context.Context, user string) (service.Member, error) {
	var m service.Member
	err := t.tx.QueryRowContext(ctx, `SELECT m.user_id,a.name,a.email,m.role,m.status,m.version,m.created_at FROM platform_members m JOIN platform_accounts a ON a.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2`, t.workspace.ID, user).Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.Status, &m.Version, &m.JoinedAt)
	return m, workspaceDBError(err)
}
func (t *workspaceTx) SaveWorkspace(ctx context.Context, w service.Workspace) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE platform_workspaces SET name=$2,description=$3,version=$4 WHERE id=$1`, t.workspace.ID, w.Name, w.Description, w.Version)
	return err
}
func (t *workspaceTx) SaveMember(ctx context.Context, m service.Member) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role,status,version) VALUES($1,$2,$3,$4,$5) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=EXCLUDED.role,status=EXCLUDED.status,version=EXCLUDED.version`, t.workspace.ID, m.ID, m.Role, m.Status, m.Version)
	return err
}
func (t *workspaceTx) DeleteMember(ctx context.Context, id string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM platform_members WHERE workspace_id=$1 AND user_id=$2`, t.workspace.ID, id)
	return err
}
func (t *workspaceTx) Members(ctx context.Context, page, size int) ([]service.Member, int, error) {
	items := []service.Member{}
	var total int
	err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_members WHERE workspace_id=$1`, t.workspace.ID).Scan(&total)
	if err != nil {
		return items, 0, err
	}
	rows, err := t.tx.QueryContext(ctx, `SELECT m.user_id,a.name,a.email,m.role,m.status,m.version,m.created_at FROM platform_members m JOIN platform_accounts a ON a.id=m.user_id WHERE m.workspace_id=$1 ORDER BY m.created_at DESC,m.user_id DESC LIMIT $2 OFFSET $3`, t.workspace.ID, size, (page-1)*size)
	if err != nil {
		return items, 0, err
	}
	defer rows.Close() //nolint:errcheck // Read-only cursor cleanup; iteration errors are checked below.
	for rows.Next() {
		var m service.Member
		if err = rows.Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.Status, &m.Version, &m.JoinedAt); err != nil {
			return items, 0, err
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}
func (t *workspaceTx) Invitations(ctx context.Context, page, size int) ([]service.Invitation, int, error) {
	items := []service.Invitation{}
	var total int
	err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_invitations WHERE workspace_id=$1`, t.workspace.ID).Scan(&total)
	if err != nil {
		return items, 0, err
	}
	rows, err := t.tx.QueryContext(ctx, `SELECT `+invitationColumns+` FROM platform_invitations i JOIN platform_accounts a ON a.id=i.inviter_id JOIN platform_workspaces w ON w.id=i.workspace_id WHERE i.workspace_id=$1 ORDER BY i.created_at DESC,i.id DESC LIMIT $2 OFFSET $3`, t.workspace.ID, size, (page-1)*size)
	if err != nil {
		return items, 0, err
	}
	defer rows.Close() //nolint:errcheck // Read-only cursor cleanup; iteration errors are checked below.
	for rows.Next() {
		i, e := scanInvitation(rows)
		if e != nil {
			return items, 0, e
		}
		items = append(items, i)
	}
	return items, total, rows.Err()
}
func (t *workspaceTx) Invitation(ctx context.Context, id string) (service.Invitation, error) {
	return scanInvitation(t.tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM platform_invitations i JOIN platform_accounts a ON a.id=i.inviter_id JOIN platform_workspaces w ON w.id=i.workspace_id WHERE i.workspace_id=$1 AND i.id=$2`, t.workspace.ID, id))
}

// A shared link addresses a group; a verified email addresses exactly one invitation.
// Keep the existing non-null unique token digest for each row, including during rolling upgrades.
func recipientDigest(linkHash, email string) string {
	sum := sha256.Sum256([]byte("shared-invitation:" + linkHash + ":" + email))
	return hex.EncodeToString(sum[:])
}
func (t *workspaceTx) InsertInvitation(ctx context.Context, i service.Invitation) error {
	digest := i.TokenHash
	var sharedHash *string
	if i.Shared {
		digest = recipientDigest(i.TokenHash, i.Email)
		sharedHash = &i.TokenHash
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_invitations
        (id,workspace_id,inviter_id,email,role,token_hash,expires_at,kind,shared_token_hash,created_at)
        VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,clock_timestamp())`,
		i.ID, t.workspace.ID, i.InviterID, i.Email, i.Role, digest, i.ExpiresAt, i.Kind, sharedHash)
	return err
}
func (t *workspaceTx) SetInvitationStatus(ctx context.Context, id, status string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE platform_invitations SET status=$3 WHERE workspace_id=$1 AND id=$2`, t.workspace.ID, id, status)
	return err
}

func (t *workspaceTx) Redeemed(ctx context.Context, invitation, account string) (bool, error) {
	var exists bool
	err := t.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_invitation_redemptions WHERE invitation_id=$1 AND account_id=$2)`, invitation, account).Scan(&exists)
	return exists, err
}
func (t *workspaceTx) RecordRedemption(ctx context.Context, invitation, account string) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_invitation_redemptions(invitation_id,account_id)VALUES($1,$2) ON CONFLICT DO NOTHING`, invitation, account)
	return err
}
func (t *workspaceTx) RemovedMember(ctx context.Context, account string) (time.Time, error) {
	var at time.Time
	err := t.tx.QueryRowContext(ctx, `SELECT removed_at FROM platform_removed_members WHERE workspace_id=$1 AND account_id=$2`, t.workspace.ID, account).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return at, err
}
func (t *workspaceTx) RecordRemoval(ctx context.Context, account string, _ time.Time) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO platform_removed_members(workspace_id,account_id,removed_at)VALUES($1,$2,clock_timestamp()) ON CONFLICT(workspace_id,account_id) DO UPDATE SET removed_at=EXCLUDED.removed_at`, t.workspace.ID, account)
	return err
}

func (s *WorkspaceStore) FindInvitationByID(ctx context.Context, id string) (service.Invitation, error) {
	return scanInvitation(s.DB.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM platform_invitations i JOIN platform_accounts a ON a.id=i.inviter_id JOIN platform_workspaces w ON w.id=i.workspace_id WHERE i.id=$1`, id))
}

func (t *workspaceTx) SharedRecipient(ctx context.Context, hash, email string) (service.Invitation, error) {
	return scanInvitation(t.tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM platform_invitations i JOIN platform_accounts a ON a.id=i.inviter_id JOIN platform_workspaces w ON w.id=i.workspace_id WHERE i.workspace_id=$1 AND i.shared_token_hash=$2 AND i.token_hash=$3`, t.workspace.ID, hash, recipientDigest(hash, email)))
}
func (t *workspaceTx) RevokeShared(ctx context.Context, hash string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE platform_invitations SET status='revoked' WHERE workspace_id=$1 AND shared_token_hash=$2 AND status='pending'`, t.workspace.ID, hash)
	return err
}
