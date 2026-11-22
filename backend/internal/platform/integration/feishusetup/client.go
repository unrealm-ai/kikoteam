// Package feishusetup integrates the official device registration protocol and
// read-only application diagnostics. It never creates an app without a Begin call.
package feishusetup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const accounts = "https://accounts.feishu.cn/oauth/v1/app/registration"
const api = "https://open.feishu.cn/open-apis"

type Client struct{ Transport http.RoundTripper }

func failure(code string) error {
	return service.Fail(code, 502, "飞书连接暂不可用，请查看连接诊断")
}
func (c *Client) request(ctx context.Context, method, address, contentType, token string, body []byte) (map[string]json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Transport: c.Transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, failure("feishu_transport_unavailable")
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // Read result is checked.
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, failure("feishu_invalid_response")
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(data, &out) != nil || out == nil {
		return nil, failure("feishu_invalid_response")
	}
	if resp.StatusCode >= 500 {
		return out, failure("feishu_provider_unavailable")
	}
	if resp.StatusCode == http.StatusForbidden && method == "PATCH" && strings.HasSuffix(address, "/contacts_range") {
		return nil, failure("feishu_authorization_denied")
	}
	if resp.StatusCode >= 300 && (address != accounts || resp.StatusCode != 400) {
		return out, failure("feishu_request_denied")
	}
	return out, nil
}
func str(m map[string]json.RawMessage, k string) string {
	var v string
	if json.Unmarshal(m[k], &v) != nil {
		return ""
	}
	return v
}
func number(m map[string]json.RawMessage, k string) int {
	var v int
	if json.Unmarshal(m[k], &v) != nil {
		return -1
	}
	return v
}
func object(m map[string]json.RawMessage, k string) map[string]json.RawMessage {
	var v map[string]json.RawMessage
	if json.Unmarshal(m[k], &v) != nil {
		return nil
	}
	return v
}
func (c *Client) registration(ctx context.Context, values url.Values) (map[string]json.RawMessage, error) {
	return c.request(ctx, "POST", accounts, "application/x-www-form-urlencoded", "", []byte(values.Encode()))
}
func (c *Client) Begin(ctx context.Context, name, description string) (service.FeishuBegin, error) {
	return c.BeginWithPermissions(ctx, name, description, service.FeishuPermissions())
}

func (c *Client) BeginWithPermissions(ctx context.Context, name, description string, permissions []service.FeishuPermission) (service.FeishuBegin, error) {
	var out service.FeishuBegin
	m, err := c.registration(ctx, url.Values{"action": {"begin"}, "archetype": {"PersonalAgent"}, "auth_method": {"client_secret"}, "request_user_info": {"open_id"}})
	if err != nil {
		return out, err
	}
	out = service.FeishuBegin{DeviceCode: str(m, "device_code"), URL: str(m, "verification_uri_complete"), ExpiresIn: number(m, "expires_in"), Interval: number(m, "interval")}
	u, err := url.Parse(out.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Hostname() != "accounts.feishu.cn" && u.Hostname() != "open.feishu.cn") || out.DeviceCode == "" || len(out.DeviceCode) > 8192 || out.ExpiresIn < 1 || out.Interval < 1 || out.Interval > 60 {
		return out, failure("feishu_invalid_registration")
	}
	// The provider may issue a longer-lived code; retain our local ten-minute window.
	if out.ExpiresIn > 600 {
		out.ExpiresIn = 600
	}
	addons, err := encodeAddons(permissions)
	if err != nil {
		return out, err
	}
	q := u.Query()
	q.Set("from", "sdk")
	q.Set("tp", "sdk")
	q.Set("source", "kikoteam")
	q.Set("createOnly", "true")
	q.Set("name", name)
	q.Set("desc", description)
	q.Set("addons", addons)
	u.RawQuery = q.Encode()
	out.URL = u.String()
	return out, nil
}
func encodeAddons(permissions []service.FeishuPermission) (string, error) {
	scopes := []string{}
	for _, p := range permissions {
		scopes = append(scopes, p.Scope)
	}
	data, err := json.Marshal(map[string]any{"preset": false, "scopes": map[string]any{"tenant": scopes}, "events": map[string]any{"items": map[string]any{"tenant": []string{"im.message.receive_v1", "im.chat.member.bot.added_v1", "im.chat.member.bot.deleted_v1", "im.chat.disbanded_v1"}}}})
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err = z.Write(data); err != nil {
		return "", err
	}
	if err = z.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b.Bytes()), nil
}

var appID = regexp.MustCompile(`^cli_[A-Za-z0-9_-]{1,120}$`)

func (c *Client) Poll(ctx context.Context, device string) (service.FeishuPoll, error) {
	m, err := c.registration(ctx, url.Values{"action": {"poll"}, "device_code": {device}})
	if err != nil {
		return service.FeishuPoll{}, err
	}
	if str(object(m, "user_info"), "tenant_brand") == "lark" {
		return service.FeishuPoll{State: "unsupported"}, nil
	}
	state := str(m, "error")
	switch state {
	case "authorization_pending", "slow_down", "access_denied", "expired_token":
		return service.FeishuPoll{State: state}, nil
	}
	id, secret := str(m, "client_id"), str(m, "client_secret")
	if state != "" || !appID.MatchString(id) || len(secret) < 1 || len(secret) > 4096 || strings.ContainsAny(secret, "\r\n\x00") {
		return service.FeishuPoll{}, failure("feishu_invalid_registration")
	}
	return service.FeishuPoll{State: "credentials_saved", AppID: id, Secret: secret}, nil
}

func success(m map[string]json.RawMessage) bool {
	var code int
	return string(m["code"]) != "null" && json.Unmarshal(m["code"], &code) == nil && code == 0
}
