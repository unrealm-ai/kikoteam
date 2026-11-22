// Package dingtalksetup implements the official device registration protocol.
// It requests app registration only, never DWS user authorization.
package dingtalksetup

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct{ HTTPClient *http.Client }

func fail(code string) error {
	return service.Fail("dingtalk_registration_"+code, 502, "钉钉扫码协议暂不可用")
}
func (c Client) post(ctx context.Context, action string, body map[string]string) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h := http.Client{}
	if c.HTTPClient != nil {
		h = *c.HTTPClient
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	data, e := json.Marshal(body)
	if e != nil {
		return nil, fail("invalid_request")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://oapi.dingtalk.com/app/registration/"+action, bytes.NewReader(data))
	if e != nil {
		return nil, fail("invalid_request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := h.Do(req)
	if e != nil {
		return nil, fail("unavailable")
	}
	defer res.Body.Close() //nolint:errcheck // Read-only cleanup.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500 {
			return nil, fail("unavailable")
		}
		return nil, fail("rejected")
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 32769))
	if e != nil {
		return nil, fail("unavailable")
	}
	if len(b) > 32768 {
		return nil, fail("invalid_response")
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(b, &v) != nil || v == nil {
		return nil, fail("invalid_response")
	}
	var code int
	if raw, ok := v["errcode"]; !ok || string(raw) == "null" || json.Unmarshal(raw, &code) != nil || code != 0 {
		return nil, fail("rejected")
	}
	return v, nil
}
func field(v json.RawMessage, max int) (string, error) {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", fail("invalid_response")
	}
	s = strings.TrimSpace(s)
	if s == "" || len(s) > max || strings.ContainsAny(s, "\x00\r\n") {
		return "", fail("invalid_response")
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return "", fail("invalid_response")
		}
	}
	return s, nil
}
func duration(v json.RawMessage, def, max int) (int, error) {
	if len(v) == 0 {
		return def, nil
	}
	var n float64
	if json.Unmarshal(v, &n) != nil || n <= 0 {
		return 0, fail("invalid_response")
	}
	if n > float64(max) {
		return max, nil
	}
	out := int(n)
	if float64(out) < n {
		out++
	}
	return out, nil
}
func verification(raw json.RawMessage, secrets ...string) (string, error) {
	s, e := field(raw, 4096)
	if e != nil {
		return "", e
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || (u.Host != "dingtalk.com" && !strings.HasSuffix(u.Host, ".dingtalk.com")) {
		return "", fail("invalid_response")
	}
	decoded := u.String()
	for i := 0; i < 5; i++ {
		for _, secret := range secrets {
			if strings.Contains(decoded, secret) || strings.Contains(decoded, url.QueryEscape(secret)) {
				return "", fail("invalid_response")
			}
		}
		next, e := url.PathUnescape(decoded)
		if e != nil {
			return "", fail("invalid_response")
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	return u.String(), nil
}
func (c Client) Begin(ctx context.Context, source string) (service.DingTalkBegin, error) {
	var out service.DingTalkBegin
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,63}$`).MatchString(source) || strings.EqualFold(source, "DING_DWS_CLAW") {
		return out, fail("source_invalid")
	}
	init, e := c.post(ctx, "init", map[string]string{"source": source})
	if e != nil {
		return out, e
	}
	nonce, e := field(init["nonce"], 8192)
	if e != nil {
		return out, e
	}
	v, e := c.post(ctx, "begin", map[string]string{"nonce": nonce})
	if e != nil {
		return out, e
	}
	out.DeviceCode, e = field(v["device_code"], 8192)
	if e != nil {
		return out, e
	}
	out.URL, e = verification(v["verification_uri_complete"], nonce, out.DeviceCode)
	if e != nil {
		return out, e
	}
	out.ExpiresIn, e = duration(v["expires_in"], 600, 600)
	if e != nil {
		return out, e
	}
	out.Interval, e = duration(v["interval"], 3, 60)
	return out, e
}
func (c Client) Poll(ctx context.Context, device string) (service.DingTalkPoll, error) {
	out := service.DingTalkPoll{}
	if device == "" || len(device) > 8192 {
		return out, fail("invalid_request")
	}
	v, e := c.post(ctx, "poll", map[string]string{"device_code": device})
	if e != nil {
		return out, e
	}
	var status string
	if raw, ok := v["status"]; ok {
		if json.Unmarshal(raw, &status) != nil {
			return out, fail("invalid_response")
		}
	}
	out.State = strings.ToUpper(strings.TrimSpace(status))
	switch out.State {
	case "WAITING", "FAIL", "EXPIRED":
		return out, nil
	case "SUCCESS":
		out.AppID, e = field(v["client_id"], 256)
		if e != nil {
			return out, e
		}
		out.Secret, e = field(v["client_secret"], 8192)
		return out, e
	default:
		out.State = "UNKNOWN"
		return out, nil
	}
}
