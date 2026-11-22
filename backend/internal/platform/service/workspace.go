package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// WorkspaceError carries business failure semantics independently of HTTP handlers.
type WorkspaceError struct {
	Code    string
	Status  int
	Message string
}

func (e *WorkspaceError) Error() string { return e.Message }
func workspaceFailure(code string, status int, message string) error {
	return &WorkspaceError{code, status, message}
}

var ErrWorkspaceMissing = errors.New("workspace record missing")

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Role        string `json:"role"`
	Version     int64  `json:"version"`
}
type Member struct {
	JoinedAt time.Time `json:"joined_at"`
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	Status   string    `json:"status"`
	Version  int64     `json:"version"`
}
type Invitation struct {
	Shared         bool      `json:"shared,omitempty"`
	DeliveryStatus string    `json:"delivery_status"`
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"-"`
	InviterID      string    `json:"-"`
	Email          string    `json:"email,omitempty"`
	Kind           string    `json:"kind"`
	CreatedAt      time.Time `json:"-"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	ContactEmail   string    `json:"contact_email"`
	Link           string    `json:"link,omitempty"`
	TokenHash      string    `json:"-"`
	WorkspaceName  string    `json:"-"`
}
type InvitationInspection struct {
	Status        string `json:"status"`
	Kind          string `json:"kind,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	ContactEmail  string `json:"contact_email,omitempty"`
	EmailMasked   string `json:"email_masked,omitempty"`
	Role          string `json:"role,omitempty"`
}
type WorkspacePage struct {
	Items    []Workspace `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}
type MemberPage struct {
	Items    []Member `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}
type InvitationPage struct {
	Items    []Invitation `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}
type WorkspacePatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Version     int64   `json:"version"`
}
type MemberPatch struct {
	Role    *string `json:"role"`
	Status  *string `json:"status"`
	Version int64   `json:"version"`
}

// WorkspaceTx is scoped to one locked workspace. Service owns authorization and mutations.
type WorkspaceTx interface {
	AccountEmail(context.Context, string) (string, bool, error)
	Workspace() Workspace
	Member(context.Context, string) (Member, error)
	SaveWorkspace(context.Context, Workspace) error
	SaveMember(context.Context, Member) error
	DeleteMember(context.Context, string) error
	Members(context.Context, int, int) ([]Member, int, error)
	Invitations(context.Context, int, int) ([]Invitation, int, error)
	Invitation(context.Context, string) (Invitation, error)
	SharedRecipient(context.Context, string, string) (Invitation, error)
	RevokeShared(context.Context, string) error
	InsertInvitation(context.Context, Invitation) error
	SetInvitationStatus(context.Context, string, string) error
	EnqueueDelivery(context.Context, InvitationDelivery) error
	RetryDelivery(context.Context, string, bool) (string, error)
	Redeemed(context.Context, string, string) (bool, error)
	RecordRedemption(context.Context, string, string) error
	RemovedMember(context.Context, string) (time.Time, error)
	RecordRemoval(context.Context, string, time.Time) error
}
type WorkspaceStore interface {
	Allow(context.Context, string, int, time.Duration) error
	List(context.Context, string, int, int) ([]Workspace, int, error)
	Create(context.Context, string, Workspace) error
	WithWorkspace(context.Context, string, func(WorkspaceTx) error) error
	FindInvitation(context.Context, string) (Invitation, error)
	FindInvitationByID(context.Context, string) (Invitation, error)
}
type WorkspaceService struct {
	EmailInvitationsEnabled bool
	ReadMailPolicy          MailPolicyReader
	MailSecret              []byte
	Store                   WorkspaceStore
	Now                     func() time.Time
	PublicURL               string
}

func (s *WorkspaceService) invitationMailEnabled(ctx context.Context) (bool, error) {
	if s.ReadMailPolicy == nil {
		return s.EmailInvitationsEnabled, nil
	}
	policy, err := s.ReadMailPolicy(ctx)
	return policy.Enabled, err
}

