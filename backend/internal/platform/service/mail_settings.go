package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

type MailPolicy struct {
	Enabled              bool `json:"enabled"`
	VerificationRequired bool `json:"registration_verification_required"`
}

type MailPolicyReader func(context.Context) (MailPolicy, error)

type MailSettings struct {
	MailPolicy
	Address     string `json:"address"`
	Host        string `json:"host"`
	Username    string `json:"username"`
	From        string `json:"from"`
	ImplicitTLS bool   `json:"implicit_tls"`
}

type MailSettingsDraft struct {
	MailSettings
	Version  int64  `json:"version"`
	Password string `json:"password,omitempty"`
}

type MailSettingsView struct {
	MailSettings
	Version            int64  `json:"version"`
	Source             string `json:"source"`
	PasswordConfigured bool   `json:"password_configured"`
}

type MailSettingsRecord struct {
	MailSettings
	Version    int64
	Ciphertext []byte
}

type MailSettingsStore interface {
	Read(context.Context) (MailSettingsRecord, error)
	ReadPolicy(context.Context, MailPolicy) (MailPolicy, error)
	AdminEmail(context.Context, string) (string, error)
	Save(context.Context, string, int64, MailSettingsRecord) (MailSettingsRecord, error)
}

type MailSettingsService struct {
	Store     MailSettingsStore
	Fallback  MailSettingsDraft
	Secret    []byte
	Probe     func(context.Context, MailSettingsDraft) error
	SendTest  func(context.Context, MailSettingsDraft, string) error
	AllowTest func(context.Context, string, int, time.Duration) error
}

func (s *MailSettingsService) Policy(ctx context.Context) (MailPolicy, error) {
	return s.Store.ReadPolicy(ctx, s.Fallback.MailPolicy)
}

func (s *MailSettingsService) Runtime(ctx context.Context) (MailSettingsDraft, error) {
	record, err := s.Store.Read(ctx)
	if err != nil {
		return MailSettingsDraft{}, err
	}
	if record.Version == 0 {
		return s.Fallback, nil
	}
	draft := MailSettingsDraft{MailSettings: record.MailSettings, Version: record.Version}
	if record.Enabled {
		draft.Password, err = s.open(record.Ciphertext)
	}
	return draft, err
}

func (s *MailSettingsService) Get(ctx context.Context, actor string) (MailSettingsView, error) {
	if _, err := s.Store.AdminEmail(ctx, actor); err != nil {
		return MailSettingsView{}, err
	}
	record, err := s.Store.Read(ctx)
	if err != nil {
		return MailSettingsView{}, err
	}
	if record.Version == 0 {
		return MailSettingsView{MailSettings: s.Fallback.MailSettings, Source: "environment", PasswordConfigured: s.Fallback.Password != ""}, nil
	}
	return mailSettingsView(record), nil
}

func mailSettingsView(r MailSettingsRecord) MailSettingsView {
	return MailSettingsView{MailSettings: r.MailSettings, Version: r.Version, Source: "database", PasswordConfigured: len(r.Ciphertext) > 0}
}

func MailSettingsConflict() error {
	return Fail("mail_settings_version_conflict", 409, "邮件设置已更新，请刷新后重试")
}

func invalidMailSettings(message string) error {
	return Fail("invalid_mail_settings", 400, message)
}

func (s *MailSettingsService) prepare(ctx context.Context, in MailSettingsDraft, test bool) (MailSettingsDraft, MailSettingsRecord, error) {
	old, err := s.Store.Read(ctx)
	if err != nil {
		return in, old, err
	}
	if old.Version != in.Version {
		return in, old, MailSettingsConflict()
	}
	if old.Version == 0 {
		old.MailSettings = s.Fallback.MailSettings
	}
	in.Address = strings.TrimSpace(in.Address)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.From = strings.TrimSpace(in.From)
	if err = validateMailSettings(in, test); err != nil {
		return in, old, err
	}
	targetMatches := sameMailCredentials(in.MailSettings, old.MailSettings)
	if in.Password == "" && in.Username != "" && targetMatches {
		if old.Version == 0 {
			in.Password = s.Fallback.Password
		} else {
			in.Password, err = s.open(old.Ciphertext)
			if err != nil {
				return in, old, err
			}
		}
	}
	if (in.Enabled || test) && in.Username != "" && in.Password == "" {
		return in, old, invalidMailSettings("请输入 SMTP 密码；更换服务器或账号时不能沿用旧密码")
	}
	return in, old, nil
}

