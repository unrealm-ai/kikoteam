package channel

import (
	"context"
	"encoding/json"
	"errors"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLifecycleIdentityAndTimestamp(t *testing.T) {
	b := Binding{ID: "binding", Generation: 1, Revision: 2, TenantID: "tenant", Credentials: Credentials{AppID: "app"}}
	for _, tc := range []struct {
		name, app, tenant, stamp string
		ok                       bool
	}{
		{"milliseconds", "app", "tenant", strconv.FormatInt(time.Now().UnixMilli(), 10), true},
		{"microseconds", "app", "tenant", strconv.FormatInt(time.Now().UnixMicro(), 10), true},
		{"foreign-app", "other", "tenant", "1000", false}, {"foreign-tenant", "app", "other", "1000", false}, {"missing-time", "app", "tenant", "", false}, {"future", "app", "tenant", strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := map[string]any{"header": map[string]string{"app_id": tc.app, "tenant_key": tc.tenant, "event_id": "event", "create_time": tc.stamp}, "event": map[string]any{"chat_id": "chat", "users": []any{map[string]any{"user_id": map[string]string{"open_id": "alice"}}, map[string]any{"user_id": map[string]string{"open_id": "alice"}}}}}
			v, err := normalizeLifecycle(b, e, "members_removed")
			if (err == nil) != tc.ok {
				t.Fatal(v, err)
			}
			if tc.ok && (v.EventType != "members_removed" || len(v.MemberIDs) != 1) {
				t.Fatal(v)
			}
		})
	}
}
func TestGroupCheckFencesAndRetry(t *testing.T) {
	for _, mode := range []string{"present", "absent", "failure", "stale", "tenant", "changed_during_check", "completion_stale"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			j := groupCheck{ID: "group", LeaseToken: "lease", BindingID: "binding", Generation: 1, Revision: 2, TenantID: "tenant", ChatID: "chat"}
			b := Binding{ID: "binding", Enabled: true, Provider: "feishu", Generation: 1, Revision: 2, TenantID: "tenant"}
			if mode == "stale" {
				b.Generation++
			}
			if mode == "tenant" {
				b.TenantID = "other"
			}
			checked, completed := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				completed++
				if r.Header.Get("Authorization") != "Bearer internal" {
					t.Error("missing service auth")
				}
				if !strings.HasSuffix(r.URL.Path, "/group/complete") {
					t.Error(r.URL.Path)
				}
				var result struct {
					Lease   string `json:"lease_token"`
					Present *bool  `json:"present"`
				}
				if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				if result.Lease != "lease" {
					t.Error(result)
				}
				if mode == "failure" && result.Present != nil {
					t.Error("transport failure became absence")
				}
				if mode == "absent" && (result.Present == nil || *result.Present) {
					t.Error("false result lost")
				}
				if mode == "completion_stale" {
					w.WriteHeader(409)
				}
			}))
			defer srv.Close()
			backend, err := NewBackend(srv.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			lookup := func(string) (Binding, bool) { return b, true }
			check := func(context.Context, Binding, string) (bool, error) {
				checked++
				if mode == "changed_during_check" {
					b.Revision++
				}
				if mode == "failure" {
					return false, errors.New("offline")
				}
				return mode != "absent", nil
			}
			if err = processGroupCheck(ctx, backend, lookup, check, j); err != nil {
				t.Fatal(err)
			}
			if (mode == "stale" || mode == "tenant") && (checked != 0 || completed != 0) {
				t.Fatal("stale check ran")
			}
			if mode == "changed_during_check" && completed != 0 {
				t.Fatal("stale result submitted")
			}
			if mode != "stale" && mode != "tenant" && mode != "changed_during_check" && (checked != 1 || completed != 1) {
				t.Fatal(checked, completed)
			}
		})
	}
}
func TestFeishuMembershipRemoteErrorsRemainUnknown(t *testing.T) {
	for _, body := range []string{`{"code":0,"data":{"is_in_chat":true}}`, `{"code":0,"data":{"is_in_chat":false}}`, `{"code":0,"data":{}}`, `{"code":99991672,"msg":"denied"}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					if _, err := w.Write([]byte(`{"code":0,"tenant_access_token":"test","expire":7200}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if r.URL.Path != "/open-apis/im/v1/chats/chat/members/is_in_chat" {
					t.Error(r.URL.Path)
				}
				if _, err := w.Write([]byte(body)); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			api := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithLogger(quietLogger{}))
			present, err := checkFeishuMembershipWithClient(context.Background(), api, "chat")
			wantErr := !strings.Contains(body, "is_in_chat")
			if (err != nil) != wantErr {
				t.Fatal(present, err)
			}
			if !wantErr && present != strings.Contains(body, "true") {
				t.Fatal(present)
			}
		})
	}
}
