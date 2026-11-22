// Package feishugroups reads one bounded page of groups joined by this app.
// It never creates platform conversations or grants access to a returned group.
package feishugroups

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const origin = "https://open.feishu.cn"
const maxBody = 2 << 20

type Client struct{ HTTPClient *http.Client }
type requestClient struct {
	ctx   context.Context
	http  *http.Client
	token string
}

func fail(code string) error { return service.Fail(code, 503, "飞书群列表暂不可用") }
func (c Client) List(ctx context.Context, appID, secret, tenantID, cursor string) (service.ChannelGroupPage, error) {
	empty := service.ChannelGroupPage{Items: []service.ChannelGroupItem{}}
	if appID == "" || secret == "" || tenantID == "" || len(cursor) > 4096 || strings.ContainsAny(cursor, "\x00\r\n") {
		return empty, fail("group_identity_invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	hc := http.Client{Timeout: 10 * time.Second}
	if c.HTTPClient != nil {
		hc = *c.HTTPClient
	}
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	r := requestClient{ctx: ctx, http: &hc}
	raw, e := r.call("POST", "/open-apis/auth/v3/tenant_access_token/internal", nil, map[string]string{"app_id": appID, "app_secret": secret})
	if e != nil {
		return empty, e
	}
	var token struct {
		Token string `json:"tenant_access_token"`
	}
	if json.Unmarshal(raw, &token) != nil || token.Token == "" {
		return empty, fail("group_authentication_failed")
	}
	r.token = token.Token
	raw, e = r.call("GET", "/open-apis/tenant/v2/tenant/query", nil, nil)
	if e != nil {
		return empty, e
	}
	var tenant struct {
		Tenant struct {
			Key string `json:"tenant_key"`
		} `json:"tenant"`
	}
	if json.Unmarshal(raw, &tenant) != nil || tenant.Tenant.Key != tenantID {
		return empty, fail("group_tenant_mismatch")
	}
	q := url.Values{"page_size": {"100"}, "user_id_type": {"open_id"}}
	if cursor != "" {
		q.Set("page_token", cursor)
	}
	raw, e = r.call("GET", "/open-apis/im/v1/chats", q, nil)
	if e != nil {
		return empty, e
	}
	var page struct {
		Items *[]struct {
			ID     string `json:"chat_id"`
			Name   string `json:"name"`
			Tenant string `json:"tenant_key"`
		} `json:"items"`
		More   *bool  `json:"has_more"`
		Cursor string `json:"page_token"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Items == nil || page.More == nil || len(*page.Items) > 100 {
		return empty, fail("group_response_invalid")
	}
	if *page.More && (page.Cursor == "" || page.Cursor == cursor || len(page.Cursor) > 4096 || strings.ContainsAny(page.Cursor, "\x00\r\n")) {
		return empty, fail("group_cursor_invalid")
	}
	result := service.ChannelGroupPage{Items: []service.ChannelGroupItem{}}
	seen := map[string]bool{}
	for _, g := range *page.Items {
		if !validGroup(g.ID, g.Name) || seen[g.ID] {
			return empty, fail("group_response_invalid")
		}
		if g.Tenant != "" && g.Tenant != tenantID {
			return empty, fail("group_tenant_mismatch")
		}
		seen[g.ID] = true
		name := strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, g.Name)
		if len([]rune(name)) > 200 {
			name = string([]rune(name)[:200])
		}
		result.Items = append(result.Items, service.ChannelGroupItem{ID: g.ID, Name: name})
	}
	if *page.More {
		result.NextCursor = page.Cursor
	}
	return result, nil
}
func (r *requestClient) call(method, path string, q url.Values, body any) (json.RawMessage, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, fail("group_request_invalid")
		}
	}
	target := origin + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(r.ctx, method, target, bytes.NewReader(data))
	if err != nil {
		return nil, fail("group_request_invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	res, err := r.http.Do(req)
	if err != nil {
		return nil, fail("group_network_unavailable")
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Preserve bounded read result.
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return nil, fail("group_response_budget")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fail("group_provider_unavailable")
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
		return nil, fail("group_response_invalid")
	}
	if *envelope.Code != 0 {
		return nil, fail("group_access_denied")
	}
	if method == "POST" {
		return raw, nil
	}
	if len(envelope.Data) == 0 {
		return nil, fail("group_response_invalid")
	}
	return envelope.Data, nil
}

func validGroup(id, name string) bool {
	return id != "" && len(id) <= 512 && !strings.ContainsAny(id, "\x00\r\n") && utf8.ValidString(id) && len(name) <= 4096 && utf8.ValidString(name)
}
