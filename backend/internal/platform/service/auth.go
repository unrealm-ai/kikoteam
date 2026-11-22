// Package service owns platform identity use cases independently of HTTP and SQL.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"math/big"
	"net/mail"
	"strings"
	"time"
)

type Error struct {
	Code    string
	Status  int
	Message string
}

func (e *Error) Error() string                           { return e.Message }
func Fail(code string, status int, message string) error { return &Error{code, status, message} }

type Account struct {
	CreditBalance        string    `json:"credit_balance,omitempty"`
	IsPlatformAdmin      bool      `json:"is_platform_admin"`
	PlatformAdminVersion int64     `json:"platform_admin_version"`
	CreatedAt            time.Time `json:"created_at"`
	ID                   string    `json:"id"`
	Email                string    `json:"email"`
	Name                 string    `json:"name"`
	EmailVerified        bool      `json:"email_verified"`
	PasswordHash         string    `json:"-"`
}
type Challenge struct {
	ID, Email, Purpose, Hash string
	ExpiresAt                time.Time
}
type ChallengeReceipt struct {
	ID          string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAfter int       `json:"resend_after"`
}
type AuthTx interface {
	CreateAccount(context.Context, Account) error
	CreatePersonalWorkspace(context.Context, string, Workspace) error
	VerifyEmail(context.Context, string) error
	FindAccount(context.Context, string) (Account, error)
	SetPassword(context.Context, string, string) error
	CreateSession(context.Context, string, string, time.Time) error
	RevokeSessions(context.Context, string) error
}
type AuthStore interface {
	RegisterPassword(context.Context, Account, Workspace, string, time.Time) error
	Issue(context.Context, Challenge) error
	CancelChallenge(context.Context, string) error
	Consume(context.Context, Challenge, func(AuthTx) error) error
	CheckChallenge(context.Context, Challenge) error
	FindAccount(context.Context, string) (Account, error)
	PasswordSession(context.Context, string, string, string, time.Time) error
	SessionAccount(context.Context, string) (Account, error)
	DeleteSession(context.Context, string) error
	Allow(context.Context, string, int, time.Duration) error
}
type Mailer interface {
	SendCode(context.Context, string, string, string) error
}
type AuthService struct {
	ReadMailPolicy       MailPolicyReader
	VerificationRequired bool
	EmailCodeDisabled    bool
	Store                AuthStore
	Mailer               Mailer
	Secret               []byte
	Now                  func() time.Time
}

