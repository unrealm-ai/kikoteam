package feishudirectory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(raw string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}
}
func fixture(t *testing.T, override func(*http.Request) (*http.Response, error, bool)) Client {
	t.Helper()
	return Client{HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "open.feishu.cn" {
			t.Fatalf("unexpected origin %s", r.URL.Host)
		}
		if override != nil {
			if out, err, ok := override(r); ok {
				return out, err
			}
		}
		var data string
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			var b map[string]string
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				t.Fatal(err)
			}
			if b["app_id"] != "app" || b["app_secret"] != "secret" {
				t.Fatal("credentials mismatch")
			}
			return response(`{"code":0,"tenant_access_token":"token"}`), nil
		case "/open-apis/tenant/v2/tenant/query":
			data = `{"tenant":{"tenant_key":"tenant","display_id":"NOT-TENANT"}}`
		case "/open-apis/contact/v3/scopes":
			data = `{"user_ids":["u"],"department_ids":["d"],"has_more":false}`
		case "/open-apis/contact/v3/users/u":
			data = `{"user":{"open_id":"u","name":"Ada","department_ids":["d"],"status":{"is_frozen":false,"is_resigned":false,"is_exited":false,"is_activated":true}}}`
		case "/open-apis/contact/v3/departments/d":
			data = `{"department":{"open_department_id":"d","name":"Engineering","parent_department_id":"0"}}`
		case "/open-apis/contact/v3/users/find_by_department":
			data = `{"items":[{"open_id":"u","name":"Ada","department_ids":["d"]}],"has_more":false}`
		case "/open-apis/contact/v3/departments/d/children":
			data = `{"has_more":false}`
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("missing token")
		}
		if strings.Contains(r.URL.Path, "/contact/") && (r.URL.Query().Get("user_id_type") != "open_id" || r.URL.Query().Get("department_id_type") != "open_department_id") {
			t.Fatal("namespace missing")
		}
		return response(`{"code":0,"data":` + data + `}`), nil
	})}}
}
func TestFetchCompleteScopedDirectory(t *testing.T) {
	scopeCalls := 0
	c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
		if strings.HasSuffix(r.URL.Path, "/scopes") {
			scopeCalls++
		}
		return nil, nil, false
	})
	out, err := c.Fetch(context.Background(), "app", "secret", "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if scopeCalls != 2 || len(out.Users) != 1 || len(out.Departments) != 1 || out.Users[0].Status != "active" || !out.Users[0].MembershipsKnown {
		t.Fatalf("unexpected snapshot %+v %d", out, scopeCalls)
	}
}
func TestSingleCategoryScopeWorksForProbeAndSync(t *testing.T) {
	for _, tc := range []struct {
		scope       string
		departments int
	}{
		{`{"user_ids":["u"],"has_more":false}`, 0},
		{`{"department_ids":["d"],"has_more":false}`, 1},
	} {
		c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
			if strings.HasSuffix(r.URL.Path, "/scopes") {
				return response(`{"code":0,"data":` + tc.scope + `,"msg":"success"}`), nil, true
			}
			return nil, nil, false
		})
		if err := c.ProbeScope(context.Background(), "token"); err != nil {
			t.Fatal(err)
		}
		got, err := c.Fetch(context.Background(), "app", "secret", "tenant")
		if err != nil || len(got.Users) != 1 || len(got.Departments) != tc.departments {
			t.Fatal(got, err)
		}
	}
}

func TestFetchRejectsIncompleteOrChangedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, path, raw string
		invalidate      bool
	}{
		{"tenant", "/tenant/v2/tenant/query", `{"code":0,"data":{"tenant":{"display_id":"tenant"}}}`, true},
		{"scope missing", "/contact/v3/scopes", `{"code":0,"data":{"department_ids":[],"has_more":false}}`, true},
		{"scope null", "/contact/v3/scopes", `{"code":0,"data":{"user_ids":["u"],"department_ids":null,"has_more":false}}`, true},
		{"scope group", "/contact/v3/scopes", `{"code":0,"data":{"department_ids":[],"user_ids":[],"group_ids":["g"],"has_more":false}}`, true},
		{"scope denied", "/contact/v3/scopes", `{"code":99991672,"msg":"secret provider details"}`, true},
		{"bad pagination", "/contact/v3/scopes", `{"code":0,"data":{"department_ids":[],"user_ids":[]}}`, false},
		{"loop", "/contact/v3/scopes", `{"code":0,"data":{"department_ids":[],"user_ids":[],"has_more":true,"page_token":"again"}}`, false},
		{"wrong user", "/contact/v3/users/u", `{"code":0,"data":{"user":{"open_id":"other","name":"Ada"}}}`, false},
		{"wrong membership", "/contact/v3/users/find_by_department", `{"code":0,"data":{"items":[{"open_id":"u","name":"Ada","department_ids":[]}],"has_more":false}}`, false},
		{"null children", "/contact/v3/departments/d/children", `{"code":0,"data":{"items":null,"has_more":false}}`, false},
		{"unknown parent", "/contact/v3/departments/d", `{"code":0,"data":{"department":{"open_department_id":"d","name":"Department"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
				if strings.HasSuffix(r.URL.Path, tc.path) {
					return response(tc.raw), nil, true
				}
				return nil, nil, false
			})
			out, err := c.Fetch(context.Background(), "app", "secret", "tenant")
			var e *service.DirectoryFetchError
			if !errors.As(err, &e) || e.Invalidate != tc.invalidate || len(out.Users) != 0 || len(out.Departments) != 0 {
				t.Fatalf("partial data or wrong error %+v %v", out, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("provider text leaked")
			}
		})
	}
}
func TestScopeChangedAndNetworkPreservation(t *testing.T) {
	n := 0
	c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
		if strings.HasSuffix(r.URL.Path, "/scopes") {
			n++
			if n == 2 {
				return response(`{"code":0,"data":{"user_ids":[],"department_ids":[],"has_more":false}}`), nil, true
			}
		}
		return nil, nil, false
	})
	_, err := c.Fetch(context.Background(), "app", "secret", "tenant")
	var e *service.DirectoryFetchError
	if !errors.As(err, &e) || !e.Invalidate || e.Code != "directory_scope_changed" {
		t.Fatal(err)
	}
	c = fixture(t, func(_ *http.Request) (*http.Response, error, bool) {
		return nil, errors.New("secret network error"), true
	})
	_, err = c.Fetch(context.Background(), "app", "secret", "tenant")
	if !errors.As(err, &e) || e.Invalidate || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
func TestHTTPBudgetsAndCancellation(t *testing.T) {
	for _, mode := range []string{"oversize", "redirect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
				switch mode {
				case "oversize":
					return response(strings.Repeat("x", maxBody+1)), nil, true
				case "redirect":
					res := response("")
					res.StatusCode = 302
					res.Header.Set("Location", "https://attacker.invalid")
					return res, nil, true
				default:
					cancel()
					return nil, r.Context().Err(), true
				}
			})
			_, err := c.Fetch(ctx, "app", "secret", "tenant")
			if err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestFetchPaginationAndRecursiveDepartments(t *testing.T) {
	c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
		q := r.URL.Query()
		var data string
		switch r.URL.Path {
		case "/open-apis/contact/v3/scopes":
			if q.Get("page_token") == "" {
				data = `{"user_ids":["u"],"department_ids":[],"has_more":true,"page_token":"next"}`
			} else {
				data = `{"user_ids":[],"department_ids":["d"],"has_more":false}`
			}
		case "/open-apis/contact/v3/departments/d/children":
			data = `{"items":[{"open_department_id":"child","name":"Child","parent_department_id":"d"}],"has_more":false}`
		case "/open-apis/contact/v3/departments/child":
			data = `{"department":{"open_department_id":"child","name":"Child","parent_department_id":"d"}}`
		case "/open-apis/contact/v3/departments/child/children":
			data = `{"items":[],"has_more":false}`
		case "/open-apis/contact/v3/users/find_by_department":
			if q.Get("department_id") == "child" {
				data = `{"items":[],"has_more":false}`
			} else {
				return nil, nil, false
			}
		default:
			return nil, nil, false
		}
		return response(`{"code":0,"data":` + data + `}`), nil, true
	})
	out, err := c.Fetch(context.Background(), "app", "secret", "tenant")
	if err != nil || len(out.Departments) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestRequestBudgetAndAuthenticationFailure(t *testing.T) {
	n := 0
	c := fixture(t, func(r *http.Request) (*http.Response, error, bool) {
		if strings.HasSuffix(r.URL.Path, "/scopes") {
			n++
			raw, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"user_ids": []string{}, "department_ids": []string{}, "has_more": true, "page_token": strings.Repeat("x", n)}})
			if err != nil {
				t.Fatal(err)
			}
			return response(string(raw)), nil, true
		}
		return nil, nil, false
	})
	_, err := c.Fetch(context.Background(), "app", "secret", "tenant")
	var e *service.DirectoryFetchError
	if !errors.As(err, &e) || e.Code != "directory_request_budget" || n != 498 {
		t.Fatalf("%v n=%d", err, n)
	}
	c = fixture(t, func(_ *http.Request) (*http.Response, error, bool) {
		return response(`{"code":10003,"msg":"invalid secret"}`), nil, true
	})
	_, err = c.Fetch(context.Background(), "app", "secret", "tenant")
	if !errors.As(err, &e) || !e.Invalidate {
		t.Fatal(err)
	}
}
