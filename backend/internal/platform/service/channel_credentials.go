package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ChannelCredentialProbe authenticates application credentials without opening a
// bot transport, sending messages, persisting bindings, or claiming permissions.
type ChannelCredentialProbe interface {
	Verify(context.Context, string, string, string) error
}
type ChannelCredentialTest struct {
	Version   int64  `json:"version"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}
type ChannelCredentialReceipt struct {
	CredentialStatus string     `json:"credential_status"`
	Receipt          string     `json:"receipt,omitempty"`
	TestedAt         time.Time  `json:"tested_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}
type channelReceiptClaim struct {
	Fingerprint string `json:"fingerprint"`
	Expires     int64  `json:"expires"`
}

func (s *ChannelService) credentialNow() time.Time {
	if s.CredentialNow != nil {
		return s.CredentialNow().UTC()
	}
	return time.Now().UTC()
}
func (s *ChannelService) credentialMAC(value []byte) []byte {
	key := sha256.Sum256(append([]byte("kiko:channel-test:v1:"), s.Secret...))
	h := hmac.New(sha256.New, key[:])
	h.Write(value)
	return h.Sum(nil)
}
func (s *ChannelService) credentialFingerprint(w, a, employee, provider string, q ChannelCredentialTest) (string, error) {
	value, err := json.Marshal([]any{w, a, employee, provider, q.Version, q.AppID, q.AppSecret})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(s.credentialMAC(value)), nil
}
func validateManualChannelSecret(secret *string) error {
	if secret == nil {
		return nil
	}
	value := strings.TrimSpace(*secret)
	if value == "" || strings.Contains(value, "•") || len(value) >= 4 && strings.Trim(value, "*") == "" {
		return Fail("invalid_channel", 400, "请填写完整的渠道凭据，不能使用脱敏占位符")
	}
	return nil
}

func credentialTestPut(q ChannelCredentialTest) ChannelPut {
	return ChannelPut{Version: q.Version, AppID: q.AppID, AppSecret: &q.AppSecret, History: ChannelHistory{Limit: 20}, Access: ChannelAccess{Mode: "allowlist"}}
}
func (s *ChannelService) credentialCurrent(ctx context.Context, w, a, employee, provider string, version int64) (ChannelBinding, error) {
	current, err := s.GetProvider(ctx, w, a, employee, provider)
	if IsChannelMissing(err) {
		if version == 0 {
			return ChannelBinding{}, nil
		}
		return ChannelBinding{}, Fail("channel_version_conflict", 409, "渠道配置已更新，请刷新后重试")
	}
	if err != nil {
		return ChannelBinding{}, err
	}
	if current.Revision != version {
		return ChannelBinding{}, Fail("channel_version_conflict", 409, "渠道配置已更新，请刷新后重试")
	}
	return current, nil
}
func (s *ChannelService) TestCredentials(ctx context.Context, w, a, employee, provider string, q ChannelCredentialTest) (ChannelCredentialReceipt, error) {
	var out ChannelCredentialReceipt
	p := credentialTestPut(q)
	if err := validateChannelFor(&p, provider); err != nil {
		return out, err
	}
	if err := validateManualChannelSecret(p.AppSecret); err != nil {
		return out, err
	}
	q.AppID = p.AppID
	if _, err := s.credentialCurrent(ctx, w, a, employee, provider, q.Version); err != nil {
		return out, err
	}
	out.TestedAt = s.credentialNow()
	if provider == "wecom" {
		out.CredentialStatus = "format_only"
		return out, nil
	}
	if len(s.Secret) < 32 || s.CredentialProbe == nil {
		return out, Fail("channel_validation_unavailable", 503, "渠道凭据验证暂不可用")
	}
	if err := s.CredentialProbe.Verify(ctx, provider, q.AppID, q.AppSecret); err != nil {
		return out, err
	}
	// Do not issue usable evidence after revocation or a concurrent binding change.
	if _, err := s.credentialCurrent(ctx, w, a, employee, provider, q.Version); err != nil {
		return out, err
	}
	out.TestedAt = s.credentialNow()
	expires := out.TestedAt.Add(5 * time.Minute)
	fingerprint, err := s.credentialFingerprint(w, a, employee, provider, q)
	if err != nil {
		return out, err
	}
	claim, err := json.Marshal(channelReceiptClaim{Fingerprint: fingerprint, Expires: expires.Unix()})
	if err != nil {
		return out, err
	}
	out.Receipt = base64.RawURLEncoding.EncodeToString(claim) + "." + base64.RawURLEncoding.EncodeToString(s.credentialMAC(claim))
	out.ExpiresAt = &expires
	out.CredentialStatus = "verified"
	return out, nil
}
func (s *ChannelService) verifyCredentialReceipt(w, a, employee, provider string, q ChannelCredentialTest, receipt string) error {
	invalid := func() error { return Fail("channel_test_required", 409, "请先验证当前渠道凭据后再保存") }
	if len(s.Secret) < 32 || len(receipt) > 2048 {
		return invalid()
	}
	parts := strings.Split(receipt, ".")
	if len(parts) != 2 {
		return invalid()
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return invalid()
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(sig, s.credentialMAC(raw)) {
		return invalid()
	}
	var claim channelReceiptClaim
	if json.Unmarshal(raw, &claim) != nil || claim.Expires <= s.credentialNow().Unix() || claim.Expires > s.credentialNow().Add(5*time.Minute).Unix() {
		return invalid()
	}
	fingerprint, err := s.credentialFingerprint(w, a, employee, provider, q)
	if err != nil {
		return invalid()
	}
	if !hmac.Equal([]byte(claim.Fingerprint), []byte(fingerprint)) {
		return invalid()
	}
	return nil
}

// PutManual is the browser credential boundary. Trusted registration services
// continue to use PutProvider after their own provider authentication.
func (s *ChannelService) PutManual(ctx context.Context, w, a, employee, provider string, q ChannelPut, receipt string, confirmedImpact bool) (ChannelBinding, error) {
	var empty ChannelBinding
	if err := validateChannelFor(&q, provider); err != nil {
		return empty, err
	}
	if err := validateManualChannelSecret(q.AppSecret); err != nil {
		return empty, err
	}
	current, err := s.credentialCurrent(ctx, w, a, employee, provider, q.Version)
	if err != nil {
		return empty, err
	}
	if current.ID != "" && current.AppID != q.AppID && !confirmedImpact {
		return empty, Fail("channel_rebind_confirmation_required", 409, "更换机器人需确认对原渠道身份和配置的影响")
	}
	if q.AppSecret != nil && provider != "wecom" {
		if err = s.verifyCredentialReceipt(w, a, employee, provider, ChannelCredentialTest{Version: q.Version, AppID: q.AppID, AppSecret: *q.AppSecret}, receipt); err != nil {
			return empty, err
		}
	}
	// PutProvider rechecks actor membership and the exact binding revision inside
	// its transaction; a replay or concurrent save cannot reuse the old evidence.
	return s.PutProvider(ctx, w, a, employee, provider, q)
}
