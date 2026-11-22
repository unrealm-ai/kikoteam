// Package channelprobe performs bounded application credential checks only.
// No WebSocket is opened: in particular a WeCom probe must not evict a live bot.
package channelprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"time"
)

type Client struct{ Transport http.RoundTripper }

func (c *Client) Verify(ctx context.Context, provider, id, secret string) error {
	endpoint := ""
	var body any
	switch provider {
	case "feishu":
		endpoint = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
		body = map[string]string{"app_id": id, "app_secret": secret}
	case "dingtalk":
		endpoint = "https://api.dingtalk.com/v1.0/oauth2/accessToken"
		body = map[string]string{"appKey": id, "appSecret": secret}
	default:
		return service.Fail("channel_validation_unsupported", 400, "此渠道不支持无连接凭据验证")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: c.Transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return service.Fail("channel_validation_unavailable", 503, "渠道验证请求未完成，请稍后重试")
	}
	defer func() {
		_ = res.Body.Close() //nolint:errcheck // Closing a consumed HTTP response cannot change the credential verdict.
	}()
	payload, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(payload) > 64<<10 {
		return service.Fail("channel_validation_unavailable", 503, "渠道验证响应无效")
	}
	if res.StatusCode == 429 {
		return service.Fail("channel_validation_rate_limited", 429, "渠道验证过于频繁，请稍后重试")
	}
	if res.StatusCode >= 500 || res.StatusCode >= 300 && res.StatusCode < 400 {
		return service.Fail("channel_validation_unavailable", 503, "渠道验证服务暂不可用")
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(payload, &result) != nil {
		return service.Fail("channel_validation_unavailable", 503, "渠道验证响应无效")
	}
	tokenKey, expiryKey := "accessToken", "expireIn"
	if provider == "feishu" {
		tokenKey = "tenant_access_token"
		expiryKey = "expire"
		var code int
		if json.Unmarshal(result["code"], &code) != nil || code != 0 {
			return service.Fail("channel_credentials_invalid", 400, "渠道凭据验证未通过")
		}
	} else if len(result["code"]) > 0 {
		return service.Fail("channel_credentials_invalid", 400, "渠道凭据验证未通过")
	}
	var token string
	var expires int64
	if res.StatusCode != 200 || json.Unmarshal(result[tokenKey], &token) != nil || token == "" || json.Unmarshal(result[expiryKey], &expires) != nil || expires <= 0 {
		return service.Fail("channel_credentials_invalid", 400, "渠道凭据验证未通过")
	}
	return nil
}
