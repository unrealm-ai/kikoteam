// Package dingtalkdirectory reads a complete, bounded authorized application directory.
package dingtalkdirectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Client reads the directory scope granted to the application credentials.
type Client struct{ HTTPClient *http.Client }
type run struct {
	ctx         context.Context
	client      *http.Client
	appID       string
	token       string
	requests    int
	users       map[string]service.DirectoryUser
	departments map[string]service.DirectoryDepartment
}

func failure(code string, invalid bool) error {
	return &service.DirectoryFetchError{Code: code, Invalidate: invalid}
}
func (c Client) Fetch(ctx context.Context, appID, secret string) (service.DirectorySnapshot, error) {
	empty := service.DirectorySnapshot{}
	if !validID(appID) || secret == "" {
		return empty, failure("directory_credentials_invalid", true)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	h := http.Client{Timeout: 20 * time.Second}
	if c.HTTPClient != nil {
		h = *c.HTTPClient
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r := run{appID: appID, ctx: ctx, client: &h, users: map[string]service.DirectoryUser{}, departments: map[string]service.DirectoryDepartment{}}
	var auth struct {
		Token   string          `json:"accessToken"`
		Expires int64           `json:"expireIn"`
		Code    json.RawMessage `json:"code"`
	}
	if err := r.request("https://api.dingtalk.com/v1.0/oauth2/accessToken", map[string]string{"appKey": appID, "appSecret": secret}, &auth, true); err != nil {
		return empty, err
	}
	if auth.Token == "" || auth.Expires <= 0 || auth.Expires > 86400 || len(auth.Code) > 0 {
		return empty, failure("directory_authentication_failed", true)
	}
	r.token = auth.Token
	before, err := r.scope()
	if err != nil {
		return empty, err
	}
	for _, id := range before.Users {
		if err = r.user(id); err != nil {
			return empty, err
		}
	}
	for _, id := range before.Departments {
		if err = r.department(id, map[string]bool{}); err != nil {
			return empty, err
		}
	}
	after, err := r.scope()
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(before, after) {
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
func (r *run) request(endpoint string, body any, out any, auth bool) error {
	r.requests++
	if r.requests > 500 {
		return failure("directory_request_budget", false)
	}
	var reader io.Reader
	method := "GET"
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return failure("directory_invalid_request", false)
		}
		reader = bytes.NewReader(b)
		method = "POST"
	}
	req, e := http.NewRequestWithContext(r.ctx, method, endpoint, reader)
	if e != nil {
		return failure("directory_invalid_request", false)
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := r.client.Do(req)
	if e != nil {
		if errors.Is(r.ctx.Err(), context.Canceled) {
			return failure("directory_cancelled", false)
		}
		if errors.Is(r.ctx.Err(), context.DeadlineExceeded) {
			return failure("directory_timeout", false)
		}
		return failure("directory_network", false)
	}
	defer res.Body.Close() //nolint:errcheck // Read-only response cleanup.
	b, e := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if e != nil {
		return failure("directory_network", false)
	}
	if len(b) > 8<<20 {
		return failure("directory_response_budget", false)
	}
	if res.StatusCode == 401 || auth && (res.StatusCode == 400 || res.StatusCode == 403) {
		return failure("directory_authentication_failed", true)
	}
	if res.StatusCode == 403 {
		return failure("directory_authorization_failed", true)
	}
	if res.StatusCode < 200 || (res.StatusCode >= 300 && res.StatusCode != 400) {
		return failure("directory_http_failed", false)
	}
	if !auth {
		var envelope struct {
			Code *int            `json:"errcode"`
			Sub  json.RawMessage `json:"sub_code"`
		}
		if json.Unmarshal(b, &envelope) != nil || envelope.Code == nil {
			return failure("directory_invalid_response", false)
		}
		if *envelope.Code != 0 {
			return directoryBusinessError(*envelope.Code, envelope.Sub)
		}
	}
	if res.StatusCode == 400 {
		return failure("directory_http_failed", false)
	}
	if json.Unmarshal(b, out) != nil {
		return failure("directory_invalid_response", false)
	}
	return nil
}
func directoryBusinessError(code int, sub json.RawMessage) error {
	switch code {
	case 40001, 40014, 42001, 40082, 40083, 40084:
		return failure("directory_authentication_failed", true)
	case 50001, 50002, 60011, 60020, 60021:
		return failure("directory_authorization_failed", true)
	case 88:
		if string(sub) == "60011" || string(sub) == `"60011"` {
			return failure("directory_authorization_failed", true)
		}
	}
	return failure("directory_provider_rejected", false)
}
func (r *run) api(path string, body, out any) error {
	return r.request("https://oapi.dingtalk.com"+path+"?access_token="+url.QueryEscape(r.token), body, out, false)
}
func validID(s string) bool { return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "\x00\r\n") }

type authorizedScope struct{ Users, Departments []string }

func (r *run) scope() (authorizedScope, error) {
	var v struct {
		More  bool `json:"has_more"`
		Scope *struct {
			More        bool            `json:"has_more"`
			Users       *[]string       `json:"authed_user"`
			Departments *[]departmentID `json:"authed_dept"`
		} `json:"auth_org_scopes"`
	}
	if e := r.api("/auth/scopes", nil, &v); e != nil {
		return authorizedScope{}, e
	}
	if v.More || v.Scope == nil || v.Scope.More || v.Scope.Users == nil || v.Scope.Departments == nil {
		return authorizedScope{}, failure("directory_scope_incomplete", true)
	}
	out := authorizedScope{Users: []string{}, Departments: []string{}}
	seen := map[string]bool{}
	for _, id := range *v.Scope.Users {
		if !validID(id) || seen[id] {
			return out, failure("directory_scope_invalid", true)
		}
		seen[id] = true
		out.Users = append(out.Users, id)
	}
	seen = map[string]bool{}
	for _, id := range *v.Scope.Departments {
		if seen[string(id)] {
			return out, failure("directory_scope_invalid", true)
		}
		seen[string(id)] = true
		out.Departments = append(out.Departments, string(id))
	}
	if len(out.Users)+len(out.Departments) > 100000 {
		return out, failure("directory_object_budget", false)
	}
	sort.Strings(out.Users)
	sort.Strings(out.Departments)
	return out, nil
}