func sameMailCredentials(a, b MailSettings) bool {
	return a.Address == b.Address && strings.EqualFold(a.Host, b.Host) && a.Username == b.Username && a.ImplicitTLS == b.ImplicitTLS
}

func validateMailSettings(in MailSettingsDraft, test bool) error {
	if in.Version < 0 || in.VerificationRequired && !in.Enabled {
		return invalidMailSettings("要求邮箱验证前请启用邮件服务")
	}
	if len(in.Address) > 260 || len(in.Host) > 253 || len(in.Username) > 320 || len(in.From) > 320 || len(in.Password) > 4096 ||
		strings.ContainsAny(in.Address+in.Host+in.Username+in.From+in.Password, "\x00\r\n") || in.Password != "" && in.Username == "" {
		return invalidMailSettings("SMTP 配置格式无效")
	}
	if in.Enabled || test {
		host, port, err := net.SplitHostPort(in.Address)
		n, portErr := strconv.Atoi(port)
		from, fromErr := mail.ParseAddress(in.From)
		if err != nil || portErr != nil || host == "" || n < 1 || n > 65535 ||
			in.Host == "" || strings.ContainsAny(in.Host, " \t/:?#@") || fromErr != nil || from.Address != in.From {
			return invalidMailSettings("请填写有效的 SMTP 地址、TLS 主机名和发件人邮箱")
		}
	}
	return nil
}

func (s *MailSettingsService) Save(ctx context.Context, actor string, in MailSettingsDraft) (MailSettingsView, error) {
	if _, err := s.Store.AdminEmail(ctx, actor); err != nil {
		return MailSettingsView{}, err
	}
	candidate, old, err := s.prepare(ctx, in, false)
	if err != nil {
		return MailSettingsView{}, err
	}
	if candidate.Enabled && (!old.Enabled || old.Version == 0 || !sameMailCredentials(old.MailSettings, candidate.MailSettings) || old.From != candidate.From || in.Password != "") {
		if s.AllowTest != nil {
			if err = s.AllowTest(ctx, "platform-mail-probe:"+HashToken(actor), 10, 10*time.Minute); err != nil {
				return MailSettingsView{}, err
			}
		}
		if s.Probe == nil || s.Probe(ctx, candidate) != nil {
			return MailSettingsView{}, Fail("smtp_test_failed", 400, "SMTP 连接或认证失败，请检查配置")
		}
	}
	sealed, err := s.seal(candidate.Password)
	if err != nil {
		return MailSettingsView{}, err
	}
	record, err := s.Store.Save(ctx, actor, in.Version, MailSettingsRecord{MailSettings: candidate.MailSettings, Ciphertext: sealed})
	if err != nil {
		return MailSettingsView{}, err
	}
	return mailSettingsView(record), nil
}

func (s *MailSettingsService) Test(ctx context.Context, actor string, in MailSettingsDraft) error {
	recipient, err := s.Store.AdminEmail(ctx, actor)
	if err != nil {
		return err
	}
	candidate, _, err := s.prepare(ctx, in, true)
	if err != nil {
		return err
	}
	if s.AllowTest != nil {
		if err = s.AllowTest(ctx, "platform-mail-test:"+HashToken(actor), 5, 10*time.Minute); err != nil {
			return err
		}
	}
	if s.SendTest == nil || s.SendTest(ctx, candidate, recipient) != nil {
		return Fail("smtp_test_failed", 400, "SMTP 测试邮件发送失败，请检查配置")
	}
	return nil
}

func (s *MailSettingsService) cipher() (cipher.AEAD, error) {
	if len(s.Secret) < 32 {
		return nil, errors.New("mail settings encryption unavailable")
	}
	key := sha256.Sum256(append([]byte("kiko:platform-smtp-password:v1:"), s.Secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

const mailSettingsAAD = "kiko:platform-mail-settings:singleton:v1"

func (s *MailSettingsService) seal(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	a, err := s.cipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, []byte(value), []byte(mailSettingsAAD)), nil
}

func (s *MailSettingsService) open(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	a, err := s.cipher()
	if err != nil {
		return "", err
	}
	n := a.NonceSize()
	if len(raw) < n {
		return "", errors.New("invalid SMTP password ciphertext")
	}
	value, err := a.Open(nil, raw[:n], raw[n:], []byte(mailSettingsAAD))
	return string(value), err
}
