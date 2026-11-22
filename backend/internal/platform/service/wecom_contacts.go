package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type WecomContact struct {
	SearchUserID       string   `json:"search_user_id"`
	DisplayName        string   `json:"display_name"`
	DepartmentPaths    []string `json:"department_paths"`
	VerificationStatus string   `json:"verification_status"`
	CallbackUserID     string   `json:"callback_user_id,omitempty"`
	VerificationID     string   `json:"verification_id,omitempty"`
}
type WecomContactSearch struct {
	Items   []WecomContact `json:"items"`
	Limited bool           `json:"limited"`
}
type WecomContactProvider interface {
	Search(context.Context, string, string, string) (WecomContactSearch, error)
}
type WecomVerification struct {
	ID             string    `json:"id"`
	SearchUserID   string    `json:"search_user_id"`
	Status         string    `json:"status"`
	CallbackUserID string    `json:"callback_user_id,omitempty"`
	Code           string    `json:"code,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	CodeHash       string    `json:"-"`
	Ciphertext     []byte    `json:"-"`
}
type WecomContactStore interface {
	MatchVerifiedWecomContacts(context.Context, ChannelBinding, string, string) (WecomContactSearch, error)
	CacheWecomContacts(context.Context, ChannelBinding, string, []WecomContact) ([]WecomContact, error)
	IssueWecomVerification(context.Context, ChannelBinding, string, WecomVerification) (WecomVerification, error)
	GetWecomVerification(context.Context, ChannelBinding, string, string, bool) (WecomVerification, error)
}
type WecomContactsService struct {
	Channels        *ChannelService
	Store           WecomContactStore
	Provider        WecomContactProvider
	permissionCache wecomContactPermissionCache
}

func (s *WecomContactsService) binding(ctx context.Context, w, actor, a string) (ChannelBinding, error) {
	b, e := s.Channels.GetProvider(ctx, w, actor, a, "wecom")
	if e != nil {
		return b, e
	}
	if !b.Enabled || !b.IdentityConfirmed || b.IdentityVerifiedGeneration != b.Generation {
		return b, Fail("wecom_identity_pending", 409, "请等待已连接机器人收到首条可信消息后再操作联系人")
	}
	return b, nil
}
func (s *WecomContactsService) Search(ctx context.Context, w, actor, a, q string) (WecomContactSearch, error) {
	b, e := s.binding(ctx, w, actor, a)
	if e != nil {
		return WecomContactSearch{}, e
	}
	q = strings.TrimSpace(q)
	if q == "" || utf8.RuneCountInString(q) > 64 || strings.IndexFunc(q, unicode.IsControl) >= 0 {
		return WecomContactSearch{}, Fail("invalid_contact_query", 400, "请输入不超过64字的姓名或关键词")
	}
	if s.Provider == nil {
		return WecomContactSearch{}, Fail("wecom_contacts_unavailable", 503, "企微联系人搜索暂不可用")
	}
	secret, e := s.Channels.open(b)
	if e != nil {
		return WecomContactSearch{}, Fail("wecom_credentials_unavailable", 503, "渠道凭据不可用")
	}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v, e := s.Provider.Search(call, b.AppID, secret, q)
	if e != nil {
		return WecomContactSearch{}, e
	}
	if len(v.Items) > 100 {
		return WecomContactSearch{}, Fail("wecom_contacts_invalid", 502, "联系人响应无效")
	}
	for _, item := range v.Items {
		if !validWecomContact(item) {
			return WecomContactSearch{}, Fail("wecom_contacts_invalid", 502, "联系人响应无效")
		}
	}
	v.Items, e = s.Store.CacheWecomContacts(ctx, b, actor, v.Items)
	if e != nil {
		return WecomContactSearch{}, e
	}
	local, e := s.Store.MatchVerifiedWecomContacts(ctx, b, actor, q)
	if e != nil {
		return WecomContactSearch{}, e
	}
	// Remote success is required above; local retained names do not hide failures.
	byID := make(map[string]WecomContact, len(local.Items)+len(v.Items))
	for _, item := range local.Items {
		byID[item.SearchUserID] = item
	}
	for _, item := range v.Items {
		byID[item.SearchUserID] = item
	}
	v.Items = make([]WecomContact, 0, len(byID))
	for _, item := range byID {
		v.Items = append(v.Items, item)
	}
	sort.Slice(v.Items, func(i, j int) bool { return v.Items[i].SearchUserID < v.Items[j].SearchUserID })
	v.Limited = v.Limited || local.Limited || len(v.Items) > 100
	if len(v.Items) > 100 {
		v.Items = v.Items[:100]
	}
	return v, nil
}
func validWecomContact(v WecomContact) bool {
	if v.SearchUserID == "" || len(v.SearchUserID) > 256 || strings.IndexFunc(v.SearchUserID, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || strings.TrimSpace(v.DisplayName) == "" || utf8.RuneCountInString(v.DisplayName) > 256 || strings.IndexFunc(v.DisplayName, unicode.IsControl) >= 0 || len(v.DepartmentPaths) > 32 {
		return false
	}
	for _, p := range v.DepartmentPaths {
		if len(p) > 2048 || strings.IndexFunc(p, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

// Hash is keyed and scoped to one employee. A known code from an old binding or
// credential generation remains control, but can never authenticate a new scope.
func WecomCodeHash(secret []byte, w, a, code string) string {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte("kiko:wecom-relay:v1:" + w + ":" + a + ":" + code))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *WecomContactsService) Start(ctx context.Context, w, actor, a, user, id string) (WecomVerification, error) {
	b, e := s.binding(ctx, w, actor, a)
	if e != nil {
		return WecomVerification{}, e
	}
	if !agentUUID(id) || user == "" || len(user) > 256 {
		return WecomVerification{}, Fail("invalid_verification_request", 400, "联系人验证请求无效")
	}
	for attempt := 0; attempt < 8; attempt++ {
		v, e := s.issueCode(ctx, b, actor, user, id)
		var failure *Error
		if errors.As(e, &failure) && failure.Code == "verification_code_collision" {
			continue
		}
		if e != nil {
			return v, e
		}
		return s.reveal(b, v)
	}
	return WecomVerification{}, Fail("wecom_verification_limit", 503, "验证码暂不可用，请稍后重试")
}
func (s *WecomContactsService) issueCode(ctx context.Context, b ChannelBinding, actor, user, id string) (WecomVerification, error) {
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return WecomVerification{}, e
	}
	code := fmt.Sprintf("%06d", n.Int64())
	codeBinding := b
	codeBinding.AgentID += ":wecom-code:" + id
	sealed, e := s.Channels.seal(codeBinding, code)
	if e != nil {
		return WecomVerification{}, e
	}
	return s.Store.IssueWecomVerification(ctx, b, actor, WecomVerification{ID: id, SearchUserID: user, Status: "pending", ExpiresAt: time.Now().Add(24 * time.Hour), CodeHash: WecomCodeHash(s.Channels.Secret, b.WorkspaceID, b.AgentID, code), Ciphertext: sealed})
}

func (s *WecomContactsService) Get(ctx context.Context, w, actor, a, id string, cancel bool) (WecomVerification, error) {
	b, e := s.binding(ctx, w, actor, a)
	if e != nil {
		return WecomVerification{}, e
	}
	if !agentUUID(id) {
		return WecomVerification{}, Fail("invalid_verification_request", 400, "联系人验证请求无效")
	}
	v, e := s.Store.GetWecomVerification(ctx, b, actor, id, cancel)
	if e != nil {
		return v, e
	}
	return s.reveal(b, v)
}
func (s *WecomContactsService) reveal(b ChannelBinding, v WecomVerification) (WecomVerification, error) {
	if v.Status != "pending" {
		return v, nil
	}
	b.AgentID += ":wecom-code:" + v.ID
	b.Ciphertext = v.Ciphertext
	code, e := s.Channels.open(b)
	if e != nil {
		return WecomVerification{}, Fail("verification_code_unavailable", 503, "验证码不可读取，请取消后重新创建")
	}
	v.Code = code
	return v, nil
}
