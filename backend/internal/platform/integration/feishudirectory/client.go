// Package feishudirectory reads a bounded, complete application directory.
// It never publishes partial data or accepts a caller-selected remote origin.
package feishudirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishuapi"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const origin = "https://open.feishu.cn"
const maxBody = 8 << 20
const pageSize = "50"

type Client struct{ HTTPClient *http.Client }
type run struct {
	ctx         context.Context
	client      *http.Client
	token       string
	requests    int
	users       map[string]service.DirectoryUser
	departments map[string]service.DirectoryDepartment
}

func failure(code string, invalidate bool) error {
	return &service.DirectoryFetchError{Code: code, Invalidate: invalidate}
}

// ProbeScope checks access to the first scope page using the same request and
// error classification as Fetch. It neither enumerates contacts nor syncs a snapshot.
func (c Client) ProbeScope(ctx context.Context, token string) error {
	client := http.Client{Timeout: 15 * time.Second}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	r := run{ctx: ctx, client: &client, token: token}
	q := params()
	q.Set("page_size", pageSize)
	var page scopePage
	if err := r.get("/open-apis/contact/v3/scopes", q, &page); err != nil {
		return err
	}
	if _, err := ids(page.Users); err != nil {
		return err
	}
	if _, err := ids(page.Departments); err != nil {
		return err
	}
	return page.validate()
}

