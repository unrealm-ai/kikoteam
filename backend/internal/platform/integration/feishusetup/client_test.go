package feishusetup

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestBeginUsesOfficialProtocolAndApprovedProfile(t *testing.T) {
	c := Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != accounts || r.Method != "POST" {
			t.Fatalf("unexpected destination %s", r.URL)
		}
		if e := r.ParseForm(); e != nil {
			t.Fatal(e)
		}
		if r.Form.Get("action") != "begin" || r.Form.Get("archetype") != "PersonalAgent" {
			t.Fatal(r.Form)
		}
		return response(`{"device_code":"private","verification_uri_complete":"https://accounts.feishu.cn/confirm?x=1","expires_in":600,"interval":5}`), nil
	})}
	got, e := c.Begin(context.Background(), "Worker", "Description")
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(got.URL)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	if q.Get("createOnly") != "true" || q.Get("name") != "Worker" {
		t.Fatal(q)
	}
	compressed, e := base64.RawURLEncoding.DecodeString(q.Get("addons"))
	if e != nil {
		t.Fatal(e)
	}
	z, e := gzip.NewReader(strings.NewReader(string(compressed)))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := z.Close(); e != nil {
			t.Error(e)
		}
	}()
	b, e := io.ReadAll(z)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "contacts_range:write") || !strings.Contains(string(b), "approval:") {
		t.Fatal("missing approved profile permissions")
	}
	var payload struct {
		Preset bool `json:"preset"`
		Scopes struct {
			Tenant []string `json:"tenant"`
		} `json:"scopes"`
		Events struct {
			Items map[string][]string `json:"items"`
		} `json:"events"`
		Callbacks struct {
			Items []string `json:"items"`
		} `json:"callbacks"`
	}
	if e = json.Unmarshal(b, &payload); e != nil || payload.Preset || len(payload.Scopes.Tenant) != len(service.FeishuPermissions()) {
		t.Fatal(payload, e)
	}
	if !slices.Equal(payload.Events.Items["tenant"], []string{"im.message.receive_v1", "im.chat.member.bot.added_v1", "im.chat.member.bot.deleted_v1", "im.chat.disbanded_v1"}) ||
		!slices.Equal(payload.Callbacks.Items, []string{"card.action.trigger"}) {
		t.Fatalf("unexpected events or callbacks: %+v", payload)
	}
}
func TestBeginRejectsUntrustedURLsAndMalformedExpiry(t *testing.T) {
	for _, uri := range []string{"https://evil.test/confirm", "http://accounts.feishu.cn/confirm", "https://accounts.feishu.cn:444/confirm", "https://user@accounts.feishu.cn/confirm"} {
		c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			b, e := json.Marshal(map[string]any{"device_code": "x", "verification_uri_complete": uri, "expires_in": 600, "interval": 5})
			if e != nil {
				t.Fatal(e)
			}
			return response(string(b)), nil
		})}
		if _, e := c.Begin(context.Background(), "a", "b"); e == nil {
			t.Fatal(uri)
		}
	}
}
func TestPollResponsesAndNoLarkRedirect(t *testing.T) {
	for _, state := range []string{"authorization_pending", "slow_down", "access_denied", "expired_token"} {
		c := Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if e := r.ParseForm(); e != nil {
				t.Fatal(e)
			}
			if r.Form.Get("device_code") != "secret-device" {
				t.Fatal(r.Form)
			}
			res := response(`{"error":"` + state + `"}`)
			res.StatusCode = 400
			return res, nil
		})}
		got, e := c.Poll(context.Background(), "secret-device")
		if e != nil || got.State != state {
			t.Fatal(got, e)
		}
	}
	c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return response(`{"user_info":{"tenant_brand":"lark"},"client_id":"cli_test","client_secret":"secret"}`), nil
	})}
	got, e := c.Poll(context.Background(), "x")
	if e != nil || got.State != "unsupported" {
		t.Fatal(got, e)
	}
}
func TestDiagnosticsDoesNotTreatUserScopeOrUnknownAsGranted(t *testing.T) {
	for _, mode := range []string{"granted", "user_only", "conflict", "invalid_code", "directory_500", "directory_429", "directory_denied_http", "directory_denied_code", "directory_unknown_code", "directory_malformed"} {
		t.Run(mode, func(t *testing.T) {
			c := Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "open.feishu.cn" {
					t.Fatal(r.URL)
				}
				switch r.URL.Path {
				case "/open-apis/auth/v3/tenant_access_token/internal":
					if mode == "invalid_code" {
						return response(`{"code":"bad","tenant_access_token":"token"}`), nil
					}
					return response(`{"code":0,"tenant_access_token":"token"}`), nil
				case "/open-apis/bot/v3/info":
					return response(`{"code":0,"bot":{"open_id":"ou_bot"}}`), nil
				case "/open-apis/tenant/v2/tenant/query":
					return response(`{"code":0,"data":{"tenant":{"display_id":"not-a-tenant-key"}}}`), nil
				case "/open-apis/contact/v3/scopes":
					q := r.URL.Query()
					if q.Get("page_size") != "50" || q.Get("user_id_type") != "open_id" || q.Get("department_id_type") != "open_department_id" || r.Header.Get("Authorization") != "Bearer token" {
						t.Fatalf("unexpected directory probe request: %s", r.URL)
					}
					switch mode {
					case "directory_500":
						resp := response(`{"code":2200}`)
						resp.StatusCode = http.StatusInternalServerError
						return resp, nil
					case "directory_429":
						resp := response(`{"code":2200}`)
						resp.StatusCode = http.StatusTooManyRequests
						return resp, nil
					case "directory_denied_http":
						resp := response("not JSON")
						resp.StatusCode = http.StatusForbidden
						return resp, nil
					case "directory_denied_code":
						return response(`{"code":99991672,"msg":"Permission denied"}`), nil
					case "directory_unknown_code":
						return response(`{"code":2200}`), nil
					case "directory_malformed":
						return response(`{"code":0,"data":{}}`), nil
					}
					return response(`{"code":0,"data":{"user_ids":[],"department_ids":[]}}`), nil
				case "/open-apis/application/v6/scopes":
					rows := []map[string]any{}
					for _, p := range service.FeishuPermissions() {
						identity := "tenant"
						if mode == "user_only" {
							identity = "user"
						}
						rows = append(rows, map[string]any{"scope_name": p.Scope, "scope_type": identity, "grant_status": 1})
					}
					if mode == "conflict" {
						rows = append(rows, map[string]any{"scope_name": service.FeishuPermissions()[0].Scope, "scope_type": "tenant", "grant_status": 2})
					}
					b, e := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"scopes": rows}})
					if e != nil {
						t.Fatal(e)
					}
					return response(string(b)), nil
				default:
					t.Fatal(r.URL)
					return nil, nil
				}
			})}
			d, e := c.Diagnose(context.Background(), "cli_test", "secret")
			if e != nil {
				t.Fatal(e)
			}
			if d.Ready != (mode == "granted") || d.TenantID != "" {
				t.Fatal(d)
			}
			switch mode {
			case "directory_500", "directory_429", "directory_unknown_code", "directory_malformed":
				if d.DirectoryStatus != "unknown" || d.ErrorCode != "feishu_directory_check_unavailable" || d.ScopeStatus != "pass" {
					t.Fatal(d)
				}
			case "directory_denied_http", "directory_denied_code":
				if mode == "directory_denied_code" && d.ErrorDetail != "99991672 Permission denied" {
					t.Fatal(d)
				}
				if d.DirectoryStatus != "failed" || d.ErrorCode != "feishu_permissions_or_configuration_required" {
					t.Fatal(d)
				}
			case "granted":
				if d.DirectoryStatus != "pass" || d.ErrorCode != "" {
					t.Fatal(d)
				}
			}
		})
	}
}
func TestDiagnosticsIncludesRedactedPlatformPolicyError(t *testing.T) {
	for _, status := range []int{200, 403} {
		c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			resp := response(`{"code":21001,"msg":"Access denied due to platform policy. private-secret","debug":"private-debug"}`)
			resp.StatusCode = status
			return resp, nil
		})}
		d, err := c.Diagnose(context.Background(), "cli_test", "private-secret")
		if err != nil || d.CredentialStatus != "failed" || d.ErrorDetail != "21001 Access denied due to platform policy. [已隐藏]" {
			t.Fatal(d, err)
		}
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	calls := 0
	c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := response("")
		r.StatusCode = 302
		r.Header.Set("Location", "https://evil.test/steal")
		return r, nil
	})}
	if _, e := c.Begin(context.Background(), "a", "b"); e == nil || calls != 1 {
		t.Fatal(e, calls)
	}
}

