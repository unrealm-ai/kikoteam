// Package wecomcontacts exposes only WeCom bot CLI contact search. Search IDs
// are discovery candidates, not authenticated callback identities or ACL grants.
package wecomcontacts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const origin = "https://qyapi.weixin.qq.com"

type Client struct {
	HTTPClient *http.Client
	Now        func() time.Time
}

func fail(code string) error {
	return service.Fail("wecom_contacts_"+code, 502, "企微联系人查询不可用")
}
func codeError(code int) error {
	switch code {
	case 40001, 40014, 853004:
		return fail("authentication_failed")
	case 48002, 850002, 851008, 853006:
		return fail("authorization_denied")
	default:
		return fail("unavailable")
	}
}
func valid(s string, max int) bool {
	return s != "" && utf8.RuneCountInString(s) <= max && strings.IndexFunc(s, unicode.IsControl) < 0
}
func (c Client) request(ctx context.Context, path string, body any, token string) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h := http.Client{}
	if c.HTTPClient != nil {
		h = *c.HTTPClient
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b, e := json.Marshal(body)
	if e != nil {
		return nil, fail("invalid_request")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", origin+path, bytes.NewReader(b))
	if e != nil {
		return nil, fail("invalid_request")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := h.Do(req)
	if e != nil {
		return nil, fail("unavailable")
	}
	defer res.Body.Close() //nolint:errcheck // Read-only cleanup.
	if res.StatusCode == 401 {
		return nil, fail("authentication_failed")
	}
	if res.StatusCode == 403 {
		return nil, fail("authorization_denied")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fail("unavailable")
	}
	b, e = io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
	if e != nil {
		return nil, fail("unavailable")
	}
	if len(b) > 256*1024 {
		return nil, fail("invalid_response")
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(b, &v) != nil || v == nil {
		return nil, fail("invalid_response")
	}
	var code *int
	if json.Unmarshal(v["errcode"], &code) != nil || code == nil {
		return nil, fail("invalid_response")
	}
	if *code != 0 {
		return nil, codeError(*code)
	}
	return v, nil
}
func nested(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) != nil || len(text) > 256*1024 {
		return nil, fail("invalid_response")
	}
	var v map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &v) != nil || v == nil {
		return nil, fail("invalid_response")
	}
	return v, nil
}
func (c Client) call(ctx context.Context, path string, payload any, token string) (map[string]json.RawMessage, error) {
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, fail("invalid_request")
	}
	v, e := c.request(ctx, path, map[string]string{"payload": string(b)}, token)
	if e != nil {
		return nil, e
	}
	result, e := nested(v["results_json"])
	if e != nil {
		return nil, e
	}
	if raw, ok := result["error"]; ok {
		var er struct {
			Code *int `json:"code"`
		}
		if json.Unmarshal(raw, &er) != nil || er.Code == nil {
			return nil, fail("invalid_response")
		}
		if *er.Code != 0 {
			return nil, codeError(*er.Code)
		}
	}
	return nested(result["result"])
}
func (c Client) connect(ctx context.Context, app, secret string) (string, string, error) {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	var random [8]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", "", fail("unavailable")
	}
	nonce := fmt.Sprintf("cli_%d_%s", now.UnixMilli(), hex.EncodeToString(random[:]))
	signature := sha256.Sum256([]byte(secret + app + fmt.Sprint(now.Unix()) + nonce))
	auth, e := c.request(ctx, "/cgi-bin/aibot/cli/get_cli_config", map[string]any{"bot_id": app, "time": now.Unix(), "nonce": nonce, "signature": hex.EncodeToString(signature[:]), "bind_source": 1}, "")
	if e != nil {
		return "", "", e
	}
	var token string
	if json.Unmarshal(auth["token"], &token) != nil || !valid(token, 8192) || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return "", "", fail("invalid_response")
	}
	schema, e := c.call(ctx, "/cli/service/discovery", map[string]string{"service": "contact"}, token)
	if e != nil {
		return "", "", e
	}
	var resources *struct {
		Users struct {
			Methods struct {
				Search *struct {
					Method string `json:"http_method"`
					Path   string `json:"path"`
				} `json:"search"`
			} `json:"methods"`
		} `json:"users"`
	}
	if json.Unmarshal(schema["resources"], &resources) != nil || resources == nil {
		return "", "", fail("invalid_response")
	}
	if resources.Users.Methods.Search == nil {
		return "", "", fail("unsupported")
	}
	method := resources.Users.Methods.Search
	if method.Method != "POST" || !regexp.MustCompile(`^/[a-zA-Z0-9_/-]+$`).MatchString(method.Path) {
		return "", "", fail("invalid_response")
	}
	base := origin + "/cli"
	if raw, ok := schema["base_url"]; ok {
		if json.Unmarshal(raw, &base) != nil {
			return "", "", fail("invalid_response")
		}
	}
	u, e := url.Parse(strings.TrimRight(base, "/") + method.Path)
	if e != nil || u.Scheme != "https" || u.Host != "qyapi.weixin.qq.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !regexp.MustCompile(`^/(?:cli/)?[a-zA-Z0-9_/-]+$`).MatchString(u.Path) || !regexp.MustCompile(`(?i)(contact|user)`).MatchString(u.Path) || !regexp.MustCompile(`(?i)(search|get_userlist)$`).MatchString(u.Path) {
		return "", "", fail("invalid_response")
	}
	return token, u.Path, nil
}
func (c Client) Search(ctx context.Context, app, secret, q string) (service.WecomContactSearch, error) {
	empty := service.WecomContactSearch{}
	q = strings.TrimSpace(q)
	if !valid(app, 256) || secret == "" || !valid(q, 64) {
		return empty, fail("invalid_request")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		token, path, e := c.connect(ctx, app, secret)
		if e != nil {
			return empty, e
		}
		v, e := c.call(ctx, path, map[string]any{"keywords": []string{q}}, token)
		if e != nil {
			var se *service.Error
			if attempt == 0 && errors.As(e, &se) && se.Code == "wecom_contacts_authentication_failed" {
				continue
			}
			return empty, e
		}
		return parse(v)
	}
	return empty, fail("authentication_failed")
}
func parse(v map[string]json.RawMessage) (service.WecomContactSearch, error) {
	out := service.WecomContactSearch{Items: []service.WecomContact{}}
	raw, ok := v["users"]
	if !ok {
		var count *int
		if json.Unmarshal(v["users_count"], &count) != nil || count == nil || *count != 0 {
			return out, fail("invalid_response")
		}
		raw = json.RawMessage("[]")
	}
	var users []struct {
		ID          string   `json:"userid"`
		Name        string   `json:"name"`
		Departments []string `json:"departments"`
	}
	if string(raw) == "null" || json.Unmarshal(raw, &users) != nil || len(users) > 100 {
		return out, fail("invalid_response")
	}
	seen := map[string]bool{}
	for _, u := range users {
		if !valid(u.ID, 256) || !valid(u.Name, 256) || len(u.Departments) > 32 {
			return service.WecomContactSearch{}, fail("invalid_response")
		}
		for _, d := range u.Departments {
			if len(d) > 512 || strings.IndexFunc(d, unicode.IsControl) >= 0 {
				return service.WecomContactSearch{}, fail("invalid_response")
			}
		}
		if !seen[u.ID] {
			if u.Departments == nil {
				u.Departments = []string{}
			}
			out.Items = append(out.Items, service.WecomContact{SearchUserID: u.ID, DisplayName: u.Name, DepartmentPaths: u.Departments})
			seen[u.ID] = true
		}
	}
	var hint string
	if json.Unmarshal(v["hint"], &hint) == nil {
		out.Limited = hint != ""
	}
	return out, nil
}