// Fetch reuses a tenant token only within this sync; all identities are open IDs.
func (c Client) Fetch(ctx context.Context, appID, secret, tenantID string) (service.DirectorySnapshot, error) {
	empty := service.DirectorySnapshot{}
	if appID == "" || secret == "" || tenantID == "" {
		return empty, failure("directory_identity_missing", true)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	r := run{ctx: ctx, client: &client, users: map[string]service.DirectoryUser{}, departments: map[string]service.DirectoryDepartment{}}
	var auth struct {
		Token string `json:"tenant_access_token"`
	}
	if err := r.request("POST", "/open-apis/auth/v3/tenant_access_token/internal", nil, map[string]string{"app_id": appID, "app_secret": secret}, &auth); err != nil {
		return empty, err
	}
	if auth.Token == "" {
		return empty, failure("directory_authentication_failed", true)
	}
	r.token = auth.Token
	var tenant struct {
		Tenant struct {
			Key string `json:"tenant_key"`
		} `json:"tenant"`
	}
	if err := r.get("/open-apis/tenant/v2/tenant/query", nil, &tenant); err != nil {
		return empty, err
	}
	if tenant.Tenant.Key != tenantID {
		return empty, failure("directory_tenant_mismatch", true)
	}
	scope, err := r.scope()
	if err != nil {
		return empty, err
	}
	for _, id := range scope.Users {
		if err = r.user(id); err != nil {
			return empty, err
		}
	}
	for _, id := range scope.Departments {
		if err = r.department(id, map[string]bool{}); err != nil {
			return empty, err
		}
	}
	final, err := r.scope()
	if err != nil {
		return empty, err
	}
	if strings.Join(scope.Users, "\x00") != strings.Join(final.Users, "\x00") || strings.Join(scope.Departments, "\x00") != strings.Join(final.Departments, "\x00") {
		return empty, failure("directory_scope_changed", true)
	}
	out := service.DirectorySnapshot{Users: []service.DirectoryUser{}, Departments: []service.DirectoryDepartment{}}
	for _, u := range r.users {
		out.Users = append(out.Users, u)
	}
	for _, d := range r.departments {
		out.Departments = append(out.Departments, d)
	}
	sort.Slice(out.Users, func(i, j int) bool { return out.Users[i].ID < out.Users[j].ID })
	sort.Slice(out.Departments, func(i, j int) bool { return out.Departments[i].ID < out.Departments[j].ID })
	return out, nil
}
func (r *run) get(path string, q url.Values, out any) error {
	return r.request("GET", path, q, nil, out)
}
func (r *run) request(method, path string, q url.Values, body any, out any) error {
	r.requests++
	if r.requests > 500 {
		return failure("directory_request_budget", false)
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return failure("directory_request_invalid", false)
		}
	}
	target := origin + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(r.ctx, method, target, bytes.NewReader(data))
	if err != nil {
		return failure("directory_request_invalid", false)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	res, err := r.client.Do(req)
	if err != nil {
		if errors.Is(r.ctx.Err(), context.Canceled) {
			return failure("directory_cancelled", false)
		}
		if errors.Is(r.ctx.Err(), context.DeadlineExceeded) {
			return failure("directory_timeout", false)
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return failure("directory_timeout", false)
		}
		return failure("directory_network", false)
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Preserve read/parse error; no body or credentials are returned.
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return failure("directory_network", false)
	}
	if len(raw) > maxBody {
		return failure("directory_response_budget", false)
	}
	var env struct {
		Code *int            `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	parseErr := json.Unmarshal(raw, &env)
	providerFailure := func(code string, invalidate bool) error {
		e := &service.DirectoryFetchError{Code: code, Invalidate: invalidate}
		if method == "GET" && parseErr == nil && env.Code != nil {
			e.Detail = feishuapi.ErrorDetail(*env.Code, env.Msg, r.token)
		}
		return e
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return providerFailure("directory_access_denied", true)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return providerFailure("directory_provider_unavailable", false)
	}
	if parseErr != nil || env.Code == nil {
		return failure("directory_response_invalid", false)
	}
	if *env.Code != 0 {
		if method == "POST" {
			return failure("directory_authentication_failed", true)
		}
		switch *env.Code {
		case 99991661, 99991663, 99991664, 99991665, 99991668, 99991671, 99991672:
			return providerFailure("directory_access_denied", true)
		}
		return providerFailure("directory_provider_rejected", false)
	}
	if method == "POST" {
		if json.Unmarshal(raw, out) != nil {
			return failure("directory_response_invalid", false)
		}
		return nil
	}
	if len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) || json.Unmarshal(env.Data, out) != nil {
		return failure("directory_response_invalid", false)
	}
	return nil
}
func params() url.Values {
	return url.Values{"user_id_type": {"open_id"}, "department_id_type": {"open_department_id"}}
}
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 256 && !strings.ContainsAny(id, "\x00\r\n/\\")
}
func ids(values []string) ([]string, error) {
	out := append([]string{}, values...)
	seen := map[string]bool{}
	for _, v := range out {
		if !validID(v) || seen[v] {
			return nil, failure("directory_identity_invalid", false)
		}
		seen[v] = true
	}
	sort.Strings(out)
	return out, nil
}

type scope struct{ Users, Departments []string }

type scopeIDs []string

func (s *scopeIDs) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return errors.New("expected a scope ID list")
	}
	return json.Unmarshal(data, (*[]string)(s))
}

type scopePage struct {
	Users       scopeIDs `json:"user_ids"`
	Departments scopeIDs `json:"department_ids"`
	Groups      []string `json:"group_ids"`
}

func (p scopePage) validate() error {
	if len(p.Groups) > 0 {
		return failure("directory_group_scope_unsupported", true)
	}
	// Feishu omits empty categories on user-only and department-only pages.
	// An entirely empty scope still needs explicit evidence of both categories.
	if len(p.Users)+len(p.Departments) == 0 && (p.Users == nil || p.Departments == nil) {
		return failure("directory_scope_undisclosed", true)
	}
	return nil
}

func (r *run) scope() (scope, error) {
	out := scope{}
	seenUsers, seenDepts := map[string]bool{}, map[string]bool{}
	err := r.pages("/open-apis/contact/v3/scopes", params(), func(raw json.RawMessage) error {
		var page scopePage
		if json.Unmarshal(raw, &page) != nil {
			return failure("directory_scope_invalid", true)
		}
		if err := page.validate(); err != nil {
			return err
		}
		for _, v := range page.Users {
			if !validID(v) || seenUsers[v] {
				return failure("directory_scope_invalid", true)
			}
			seenUsers[v] = true
			out.Users = append(out.Users, v)
		}
		for _, v := range page.Departments {
			if !validID(v) || seenDepts[v] {
				return failure("directory_scope_invalid", true)
			}
			seenDepts[v] = true
			out.Departments = append(out.Departments, v)
		}
		return nil
	})
	sort.Strings(out.Users)
	sort.Strings(out.Departments)
	return out, err
}
func (r *run) pages(path string, q url.Values, consume func(json.RawMessage) error) error {
	q.Set("page_size", pageSize)
	seen := map[string]bool{}
	for page := 0; page < 500; page++ {
		var raw json.RawMessage
		if err := r.get(path, q, &raw); err != nil {
			return err
		}
		var p struct {
			More  *bool  `json:"has_more"`
			Token string `json:"page_token"`
		}
		if json.Unmarshal(raw, &p) != nil || p.More == nil {
			return failure("directory_pagination_invalid", false)
		}
		if err := consume(raw); err != nil {
			return err
		}
		if !*p.More {
			return nil
		}
		if p.Token == "" || seen[p.Token] {
			return failure("directory_cursor_invalid", false)
		}
		seen[p.Token] = true
		q.Set("page_token", p.Token)
	}
	return failure("directory_request_budget", false)
}