func TestBeginClampsProviderExpiryToLocalWindow(t *testing.T) {
	for _, tc := range []struct{ expiry, want int }{{3600, 600}, {600, 600}, {120, 120}, {0, 0}, {-1, 0}} {
		t.Run(strconv.Itoa(tc.expiry), func(t *testing.T) {
			c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return response(fmt.Sprintf(`{"device_code":"private","verification_uri_complete":"https://open.feishu.cn/confirm?x=1","expires_in":%d,"interval":5}`, tc.expiry)), nil
			})}
			got, err := c.Begin(context.Background(), "Worker", "Description")
			if tc.want == 0 {
				if err == nil {
					t.Fatal("nonpositive expiry accepted")
				}
				return
			}
			if err != nil || got.ExpiresIn != tc.want {
				t.Fatalf("expiry %d want %d, err %v", got.ExpiresIn, tc.want, err)
			}
		})
	}
}

func TestContactRangeOnlyWritesNewAppAfterGuard(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			calls := 0
			c := Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(`{"code":0,"tenant_access_token":"token"}`), nil
				}
				if r.Method != "PATCH" || r.URL.Path != "/open-apis/application/v6/applications/cli_new/contacts_range" {
					t.Fatal(r.Method, r.URL)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != `{"contacts_range_type":"all"}` {
					t.Fatal(string(body))
				}
				return response(`{"code":0}`), nil
			})}
			err := c.ExpandContactRange(context.Background(), "cli_new", "secret", func() error {
				if blocked {
					return errors.New("binding changed")
				}
				return nil
			})
			if blocked {
				if err == nil || calls != 1 {
					t.Fatal(err, calls)
				}
			} else if err != nil || calls != 2 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestContactRangeDefiniteProviderDenials(t *testing.T) {
	for _, code := range []int{99991663, 99991672} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			calls := 0
			c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(`{"code":0,"tenant_access_token":"token"}`), nil
				}
				return response(fmt.Sprintf(`{"code":%d}`, code)), nil
			})}
			err := c.ExpandContactRange(context.Background(), "cli_new", "secret", func() error { return nil })
			var failure *service.Error
			if !errors.As(err, &failure) {
				t.Fatal(err)
			}
			want := "feishu_authorization_denied"
			if code == 99991663 {
				want = "feishu_authentication_failed"
			}
			if failure.Code != want {
				t.Fatal(failure.Code)
			}
		})
	}
}