func (s *WorkspaceService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func workspaceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func invitationHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func pageValid(page, size int) error {
	if page < 1 || size < 1 || size > 100 || page > 1000000 {
		return workspaceFailure("invalid_pagination", 400, "分页参数无效")
	}
	return nil
}
func workspaceAuthorized(ctx context.Context, tx WorkspaceTx, actor string, admin bool) (Member, error) {
	m, err := tx.Member(ctx, actor)
	if errors.Is(err, ErrWorkspaceMissing) || err == nil && m.Status != "active" {
		return Member{}, workspaceFailure("workspace_forbidden", 403, "没有工作区权限")
	}
	if err != nil {
		return m, err
	}
	if admin && m.Role != "owner" && m.Role != "admin" {
		return m, workspaceFailure("workspace_forbidden", 403, "需要工作区管理员权限")
	}
	return m, nil
}
func (s *WorkspaceService) List(ctx context.Context, actor string, page, size int) (WorkspacePage, error) {
	out := WorkspacePage{Items: []Workspace{}, Page: page, PageSize: size}
	if err := pageValid(page, size); err != nil {
		return out, err
	}
	items, total, err := s.Store.List(ctx, actor, page, size)
	if items != nil {
		out.Items = items
	}
	out.Total = total
	return out, err
}
func (s *WorkspaceService) Create(ctx context.Context, actor, name, description string) (Workspace, error) {
	w := Workspace{ID: workspaceID(), Name: strings.TrimSpace(name), Description: description, Role: "owner", Version: 1}
	if err := validateWorkspace(w); err != nil {
		return w, err
	}
	return w, s.Store.Create(ctx, actor, w)
}
func validateWorkspace(w Workspace) error {
	if len([]rune(w.Name)) < 1 || len([]rune(w.Name)) > 100 || len([]rune(w.Description)) > 2000 {
		return workspaceFailure("invalid_workspace", 400, "工作区名称需为 1–100 字，描述不超过 2000 字")
	}
	return nil
}
func (s *WorkspaceService) Get(ctx context.Context, actor, id string) (Workspace, error) {
	var w Workspace
	err := s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		m, err := workspaceAuthorized(ctx, tx, actor, false)
		if err != nil {
			return err
		}
		w = tx.Workspace()
		w.Role = m.Role
		return nil
	})
	return w, err
}
func (s *WorkspaceService) Update(ctx context.Context, actor, id string, p WorkspacePatch) (Workspace, error) {
	var w Workspace
	err := s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		m, err := workspaceAuthorized(ctx, tx, actor, true)
		if err != nil {
			return err
		}
		w = tx.Workspace()
		if p.Version != w.Version {
			return workspaceFailure("version_conflict", 409, "配置已更新，请刷新后重试")
		}
		if p.Name != nil {
			w.Name = strings.TrimSpace(*p.Name)
		}
		if p.Description != nil {
			w.Description = *p.Description
		}
		if err = validateWorkspace(w); err != nil {
			return err
		}
		w.Version++
		w.Role = m.Role
		return tx.SaveWorkspace(ctx, w)
	})
	return w, err
}
func (s *WorkspaceService) Members(ctx context.Context, actor, id string, page, size int) (MemberPage, error) {
	out := MemberPage{Items: []Member{}, Page: page, PageSize: size}
	if err := pageValid(page, size); err != nil {
		return out, err
	}
	err := s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		if _, err := workspaceAuthorized(ctx, tx, actor, false); err != nil {
			return err
		}
		items, total, err := tx.Members(ctx, page, size)
		if items != nil {
			out.Items = items
		}
		out.Total = total
		return err
	})
	return out, err
}
func (s *WorkspaceService) UpdateMember(ctx context.Context, actor, id, target string, p MemberPatch) (Member, error) {
	var m Member
	err := s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		a, err := workspaceAuthorized(ctx, tx, actor, true)
		if err != nil {
			return err
		}
		m, err = tx.Member(ctx, target)
		if err != nil {
			return err
		}
		if err = validateMemberChange(a, m, p); err != nil {
			return err
		}
		if p.Role != nil {
			m.Role = *p.Role
		}
		if p.Status != nil {
			m.Status = *p.Status
		}
		m.Version++
		return tx.SaveMember(ctx, m)
	})
	return m, err
}
func validateMemberChange(actor, target Member, p MemberPatch) error {
	if target.Version != p.Version {
		return workspaceFailure("version_conflict", 409, "成员已更新，请刷新后重试")
	}
	if actor.ID == target.ID || target.Role == "owner" {
		return workspaceFailure("member_protected", 403, "不能修改自己或工作区所有者")
	}
	if actor.Role != "owner" && target.Role == "admin" {
		return workspaceFailure("member_protected", 403, "只有所有者可以修改管理员")
	}
	if p.Role != nil && (*p.Role != "admin" && *p.Role != "member" || actor.Role != "owner" && *p.Role != "member") {
		return workspaceFailure("invalid_role", 403, "不能授予该角色")
	}
	if p.Status != nil && *p.Status != "active" && *p.Status != "disabled" {
		return workspaceFailure("invalid_status", 400, "成员状态无效")
	}
	return nil
}
func (s *WorkspaceService) RemoveMember(ctx context.Context, actor, id, target string, version int64) error {
	return s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		a, err := workspaceAuthorized(ctx, tx, actor, true)
		if err != nil {
			return err
		}
		m, err := tx.Member(ctx, target)
		if err != nil {
			return err
		}
		if err = validateMemberChange(a, m, MemberPatch{Version: version}); err != nil {
			return err
		}
		if err = tx.RecordRemoval(ctx, target, s.now()); err != nil {
			return err
		}
		return tx.DeleteMember(ctx, target)
	})
}
func (s *WorkspaceService) Invitations(ctx context.Context, actor, id string, page, size int) (InvitationPage, error) {
	out := InvitationPage{Items: []Invitation{}, Page: page, PageSize: size}
	if err := pageValid(page, size); err != nil {
		return out, err
	}
	err := s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		if _, err := workspaceAuthorized(ctx, tx, actor, true); err != nil {
			return err
		}
		items, total, err := tx.Invitations(ctx, page, size)
		for i := range items {
			if items[i].Status == "pending" && !items[i].ExpiresAt.After(s.now()) {
				items[i].Status = "expired"
			}
		}
		if items != nil {
			out.Items = items
		}
		out.Total = total
		return err
	})
	return out, err
}
func (s *WorkspaceService) CreateInvitation(ctx context.Context, actor, id, email, role string) (Invitation, error) {
	return s.CreateInvitationWithOptions(ctx, actor, id, email, role, "email", 0)
}

