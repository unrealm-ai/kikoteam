package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *ModelService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func modelAuthorize(ctx context.Context, t ModelTx, actor string, write bool) error {
	m, e := t.Member(ctx, actor)
	if errors.Is(e, ErrWorkspaceMissing) || e == nil && (m.Status != "active" || write && m.Role != "owner" && m.Role != "admin") {
		return Fail("workspace_forbidden", 403, "没有模型管理权限")
	}
	return e
}

type ModelSelection interface {
	Selection(context.Context, int, int) ([]ModelProfile, int, error)
}

func (s *ModelService) Selection(ctx context.Context, workspace, actor string, page, size int) (out ModelPage, e error) {
	out = ModelPage{Items: []ModelProfile{}, Page: page, PageSize: size}
	if e = pageValid(page, size); e != nil {
		return out, e
	}
	e = s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if err := modelAuthorize(ctx, t, actor, false); err != nil {
			return err
		}
		selectTx, ok := t.(ModelSelection)
		if !ok {
			return Fail("model_selection_unavailable", 503, "模型列表暂不可用")
		}
		out.Items, out.Total, e = selectTx.Selection(ctx, page, size)
		return e
	})
	return
}
func (s *ModelService) List(ctx context.Context, workspace, actor string, page, size int) (ModelPage, error) {
	out := ModelPage{Items: []ModelProfile{}, Page: page, PageSize: size}
	if e := pageValid(page, size); e != nil {
		return out, e
	}
	e := s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, false); e != nil {
			return e
		}
		var e error
		out.Items, out.Total, e = t.List(ctx, page, size)
		return e
	})
	return out, e
}
func (s *ModelService) Get(ctx context.Context, workspace, actor, id string) (ModelProfile, error) {
	var out ModelProfile
	e := s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, false); e != nil {
			return e
		}
		var e error
		out, e = t.Get(ctx, id)
		return e
	})
	return out, e
}
func (s *ModelService) normalize(c ModelConfig) (ModelConfig, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	c.ModelID = strings.TrimSpace(c.ModelID)
	if c.Name == "" || utf8.RuneCountInString(c.Name) > 100 || utf8.RuneCountInString(c.Description) > 2000 || c.ModelID == "" || utf8.RuneCountInString(c.ModelID) > 256 || strings.ContainsAny(c.ModelID, "\r\n") {
		return c, Fail("invalid_model", 400, "模型名称、说明或标识无效")
	}
	if c.Protocol != "chat_completions" && c.Protocol != "openai_responses" && c.Protocol != "anthropic_messages" {
		return c, Fail("invalid_protocol", 400, "不支持的模型协议")
	}
	if c.APIKey != nil && (len(*c.APIKey) > 8192 || strings.ContainsAny(*c.APIKey, "\r\n\x00")) {
		return c, Fail("invalid_credential", 400, "模型凭据无效")
	}
	if s.NormalizeEndpoint == nil {
		return c, Fail("model_probe_unavailable", 503, "模型验证尚未配置")
	}
	var e error
	c.BaseURL, e = s.NormalizeEndpoint(c.Protocol, strings.TrimSpace(c.BaseURL))
	if e != nil {
		return c, Fail("invalid_model_url", 400, "模型地址无效，请使用无查询参数和账号信息的 HTTPS 地址")
	}
	u, parseErr := url.Parse(c.BaseURL)
	if parseErr != nil || u == nil {
		return c, Fail("invalid_model_url", 400, "模型地址无效")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && (!s.AllowLoopbackHTTP || u.Scheme != "http" || !loopback) {
		return c, Fail("invalid_model_url", 400, "模型地址必须使用 HTTPS")
	}
	return c, nil
}
func (s *ModelService) modelCipher() (cipher.AEAD, error) {
	if len(s.Secret) < 32 {
		return nil, errors.New("model encryption key unavailable")
	}
	key := sha256.Sum256(append([]byte("kiko:model-credential:v1:"), s.Secret...))
	b, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (s *ModelService) encrypt(workspace, id, key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	a, e := s.modelCipher()
	if e != nil {
		return nil, e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return a.Seal(n, n, []byte(key), []byte(workspace+":"+id)), nil
}
func (s *ModelService) decrypt(workspace string, p ModelProfile) (string, error) {
	if len(p.Credential) == 0 {
		return "", nil
	}
	a, e := s.modelCipher()
	if e != nil {
		return "", e
	}
	if len(p.Credential) < a.NonceSize() {
		return "", errors.New("invalid model credential")
	}
	v, e := a.Open(nil, p.Credential[:a.NonceSize()], p.Credential[a.NonceSize():], []byte(workspace+":"+p.ID))
	if e != nil {
		return "", errors.New("model credential authentication failed")
	}
	return string(v), nil
}
func (s *ModelService) fingerprint(workspace, actor, id string, version int64, c ModelConfig, key string) string {
	b, err := json.Marshal(struct {
		Workspace, Actor, ID                         string
		Version                                      int64
		Name, Description, Protocol, URL, Model, Key string
	}{workspace, actor, id, version, c.Name, c.Description, c.Protocol, c.BaseURL, c.ModelID, key})
	if err != nil {
		panic("model fingerprint serialization failed")
	} // Only strings and integers are encoded.
	h := hmac.New(sha256.New, s.Secret)
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *ModelService) effectiveKey(workspace string, c ModelConfig, p ModelProfile) (string, error) {
	if c.APIKey != nil {
		return *c.APIKey, nil
	}
	if p.HasKey && p.BaseURL != c.BaseURL {
		return "", Fail("endpoint_change_requires_credential_replacement", 400, "更换模型地址时请重新填写凭据，不能将旧密钥发送到新地址")
	}
	return s.decrypt(workspace, p)
}
func versionCheck(p ModelProfile, v int64) error {
	if p.Version != v {
		return Fail("model_version_conflict", 409, "模型已被修改，请刷新后重新测试")
	}
	return nil
}
func (s *ModelService) Test(ctx context.Context, workspace, actor string, req ModelTestRequest) (ModelTestReceipt, error) {
	var out ModelTestReceipt
	c, e := s.normalize(req.ModelConfig)
	if e != nil {
		return out, e
	}
	var key string
	claim := NewID()
	e = s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, true); e != nil {
			return e
		}
		var p ModelProfile
		if req.ProfileID != "" {
			var e error
			p, e = t.Get(ctx, req.ProfileID)
			if e != nil {
				return e
			}
			if e = versionCheck(p, req.Version); e != nil {
				return e
			}
		} else if req.Version != 0 {
			return Fail("invalid_version", 400, "新模型不接受版本号")
		}
		var e error
		key, e = s.effectiveKey(workspace, c, p)
		if e != nil {
			return e
		}
		return t.ClaimProbe(ctx, actor, claim, 50*time.Second)
	})
	if e != nil {
		return out, e
	}
	defer s.releaseProbe(ctx, workspace, actor, claim)
	if e = s.Store.AllowProbe(ctx, workspace, actor); e != nil {
		return out, e
	}
	if s.Prober == nil {
		return out, Fail("model_probe_unavailable", 503, "模型验证尚未配置")
	}
	result, e := s.Prober.Probe(ctx, ProbeInput{Protocol: c.Protocol, Endpoint: c.BaseURL, ModelID: c.ModelID, APIKey: key})
	if e != nil {
		return out, e
	}
	out.ProbeResult = result
	out.ExpiresAt = s.now().Add(10 * time.Minute)
	if result.Text.Status != "passed" {
		return out, nil
	}
	raw, e := newToken()
	if e != nil {
		return out, e
	}
	out.Receipt = raw
	record := ModelReceiptRecord{Hash: HashToken(raw), Actor: actor, ProfileID: req.ProfileID, Version: req.Version, Fingerprint: s.fingerprint(workspace, actor, req.ProfileID, req.Version, c, key), ExpiresAt: out.ExpiresAt, TestedAt: s.now(), Result: result}
	e = s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, true); e != nil {
			return e
		}
		if req.ProfileID != "" {
			p, e := t.Get(ctx, req.ProfileID)
			if e != nil {
				return e
			}
			if e = versionCheck(p, req.Version); e != nil {
				return e
			}
		}
		return t.InsertReceipt(ctx, record)
	})
	return out, e
}
func (s *ModelService) Save(ctx context.Context, workspace, actor, id string, req ModelSaveRequest) (ModelProfile, error) {
	var out ModelProfile
	c, e := s.normalize(req.ModelConfig)
	if e != nil {
		return out, e
	}
	if len(req.TestReceipt) != 64 {
		return out, Fail("model_test_required", 409, "请先通过文字测试再保存")
	}
	e = s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, true); e != nil {
			return e
		}
		rec, e := t.Receipt(ctx, HashToken(req.TestReceipt))
		if errors.Is(e, ErrWorkspaceMissing) {
			return Fail("model_test_required", 409, "测试回执无效或已过期，请重新测试")
		}
		if e != nil {
			return e
		}
		if rec.Actor != actor || rec.ProfileID != id || rec.Version != req.Version || !s.now().Before(rec.ExpiresAt) || rec.Result.Text.Status != "passed" {
			return Fail("model_test_required", 409, "测试回执不适用于当前配置，请重新测试")
		}
		var p ModelProfile
		if id != "" {
			p, e = t.Get(ctx, id)
			if e != nil {
				return e
			}
		}
		key, e := s.effectiveKey(workspace, c, p)
		if e != nil {
			return e
		}
		if !hmac.Equal([]byte(rec.Fingerprint), []byte(s.fingerprint(workspace, actor, id, req.Version, c, key))) {
			return Fail("model_test_required", 409, "配置或凭据已修改，请重新测试")
		}
		if rec.UsedProfileID != "" {
			saved, e := t.Get(ctx, rec.UsedProfileID)
			if e != nil {
				return e
			}
			if saved.Version != req.Version+1 {
				return Fail("model_version_conflict", 409, "模型已有后续修改，请刷新")
			}
			saved.Replayed = true
			out = saved
			return nil
		}
		if id != "" {
			if e = versionCheck(p, req.Version); e != nil {
				return e
			}
		} else if req.Version != 0 {
			return Fail("invalid_version", 400, "新模型不接受版本号")
		}
		if id == "" {
			p.ID = NewID()
			p.Enabled = true
		}
		ciphertext, e := s.encrypt(workspace, p.ID, key)
		if e != nil {
			return e
		}
		p.Name = c.Name
		p.Description = c.Description
		p.Protocol = c.Protocol
		p.BaseURL = c.BaseURL
		p.ModelID = c.ModelID
		p.Version++
		p.CredentialRevision++
		p.Credential = ciphertext
		p.HasKey = key != ""
		p.VisionStatus = rec.Result.Image.Status
		p.TestedAt = rec.TestedAt
		p.Validated = true
		if e = t.Save(ctx, p, id == ""); e != nil {
			return e
		}
		if e = t.ConsumeReceipt(ctx, rec.Hash, p.ID); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, e
}
func (s *ModelService) Change(ctx context.Context, workspace, actor, id, action string, version int64, enabled bool) (ModelProfile, error) {
	var out ModelProfile
	e := s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error {
		if e := modelAuthorize(ctx, t, actor, true); e != nil {
			return e
		}
		p, e := t.Get(ctx, id)
		if e != nil {
			return e
		}
		if e = versionCheck(p, version); e != nil {
			return e
		}
		switch action {
		case "status":
			if enabled && !p.Validated {
				return Fail("model_test_required", 409, "凭据已撤销，请重新测试后保存")
			}
			p.Enabled = enabled
			if !enabled {
				p.IsDefault = false
			}
		case "set-default":
			if !p.Enabled {
				return Fail("model_disabled", 409, "已停用模型不能设为默认")
			}
			if e = t.ClearDefault(ctx); e != nil {
				return e
			}
			p.IsDefault = true
		case "revoke-key":
			p.Validated = false
			p.Credential = nil
			p.HasKey = false
			p.CredentialRevision++
			p.Enabled = false
			p.IsDefault = false
		default:
			return Fail("invalid_action", 400, "未知模型操作")
		}
		p.Version++
		if e = t.Save(ctx, p, false); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, e
}

// A cancelled browser request still releases its own lease; a bounded cleanup failure
// cannot mask the probe result and the persisted lease expires automatically.
func (s *ModelService) releaseProbe(parent context.Context, workspace, actor, claim string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	e := s.Store.WithWorkspace(ctx, workspace, func(t ModelTx) error { return t.ReleaseProbe(ctx, actor, claim) })
	if e != nil && s.OnReleaseError != nil {
		s.OnReleaseError()
	}
}