func TestReceiptReactionStatusDistinguishesUnlistedAndIncomplete(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{`{"scopes":[]}`, "not_detected"},
		{`{"scopes":[],"has_more":true}`, "unknown"},
		{`{"scopes":[{"scope_name":"im:message.reactions:write_only","grant_status":1}]}`, "unknown"},
		{`{"scopes":[{"scope_name":"im:message","grant_status":1}]}`, "unknown"},
		{`{"scopes":[{"scope_name":"im:message","scope_type":"tenant"}]}`, "unknown"},
		{`{"scopes":[{"scope_name":"im:message.reactions:write_only","scope_type":"user","grant_status":1}]}`, "not_detected"},
		{`{"scopes":[{"scope_name":"im:message.reactions:write_only","scope_type":"tenant","grant_status":2}]}`, "missing"},
		{`{"scopes":[{"scope_name":"im:message.reactions:write_only","scope_type":"tenant","grant_status":1}]}`, "granted"},
	} {
		var data map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.raw), &data); err != nil {
			t.Fatal(err)
		}
		d := service.FeishuDiagnostics{Permissions: service.FeishuPermissions()}
		inspectScopes(&d, data)
		for _, x := range d.Permissions {
			if x.Scope == service.FeishuReceiptReactionScope && x.Status != c.want {
				t.Fatalf("want %s got %s for %s", c.want, x.Status, c.raw)
			}
		}
	}
}