func (s *AuthService) MailPolicy(ctx context.Context) (MailPolicy, error) {
	if s.ReadMailPolicy != nil {
		return s.ReadMailPolicy(ctx)
	}
	return MailPolicy{Enabled: !s.EmailCodeDisabled, VerificationRequired: s.VerificationRequired}, nil
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func HashToken(raw string) string { v := sha256.Sum256([]byte(raw)); return hex.EncodeToString(v[:]) }
func newToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email || len(email) > 254 {
		return "", Fail("invalid_email", 400, "邮箱格式无效")
	}
	return email, nil
}
func (s *AuthService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *AuthService) codeHash(id, code string) string {
	m := hmac.New(sha256.New, s.Secret)
	_, _ = m.Write([]byte(id + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}
func (s *AuthService) RequestCode(ctx context.Context, raw, purpose, source string) (ChallengeReceipt, error) {
	policy, policyErr := s.MailPolicy(ctx)
	if policyErr != nil {
		return ChallengeReceipt{}, policyErr
	}
	if !policy.Enabled {
		return ChallengeReceipt{}, Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用密码登录或联系管理员")
	}
	email, err := NormalizeEmail(raw)
	if err != nil {
		return ChallengeReceipt{}, err
	}
	if purpose != "register" && purpose != "login" && purpose != "reset_password" {
		return ChallengeReceipt{}, Fail("invalid_purpose", 400, "验证码用途无效")
	}
	if err = s.Store.Allow(ctx, "mail-ip:"+HashToken(source), 30, time.Hour); err != nil {
		return ChallengeReceipt{}, err
	}
	if err = s.Store.Allow(ctx, "mail-email:"+HashToken(email), 10, time.Hour); err != nil {
		return ChallengeReceipt{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return ChallengeReceipt{}, err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	id := NewID()
	exp := s.now().Add(5 * time.Minute)
	challenge := Challenge{id, email, purpose, s.codeHash(id, code), exp}
	if err = s.Store.Issue(ctx, challenge); err != nil {
		return ChallengeReceipt{}, err
	}
	if err = s.Mailer.SendCode(ctx, email, purpose, code); err != nil {
		if cancelErr := s.Store.CancelChallenge(ctx, id); cancelErr != nil {
			return ChallengeReceipt{}, fmt.Errorf("cancel undelivered challenge: %w", cancelErr)
		}
		return ChallengeReceipt{}, Fail("email_delivery_failed", 503, "验证码发送失败，请稍后重试")
	}
	return ChallengeReceipt{id, exp, 60}, nil
}
func passwordHash(password string) (string, error) {
	if len(password) < 10 || len(password) > 72 {
		return "", Fail("invalid_password", 400, "密码需要 10 至 72 个字节")
	}
	b, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), e
}
func (s *AuthService) Register(ctx context.Context, email, name, password, code, id string, sources ...string) (Account, string, error) {
	policy, policyErr := s.MailPolicy(ctx)
	if policyErr != nil {
		return Account{}, "", policyErr
	}
	if !policy.Enabled {
		return Account{}, "", Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用密码登录或联系管理员")
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Account{}, "", err
	}
	if err = s.precheck(ctx, email, "register", code, id, sources); err != nil {
		return Account{}, "", err
	}
	name = strings.TrimSpace(name)
	if len(name) == 0 || len([]rune(name)) > 100 {
		return Account{}, "", Fail("invalid_name", 400, "名称需要 1 至 100 个字")
	}
	hash := ""
	if password != "" {
		hash, err = passwordHash(password)
		if err != nil {
			return Account{}, "", err
		}
	}
	a := Account{ID: NewID(), Email: email, Name: name, EmailVerified: true, PasswordHash: hash}
	token, err := newToken()
	if err != nil {
		return Account{}, "", err
	}
	err = s.Store.Consume(ctx, Challenge{ID: id, Email: email, Purpose: "register", Hash: s.codeHash(id, code)}, func(tx AuthTx) error {
		if err := tx.CreateAccount(ctx, a); err != nil {
			return err
		}
		if err := tx.CreatePersonalWorkspace(ctx, a.ID, personalWorkspace(a.Name)); err != nil {
			return err
		}
		return tx.CreateSession(ctx, HashToken(token), a.ID, s.now().Add(30*24*time.Hour))
	})
	return a, token, err
}
func (s *AuthService) Login(ctx context.Context, email, method, password, code, id, source string) (Account, string, error) {
	if method == "code" {
		policy, err := s.MailPolicy(ctx)
		if err != nil {
			return Account{}, "", err
		}
		if !policy.Enabled {
			return Account{}, "", Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用密码登录或联系管理员")
		}
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Account{}, "", err
	}
	if err = s.Store.Allow(ctx, "login:"+HashToken(source+":"+email), 20, 15*time.Minute); err != nil {
		return Account{}, "", err
	}
	token, err := newToken()
	if err != nil {
		return Account{}, "", err
	}
	var a Account
	if method == "code" {
		err = s.Store.Consume(ctx, Challenge{ID: id, Email: email, Purpose: "login", Hash: s.codeHash(id, code)}, func(tx AuthTx) error {
			var e error
			a, e = tx.FindAccount(ctx, email)
			if e != nil {
				return e
			}
			if !a.EmailVerified {
				return Fail("email_verification_required", 403, "请使用邮箱验证码重置密码，完成邮箱验证后再登录")
			}
			return tx.CreateSession(ctx, HashToken(token), a.ID, s.now().Add(30*24*time.Hour))
		})
		return a, token, err
	}
	if method != "password" {
		return a, "", Fail("invalid_method", 400, "登录方式无效")
	}
	a, err = s.Store.FindAccount(ctx, email)
	if err != nil || a.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) != nil {
		return Account{}, "", Fail("invalid_credentials", 401, "邮箱或密码错误")
	}
	err = s.Store.PasswordSession(ctx, a.Email, a.PasswordHash, HashToken(token), s.now().Add(30*24*time.Hour))
	return a, token, err
}
func (s *AuthService) ResetPassword(ctx context.Context, email, code, id, password string, sources ...string) error {
	policy, policyErr := s.MailPolicy(ctx)
	if policyErr != nil {
		return policyErr
	}
	if !policy.Enabled {
		return Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用密码登录或联系管理员")
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err = s.precheck(ctx, email, "reset_password", code, id, sources); err != nil {
		return err
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	return s.Store.Consume(ctx, Challenge{ID: id, Email: email, Purpose: "reset_password", Hash: s.codeHash(id, code)}, func(tx AuthTx) error {
		a, e := tx.FindAccount(ctx, email)
		if e != nil {
			return e
		}
		if e = tx.SetPassword(ctx, a.ID, hash); e != nil {
			return e
		}
		if e = tx.VerifyEmail(ctx, a.ID); e != nil {
			return e
		}
		return tx.RevokeSessions(ctx, a.ID)
	})
}
func (s *AuthService) Me(ctx context.Context, token string) (Account, error) {
	if token == "" {
		return Account{}, Fail("unauthenticated", 401, "请先登录")
	}
	return s.Store.SessionAccount(ctx, HashToken(token))
}
func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.Store.DeleteSession(ctx, HashToken(token))
}

var ErrNotFound = errors.New("not found")

func (s *AuthService) precheck(ctx context.Context, email, purpose, code, id string, sources []string) error {
	source := "internal"
	if len(sources) > 0 {
		source = sources[0]
	}
	if err := s.Store.Allow(ctx, "verify-source:"+HashToken(source), 30, 15*time.Minute); err != nil {
		return err
	}
	if err := s.Store.Allow(ctx, "verify-email:"+HashToken(email), 15, 15*time.Minute); err != nil {
		return err
	}
	return s.Store.CheckChallenge(ctx, Challenge{ID: id, Email: email, Purpose: purpose, Hash: s.codeHash(id, code)})
}

// RegisterWithPassword is an explicitly unverified local account path. It must
// never establish mailbox ownership or grant email-bound invitation access.
func (s *AuthService) RegisterWithPassword(ctx context.Context, email, name, password, source string) (Account, string, error) {
	policy, policyErr := s.MailPolicy(ctx)
	if policyErr != nil {
		return Account{}, "", policyErr
	}
	if policy.VerificationRequired {
		return Account{}, "", Fail("email_verification_required", 403, "当前部署要求邮箱验证码注册")
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Account{}, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return Account{}, "", Fail("invalid_name", 400, "名称需要 1 至 100 个字")
	}
	if err = s.Store.Allow(ctx, "register-source:"+HashToken(source), 20, 15*time.Minute); err != nil {
		return Account{}, "", err
	}
	if err = s.Store.Allow(ctx, "register-email:"+HashToken(email), 5, 15*time.Minute); err != nil {
		return Account{}, "", err
	}
	hash, err := passwordHash(password)
	if err != nil {
		return Account{}, "", err
	}
	a := Account{ID: NewID(), Email: email, Name: name, EmailVerified: false, PasswordHash: hash}
	token, err := newToken()
	if err != nil {
		return Account{}, "", err
	}
	err = s.Store.RegisterPassword(ctx, a, personalWorkspace(a.Name), HashToken(token), s.now().Add(30*24*time.Hour))
	return a, token, err
}

func personalWorkspace(name string) Workspace {
	// Bound UTF-8 bytes as well as characters for deployments using SQL_ASCII.
	chars := []rune(name)
	for len(string(chars)) > 80 {
		chars = chars[:len(chars)-1]
	}
	return Workspace{ID: NewID(), Name: string(chars) + "的工作区", Role: "owner", Version: 1}
}
