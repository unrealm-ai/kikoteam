package dingtalkdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: http.Header{}}
}
func fixture(t *testing.T, change func(*http.Request, string) string) Client {
	t.Helper()
	return Client{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || (r.URL.Host != "api.dingtalk.com" && r.URL.Host != "oapi.dingtalk.com") {
			t.Fatalf("unexpected origin %s", r.URL.Host)
		}
		path := r.URL.Path
		var s string
		switch path {
		case "/v1.0/oauth2/accessToken":
			s = `{"accessToken":"test-only-token","expireIn":7200}`
			if r.Method != "POST" {
				t.Fatal("token method")
			}
		case "/auth/scopes":
			s = `{"errcode":0,"auth_org_scopes":{"authed_user":["user"],"authed_dept":[1]}}`
		case "/topapi/v2/user/get":
			s = `{"errcode":0,"result":{"userid":"user","name":"Test","active":true,"dept_id_list":[1]}}`
		case "/topapi/v2/department/get":
			s = `{"errcode":0,"result":{"dept_id":1,"name":"Root","parent_id":0}}`
		case "/topapi/v2/user/list":
			s = `{"errcode":0,"result":{"list":[{"userid":"user","name":"Test","active":true,"dept_id_list":[1]}],"has_more":false}}`
		case "/topapi/v2/department/listsub":
			s = `{"errcode":0,"result":[]}`
		default:
			t.Fatalf("unexpected path %s", path)
		}
		if path != "/v1.0/oauth2/accessToken" && r.URL.Query().Get("access_token") != "test-only-token" {
			t.Fatal("missing scoped token")
		}
		if change != nil {
			s = change(r, s)
		}
		return response(s), nil
	})}}
}
func TestCompleteSnapshot(t *testing.T) {
	scopes := 0
	c := fixture(t, func(r *http.Request, s string) string {
		if r.URL.Path == "/auth/scopes" {
			scopes++
		}
		return s
	})
	v, e := c.Fetch(context.Background(), "app", "secret")
	if e != nil || len(v.Users) != 1 || len(v.Departments) != 1 || !v.Users[0].MembershipsKnown || v.Departments[0].ParentID != "" || scopes != 2 {
		t.Fatalf("%+v %v scopes=%d", v, e, scopes)
	}
}
func TestInvalidResponses(t *testing.T) {
	cases := []struct {
		name, path, body string
		invalid          bool
	}{
		{"auth", "/v1.0/oauth2/accessToken", `{"code":"invalid","accessToken":"x","expireIn":10}`, true},
		{"scope omitted", "/auth/scopes", `{"errcode":0,"auth_org_scopes":{"authed_dept":[]}}`, true},
		{"partial scope", "/auth/scopes", `{"errcode":0,"has_more":true,"auth_org_scopes":{"authed_user":[],"authed_dept":[]}}`, true},
		{"denied", "/auth/scopes", `{"errcode":60011}`, true},
		{"scope duplicate", "/auth/scopes", `{"errcode":0,"auth_org_scopes":{"authed_user":["x","x"],"authed_dept":[]}}`, true},
		{"user mismatch", "/topapi/v2/user/get", `{"errcode":0,"result":{"userid":"other","name":"x"}}`, false},
		{"unsafe dept", "/topapi/v2/department/get", `{"errcode":0,"result":{"dept_id":9007199254740992,"name":"root","parent_id":0}}`, false},
		{"null children", "/topapi/v2/department/listsub", `{"errcode":0,"result":null}`, false},
		{"missing more", "/topapi/v2/user/list", `{"errcode":0,"result":{"list":[]}}`, false},
		{"loop", "/topapi/v2/user/list", `{"errcode":0,"result":{"list":[],"has_more":true,"next_cursor":0}}`, false},
		{"membership conflict", "/topapi/v2/user/list", `{"errcode":0,"result":{"list":[{"userid":"user","name":"Test","active":true,"dept_id_list":[2]}],"has_more":false}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(r *http.Request, s string) string {
				if r.URL.Path == tc.path {
					return tc.body
				}
				return s
			})
			v, e := c.Fetch(context.Background(), "app", "secret")
			var f *service.DirectoryFetchError
			if !errors.As(e, &f) || f.Invalidate != tc.invalid || len(v.Users) > 0 || len(v.Departments) > 0 {
				t.Fatalf("%+v %v", v, e)
			}
		})
	}
}
func TestScopeChangeAndActivationUnknown(t *testing.T) {
	calls := 0
	c := fixture(t, func(r *http.Request, s string) string {
		if r.URL.Path == "/auth/scopes" {
			calls++
			if calls == 2 {
				return `{"errcode":0,"auth_org_scopes":{"authed_user":[],"authed_dept":[1]}}`
			}
		}
		return s
	})
	_, e := c.Fetch(context.Background(), "app", "s")
	var f *service.DirectoryFetchError
	if !errors.As(e, &f) || !f.Invalidate {
		t.Fatalf("%v", e)
	}
	c = fixture(t, func(_ *http.Request, s string) string {
		return strings.ReplaceAll(s, `"active":true`, `"active":false`)
	})
	v, e := c.Fetch(context.Background(), "app", "s")
	if e != nil || v.Users[0].Status != "unknown" {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestBudgetsAndTransport(t *testing.T) {
	for _, mode := range []string{"network", "redirect", "oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			calls := 0
			c := Client{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "network":
					return nil, errors.New("secret should not escape")
				case "redirect":
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.invalid"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				case "oversize":
					return response(strings.Repeat("x", (8<<20)+1)), nil
				default:
					return nil, r.Context().Err()
				}
			})}}
			_, e := c.Fetch(ctx, "a", "s")
			var f *service.DirectoryFetchError
			if !errors.As(e, &f) || f.Invalidate || calls != 1 || strings.Contains(e.Error(), "secret") {
				t.Fatalf("calls=%d %v", calls, e)
			}
		})
	}
}
func TestRecursivePagination(t *testing.T) {
	c := fixture(t, func(r *http.Request, s string) string {
		if r.Method != "POST" || r.URL.Path == "/v1.0/oauth2/accessToken" {
			return s
		}
		var b map[string]any
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Fatal(e)
		}
		switch r.URL.Path {
		case "/topapi/v2/department/listsub":
			if b["dept_id"] == float64(1) {
				return `{"errcode":0,"result":[{"dept_id":2,"name":"Child","parent_id":1}]}`
			}
		case "/topapi/v2/department/get":
			if b["dept_id"] == float64(2) {
				return `{"errcode":0,"result":{"dept_id":2,"name":"Child","parent_id":1}}`
			}
		case "/topapi/v2/user/list":
			if b["dept_id"] == float64(2) {
				if b["cursor"] == float64(0) {
					return `{"errcode":0,"result":{"list":[],"has_more":true,"next_cursor":1}}`
				}
				return `{"errcode":0,"result":{"list":[{"userid":"second","name":"Second"}],"has_more":false}}`
			}
		}
		return s
	})
	v, e := c.Fetch(context.Background(), "a", "s")
	if e != nil || len(v.Users) != 2 || len(v.Departments) != 2 {
		t.Fatalf("%+v %v", v, e)
	}
	for _, u := range v.Users {
		if u.ID == "second" && (u.MembershipsKnown || len(u.DepartmentIDs) != 1 || u.DepartmentIDs[0] != "2") {
			t.Fatal(u)
		}
	}
}

func TestRequestBudget(t *testing.T) {
	calls := 0
	c := fixture(t, func(r *http.Request, s string) string {
		if r.URL.Path == "/topapi/v2/user/list" {
			calls++
			return `{"errcode":0,"result":{"list":[],"has_more":true,"next_cursor":` + strconv.Itoa(calls) + `}}`
		}
		return s
	})
	v, e := c.Fetch(context.Background(), "a", "s")
	var f *service.DirectoryFetchError
	if !errors.As(e, &f) || f.Code != "directory_request_budget" || len(v.Users) != 0 {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestHTTPPermissionEnvelope(t *testing.T) {
	cases := []struct {
		body       string
		code       string
		httpStatus int
		invalidate bool
	}{
		{`{"errcode":50002}`, "directory_authorization_failed", 400, true},
		{`{"errcode":60011}`, "directory_authorization_failed", 200, true},
		{`{"errcode":88,"sub_code":"60011"}`, "directory_authorization_failed", 200, true},
		{`{"errcode":88,"sub_code":60011}`, "directory_authorization_failed", 400, true},
		{`{"errcode":40082}`, "directory_authentication_failed", 400, true},
		{`{"errcode":123}`, "directory_provider_rejected", 200, false},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			c := Client{HTTPClient: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.dingtalk.com" {
					return response(`{"accessToken":"t","expireIn":60}`), nil
				}
				v := response(tc.body)
				v.StatusCode = tc.httpStatus
				return v, nil
			})}}
			_, e := c.Fetch(context.Background(), "a", "s")
			var f *service.DirectoryFetchError
			if !errors.As(e, &f) || f.Code != tc.code || f.Invalidate != tc.invalidate {
				t.Fatalf("%v", e)
			}
		})
	}
}

func TestRelationshipMetadataParity(t *testing.T) {
	for _, manager := range []string{`"boss"`, `""`} {
		c := fixture(t, func(r *http.Request, s string) string {
			if r.URL.Path == "/topapi/v2/user/get" {
				return strings.Replace(s, `"name":"Test"`, `"name":"Test","manager_userid":`+manager+`,"unionid":"union-1"`, 1)
			}
			if r.URL.Path == "/topapi/v2/department/get" {
				return strings.Replace(s, `"name":"Root"`, `"name":"Root","dept_manager_userid_list":["boss","boss"]`, 1)
			}
			return s
		})
		v, e := c.Fetch(context.Background(), "app", "secret")
		if e != nil {
			t.Fatal(e)
		}
		u := v.Users[0]
		if u.ID != "user" || !u.DirectManagerKnown || len(u.Aliases) != 1 || u.Aliases[0] != (service.DirectoryAlias{ID: "union-1", Namespace: "union_id", ApplicationID: "app"}) {
			t.Fatal(u)
		}
		if manager == `""` && u.DirectManagerID != nil {
			t.Fatal("explicit manager removal lost", u)
		}
		if manager != `""` && (u.DirectManagerID == nil || *u.DirectManagerID != "boss") {
			t.Fatal(u)
		}
		d := v.Departments[0]
		if !d.LeadersKnown || len(d.LeaderIDs) != 1 || d.LeaderIDs[0] != "boss" {
			t.Fatal(d)
		}
	}
	v, e := fixture(t, nil).Fetch(context.Background(), "app", "secret")
	if e != nil || v.Users[0].DirectManagerKnown || v.Departments[0].LeadersKnown {
		t.Fatal(v, e)
	}
}
func TestRelationshipInvalidAndConflictingFacts(t *testing.T) {
	for _, field := range []string{`"manager_userid":null`, `"manager_userid":1`, `"unionid":null`, `"unionid":123`} {
		c := fixture(t, func(r *http.Request, s string) string {
			if r.URL.Path == "/topapi/v2/user/get" {
				return strings.Replace(s, `"name":"Test"`, `"name":"Test",`+field, 1)
			}
			return s
		})
		if v, e := c.Fetch(context.Background(), "app", "secret"); e == nil || len(v.Users) > 0 {
			t.Fatal(field, v, e)
		}
	}
	c := fixture(t, func(r *http.Request, s string) string {
		if r.URL.Path == "/topapi/v2/user/get" {
			return strings.Replace(s, `"name":"Test"`, `"name":"Test","manager_userid":"boss","unionid":"union-a"`, 1)
		}
		if r.URL.Path == "/topapi/v2/user/list" {
			return strings.Replace(s, `"name":"Test"`, `"name":"Test","manager_userid":"","unionid":"union-b"`, 1)
		}
		return s
	})
	if _, e := c.Fetch(context.Background(), "app", "secret"); e == nil {
		t.Fatal("conflicting facts published")
	}
}

func TestDepartmentLeaderUnknownEmptyAndInvalid(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		known, invalid bool
	}{
		{``, false, false}, {`[]`, true, false}, {`null`, false, true}, {`[1]`, false, true}, {`[""]`, false, true},
	} {
		c := fixture(t, func(r *http.Request, s string) string {
			if tc.raw != "" && r.URL.Path == "/topapi/v2/department/get" {
				return strings.Replace(s, `"name":"Root"`, `"name":"Root","dept_manager_userid_list":`+tc.raw, 1)
			}
			return s
		})
		v, e := c.Fetch(context.Background(), "app", "secret")
		if tc.invalid {
			if e == nil {
				t.Fatal(tc.raw, "published invalid leader list")
			}
			continue
		}
		if e != nil || v.Departments[0].LeadersKnown != tc.known || len(v.Departments[0].LeaderIDs) != 0 {
			t.Fatal(tc.raw, v, e)
		}
	}
}
func TestConflictingUnionAliasesFailSnapshot(t *testing.T) {
	c := fixture(t, func(r *http.Request, s string) string {
		if r.URL.Path == "/topapi/v2/user/get" {
			return strings.Replace(s, `"name":"Test"`, `"name":"Test","unionid":"a"`, 1)
		}
		if r.URL.Path == "/topapi/v2/user/list" {
			return strings.Replace(s, `"name":"Test"`, `"name":"Test","unionid":"b"`, 1)
		}
		return s
	})
	if _, e := c.Fetch(context.Background(), "app", "secret"); e == nil {
		t.Fatal("conflicting alias accepted")
	}
}

func TestRootDepartmentDetailParentCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scope     string
		detail    string
		children  string
		wantError string
	}{
		{name: "canonical root omitted parent", scope: "1", detail: `{"dept_id":1,"name":"Root"}`},
		{name: "canonical root numeric sentinel", scope: "1", detail: `{"dept_id":1,"name":"Root","parent_id":0}`},
		{name: "canonical root string sentinel", scope: "1", detail: `{"dept_id":"1","name":"Root","parent_id":"1"}`},
		{name: "explicit null is not omission", scope: "1", detail: `{"dept_id":1,"name":"Root","parent_id":null}`, wantError: "directory_department_invalid"},
		{name: "contradictory explicit root parent", scope: "1", detail: `{"dept_id":1,"name":"Root","parent_id":2}`, wantError: "directory_department_invalid"},
		{name: "requested root returned another department", scope: "1", detail: `{"dept_id":2,"name":"Child","parent_id":1}`, wantError: "directory_department_identity_mismatch"},
		{name: "ordinary department missing parent", scope: "2", detail: `{"dept_id":2,"name":"Child"}`, wantError: "directory_department_invalid"},
		{name: "requested child returned root without parent", scope: "2", detail: `{"dept_id":1,"name":"Root"}`, wantError: "directory_department_invalid"},
		{name: "child list still requires parent", scope: "1", detail: `{"dept_id":1,"name":"Root"}`, children: `[{"dept_id":2,"name":"Child"}]`, wantError: "directory_department_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(r *http.Request, s string) string {
				switch r.URL.Path {
				case "/auth/scopes":
					return strings.Replace(s, `"authed_dept":[1]`, `"authed_dept":[`+tc.scope+`]`, 1)
				case "/topapi/v2/department/get":
					return `{"errcode":0,"result":` + tc.detail + `}`
				case "/topapi/v2/department/listsub":
					if tc.children != "" {
						return `{"errcode":0,"result":` + tc.children + `}`
					}
				}
				return s
			})
			got, err := c.Fetch(t.Context(), "app", "secret")
			if tc.wantError == "" {
				if err != nil || len(got.Departments) != 1 || got.Departments[0].ParentID != "" || len(got.Users) != 1 {
					t.Fatalf("failed to read full root snapshot %v: %v", got, err)
				}
				return
			}
			var e *service.DirectoryFetchError
			if !errors.As(err, &e) || e.Code != tc.wantError || len(got.Departments) != 0 || len(got.Users) != 0 {
				t.Fatalf("want rejected snapshot (%s), got %v: %v", tc.wantError, got, err)
			}
		})
	}
}
