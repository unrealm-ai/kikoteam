package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	existing "github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	upstream "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/integration"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Models struct {
	Store             repo.Store
	Secrets           Secrets
	Upstream          upstream.Upstream
	AllowLoopbackHTTP bool
}

func (s Models) candidate(ctx context.Context, actor, id string, p proxy.ModelPatch) (old, m proxy.Model, key string, e error) {
	if e = repo.Admin(ctx, s.Store.DB, actor, false); e != nil {
		return
	}
	if id != "" {
		old, e = repo.Model(ctx, s.Store.DB, id, false)
		if e != nil {
			return
		}
		if p.Version != old.Version {
			e = platform.Fail("model_version_conflict", 409, "模型已更新，请刷新")
			return
		}
	}
	m = old
	for _, field := range []struct {
		target *string
		input  *string
	}{{&m.Name, p.Name}, {&m.Protocol, p.Protocol}, {&m.BaseURL, p.BaseURL}, {&m.UpstreamID, p.UpstreamID}} {
		if field.input != nil {
			*field.target = strings.TrimSpace(*field.input)
		}
	}
	if p.Prices != nil {
		m.Prices = *p.Prices
	}
	if p.Enabled != nil {
		m.Enabled = *p.Enabled
	}
	if p.Default != nil {
		m.Default = *p.Default
	}
	if p.Enabled != nil && !*p.Enabled && p.Default == nil {
		m.Default = false
	}
	m.BaseURL, e = existing.NormalizeModelEndpoint(m.Protocol, m.BaseURL)
	if e != nil || m.Name == "" || m.UpstreamID == "" || utf8.RuneCountInString(m.Name) > 100 || len(m.UpstreamID) > 255 || strings.ContainsRune(m.Name+m.UpstreamID, '\x00') {
		e = platform.Fail("invalid_model", 400, "模型名称、协议、型号或地址无效")
		return
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || !s.allowedURL(u) {
		e = platform.Fail("invalid_model_url", 400, "模型地址必须使用 HTTPS")
		return
	}
	if p.APIKey != nil {
		key = *p.APIKey
		if len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
			e = platform.Fail("invalid_model_key", 400, "供应商凭据无效")
		}
	} else if old.BaseURL != "" && old.BaseURL != m.BaseURL {
		e = platform.Fail("model_key_required", 400, "更换地址时请重新填写凭据或显式选择无需认证")
	} else {
		key, e = s.Secrets.Open("provider", id, old.Credential)
	}
	return
}
func (s Models) allowedURL(u *url.URL) bool {
	return u.Scheme == "https" || s.AllowLoopbackHTTP && u.Scheme == "http" && loopback(u.Hostname())
}

func loopback(host string) bool { return host == "localhost" || net.ParseIP(host).IsLoopback() }

func (s Models) signature(actor, id string, m proxy.Model, key, stamp string) string {
	mac := hmac.New(sha256.New, s.Secrets.Root)
	for _, v := range []string{"model-proxy-test-v1", actor, id, strconv.FormatInt(m.Version, 10), m.Protocol, m.BaseURL, m.UpstreamID, key, stamp} {
		// Length prefixes keep fields distinct even when they contain delimiters.
		mac.Write(strconv.AppendInt(nil, int64(len(v)), 10))
		mac.Write([]byte{':'})
		mac.Write([]byte(v))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s Models) Test(ctx context.Context, actor, id string, p proxy.ModelPatch) (map[string]any, error) {
	_, m, key, e := s.candidate(ctx, actor, id, p)
	if e != nil {
		return nil, e
	}
	call, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, e := s.Upstream.Request(call, m, key, upstream.ProbeBody(m.Protocol), nil)
	if e != nil {
		return map[string]any{"connected": false, "usage_available": false}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close() //nolint:errcheck // The upstream status is the probe outcome.
		return map[string]any{"connected": false, "usage_available": false, "http_status": resp.StatusCode}, nil
	}
	stream := strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	usage, e := upstream.Read(resp, m.Protocol, func([]byte) {})
	if !stream || e != nil || usage == nil || usage.Output <= 0 {
		return map[string]any{"connected": true, "usage_available": false}, nil
	}
	if e = repo.Admin(ctx, s.Store.DB, actor, false); e != nil {
		return nil, e
	}
	if id != "" {
		current, e := repo.Model(ctx, s.Store.DB, id, false)
		if e != nil {
			return nil, e
		}
		if current.Version != m.Version {
			return nil, platform.Fail("model_version_conflict", 409, "模型已更新，请刷新")
		}
	}
	now := time.Now().UTC()
	stamp := strconv.FormatInt(now.Unix(), 10)
	return map[string]any{"connected": true, "usage_available": true, "receipt": stamp + "." + s.signature(actor, id, m, key, stamp), "expires_at": now.Add(10 * time.Minute)}, nil
}

func (s Models) Save(ctx context.Context, actor, id string, p proxy.ModelPatch) (proxy.Model, error) {
	old, m, key, e := s.candidate(ctx, actor, id, p)
	if e != nil {
		return m, e
	}
	if _, e = m.Prices.Values(); e != nil {
		return m, platform.Fail("invalid_prices", 400, "价格须为非负数，最多六位小数")
	}
	changed := id == "" || old.Protocol != m.Protocol || old.UpstreamID != m.UpstreamID || old.BaseURL != m.BaseURL || p.APIKey != nil
	if changed {
		m.Validated, m.TestedAt = false, nil
	}
	if p.Receipt != "" {
		stamp, sig, ok := strings.Cut(p.Receipt, ".")
		v, err := strconv.ParseInt(stamp, 10, 64)
		tested := time.Unix(v, 0).UTC()
		if !ok || err != nil || time.Since(tested) < 0 || time.Since(tested) > 10*time.Minute || !hmac.Equal([]byte(sig), []byte(s.signature(actor, id, m, key, stamp))) {
			return m, platform.Fail("model_test_required", 409, "测试凭证过期或配置已修改，请重新测试")
		}
		m.Validated, m.TestedAt = true, &tested
	}
	if m.Enabled && !m.Validated || m.Default && !m.Enabled {
		return m, platform.Fail("model_test_required", 409, "请验证并启用模型后再设为默认")
	}
	if id == "" {
		m.ID = platform.NewID()
	}
	if changed {
		m.Credential, e = s.Secrets.Seal("provider", m.ID, key)
		if e != nil {
			return m, e
		}
	}
	return s.Store.SaveModel(ctx, actor, m, id == "", old.Protocol != m.Protocol || old.UpstreamID != m.UpstreamID)
}

func ValidReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 300 && !strings.ContainsRune(reason, '\x00')
}

func ValidUsage(raw json.RawMessage) (*proxy.Usage, error) {
	var u struct {
		Input      *int64 `json:"input_tokens"`
		Output     *int64 `json:"output_tokens"`
		CacheRead  *int64 `json:"cache_read_tokens"`
		CacheWrite *int64 `json:"cache_write_tokens"`
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&u) != nil || u.Input == nil || u.Output == nil || u.CacheRead == nil || u.CacheWrite == nil || *u.Input < 0 || *u.Output < 0 || *u.CacheRead < 0 || *u.CacheWrite < 0 {
		return nil, errors.New("invalid usage")
	}
	return &proxy.Usage{Input: *u.Input, Output: *u.Output, CacheRead: *u.CacheRead, CacheWrite: *u.CacheWrite}, nil
}