func invitationOptions(email, role, kind string, days int) (string, string, int, error) {
	if kind == "" {
		kind = "email"
	}
	if kind != "email" && kind != "link" {
		return "", "", 0, workspaceFailure("invalid_invitation_kind", 400, "邀请类型无效")
	}
	if days == 0 {
		days = 1
		if kind == "link" {
			days = 7
		}
	}
	if days < 1 || days > 180 {
		return "", "", 0, workspaceFailure("invalid_expiry", 400, "邀请有效期需为 1 至 180 天")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if kind == "link" {
		if email != "" || role != "member" {
			return "", "", 0, workspaceFailure("invalid_link_invitation", 400, "通用链接只能邀请普通成员，且不能指定邮箱")
		}
		return email, kind, days, nil
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 {
		return "", "", 0, workspaceFailure("invalid_email", 400, "邮箱格式无效")
	}
	return email, kind, days, nil
}
func (s *WorkspaceService) CreateInvitationWithOptions(ctx context.Context, actor, id, email, role, kind string, expiresInDays int) (Invitation, error) {
	return s.CreateInvite(ctx, actor, id, InvitationOptions{Email: email, Role: role, Kind: kind, ExpiresInDays: expiresInDays})
}

type InvitationOptions struct {
	Email         string
	Emails        []string
	Role          string
	Kind          string
	ExpiresInDays int
}

func (q InvitationOptions) recipients() ([]string, string, int, error) {
	addresses := q.Emails
	if addresses == nil {
		addresses = []string{q.Email}
	} else if q.Email != "" || len(addresses) == 0 || q.Kind == "link" {
		return nil, "", 0, workspaceFailure("invalid_email", 400, "请使用一种方式指定受邀邮箱")
	}
	recipients := make([]string, 0, len(addresses))
	seen := make(map[string]bool)
	var kind string
	var days int
	for _, raw := range addresses {
		email, k, d, err := invitationOptions(raw, q.Role, q.Kind, q.ExpiresInDays)
		if err != nil {
			return nil, "", 0, err
		}
		kind, days = k, d
		if !seen[email] {
			recipients = append(recipients, email)
			seen[email] = true
		}
	}
	return recipients, kind, days, nil
}

func (s *WorkspaceService) CreateInvite(ctx context.Context, actor, id string, q InvitationOptions) (Invitation, error) {
	if q.Role == "" {
		q.Role = "member"
	}
	recipients, kind, days, err := q.recipients()
	if err != nil {
		return Invitation{}, err
	}
	if kind == "email" {
		enabled, err := s.invitationMailEnabled(ctx)
		if err != nil {
			return Invitation{}, err
		}
		if !enabled || len(s.MailSecret) < 32 {
			return Invitation{}, workspaceFailure("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用通用邀请链接")
		}
		current, err := s.Get(ctx, actor, id)
		if err != nil {
			return Invitation{}, err
		}
		if err = canInvite(current.Role, q.Role); err != nil {
			return Invitation{}, err
		}
		for _, recipient := range recipients {
			if err = s.limitInvitationMail(ctx, actor, recipient); err != nil {
				return Invitation{}, err
			}
		}
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return Invitation{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	link := strings.TrimRight(s.PublicURL, "/") + "/?invitation_token=" + token
	now := s.now()
	invite := Invitation{WorkspaceID: id, InviterID: actor, Role: q.Role, Kind: kind, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour), TokenHash: invitationHash(token), Shared: len(recipients) > 1, DeliveryStatus: "not_applicable"}
	err = s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		a, e := workspaceAuthorized(ctx, tx, actor, true)
		if e != nil {
			return e
		}
		if e = canInvite(a.Role, q.Role); e != nil {
			return e
		}
		invite.ContactEmail = a.Email
		for _, email := range recipients {
			invite.ID, invite.Email = workspaceID(), email
			if e = s.insertRecipient(ctx, tx, invite, link); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return Invitation{}, err
	}
	if kind == "email" {
		invite.DeliveryStatus = "pending"
	}
	invite.Link = link
	return invite, nil
}

func canInvite(actorRole, role string) error {
	if actorRole != "admin" && actorRole != "owner" {
		return workspaceFailure("workspace_forbidden", 403, "需要工作区管理员权限")
	}
	if role != "member" && (role != "admin" || actorRole != "owner") {
		return workspaceFailure("invalid_role", 403, "不能邀请该角色")
	}
	return nil
}

func (s *WorkspaceService) insertRecipient(ctx context.Context, tx WorkspaceTx, invite Invitation, link string) error {
	if err := tx.InsertInvitation(ctx, invite); err != nil {
		return err
	}
	if invite.Kind == "link" {
		return nil
	}
	ciphertext, err := encryptInvitation(s.MailSecret, invite.ID, invite.Email, link)
	if err != nil {
		return err
	}
	return tx.EnqueueDelivery(ctx, InvitationDelivery{InvitationID: invite.ID, Ciphertext: ciphertext, Status: "pending"})
}
func (s *WorkspaceService) RevokeInvitation(ctx context.Context, actor, id, inviteID string) error {
	return s.Store.WithWorkspace(ctx, id, func(tx WorkspaceTx) error {
		a, err := workspaceAuthorized(ctx, tx, actor, true)
		if err != nil {
			return err
		}
		i, err := tx.Invitation(ctx, inviteID)
		if err != nil {
			return err
		}
		if a.Role != "owner" && i.Role == "admin" {
			return workspaceFailure("invitation_forbidden", 403, "不能撤销管理员邀请")
		}
		if i.Shared {
			return tx.RevokeShared(ctx, i.TokenHash)
		}
		if i.Status == "accepted" {
			return workspaceFailure("invitation_accepted", 409, "邀请已经使用")
		}
		return tx.SetInvitationStatus(ctx, i.ID, "revoked")
	})
}
func invitationStatus(i Invitation, now time.Time) string {
	if i.Status != "pending" {
		return i.Status
	}
	if !i.ExpiresAt.After(now) {
		return "expired"
	}
	return "valid"
}
func (s *WorkspaceService) InspectInvitation(ctx context.Context, token string) (InvitationInspection, error) {
	out := InvitationInspection{Status: "unknown"}
	if len(token) != 64 {
		return out, nil
	}
	i, err := s.Store.FindInvitation(ctx, invitationHash(token))
	if errors.Is(err, ErrWorkspaceMissing) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Status = invitationStatus(i, s.now())
	out.Kind = i.Kind
	out.ContactEmail = i.ContactEmail
	out.WorkspaceName = i.WorkspaceName
	out.Role = i.Role
	if p := strings.LastIndexByte(i.Email, '@'); !i.Shared && p > 0 {
		out.EmailMasked = i.Email[:1] + "***" + i.Email[p:]
	}
	if out.Status == "valid" {
		err = s.Store.WithWorkspace(ctx, i.WorkspaceID, func(tx WorkspaceTx) error {
			a, e := tx.Member(ctx, i.InviterID)
			if errors.Is(e, ErrWorkspaceMissing) || e == nil && (a.Status != "active" || a.Role != "owner" && a.Role != "admin" || i.Role == "admin" && a.Role != "owner") {
				out.Status = "revoked"
				return nil
			}
			return e
		})
	}
	return out, err
}

// Resolve the current account to its own recipient row while holding the workspace lock.
func invitationForActor(ctx context.Context, tx WorkspaceTx, actor string, link Invitation) (Invitation, error) {
	if link.Kind == "link" {
		return tx.Invitation(ctx, link.ID)
	}
	email, verified, err := tx.AccountEmail(ctx, actor)
	if err != nil {
		return Invitation{}, err
	}
	if !verified {
		return Invitation{}, workspaceFailure("email_verification_required", 403, "请先完成邮箱验证，再接受邮箱邀请")
	}
	if link.Shared {
		i, e := tx.SharedRecipient(ctx, link.TokenHash, email)
		if !errors.Is(e, ErrWorkspaceMissing) {
			return i, e
		}
	} else if link.Email == email {
		return tx.Invitation(ctx, link.ID)
	}
	return Invitation{}, workspaceFailure("invitation_email_mismatch", 403, "请使用受邀邮箱登录")
}
func (s *WorkspaceService) verifyInviter(ctx context.Context, tx WorkspaceTx, i Invitation) error {
	inviter, err := workspaceAuthorized(ctx, tx, i.InviterID, true)
	if err != nil {
		var denied *WorkspaceError
		if errors.As(err, &denied) {
			return workspaceFailure("invitation_invalid", 410, "邀请人已无邀请权限")
		}
		return err
	}
	if i.Role == "admin" && inviter.Role != "owner" {
		return workspaceFailure("invitation_invalid", 410, "邀请人已无邀请权限")
	}
	return nil
}
func (s *WorkspaceService) invitationMember(ctx context.Context, tx WorkspaceTx, actor string, i Invitation) (Member, bool, error) {
	m, err := tx.Member(ctx, actor)
	if err == nil {
		if m.Status != "active" {
			return m, false, workspaceFailure("member_disabled", 403, "成员已停用，请联系管理员")
		}
		return m, true, nil
	}
	if !errors.Is(err, ErrWorkspaceMissing) {
		return m, false, err
	}
	redeemed, err := tx.Redeemed(ctx, i.ID, actor)
	if err != nil {
		return m, false, err
	}
	removedAt, err := tx.RemovedMember(ctx, actor)
	if err != nil {
		return m, false, err
	}
	if redeemed || !removedAt.IsZero() && !i.CreatedAt.After(removedAt) {
		return m, false, workspaceFailure("member_removed", 403, "您已被移出工作区，请联系管理员重新邀请")
	}
	return Member{ID: actor, Role: i.Role, Status: "active", Version: 1}, false, nil
}
func (s *WorkspaceService) AcceptInvitation(ctx context.Context, actor, token string) (Workspace, error) {
	var w Workspace
	if len(token) != 64 {
		return w, workspaceFailure("invitation_invalid", 410, "邀请无效，请联系邀请人")
	}
	i, err := s.Store.FindInvitation(ctx, invitationHash(token))
	if errors.Is(err, ErrWorkspaceMissing) {
		return w, workspaceFailure("invitation_invalid", 410, "邀请无效，请联系邀请人")
	}
	if err != nil {
		return w, err
	}
	err = s.Store.WithWorkspace(ctx, i.WorkspaceID, func(tx WorkspaceTx) error {
		current, e := invitationForActor(ctx, tx, actor, i)
		if e != nil {
			return e
		}
		if e = s.verifyInviter(ctx, tx, current); e != nil {
			return e
		}
		m, exists, e := s.invitationMember(ctx, tx, actor, current)
		if e != nil {
			return e
		}
		status := invitationStatus(current, s.now())
		if status != "valid" && (current.Kind == "link" || status != "accepted" || !exists) {
			return workspaceFailure("invitation_invalid", 410, "邀请已失效，请联系邀请人")
		}
		if !exists {
			if e = tx.SaveMember(ctx, m); e != nil {
				return e
			}
		}
		if e = tx.RecordRedemption(ctx, current.ID, actor); e != nil {
			return e
		}
		if current.Kind != "link" && current.Status != "accepted" {
			if e = tx.SetInvitationStatus(ctx, current.ID, "accepted"); e != nil {
				return e
			}
		}
		w = tx.Workspace()
		w.Role = m.Role
		return nil
	})
	return w, err
}
