package channelprobe

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCredentialProbeUsesFixedHTTPAndNeverConnectsWecom(t *testing.T) {
	for _, p := range []string{"feishu", "dingtalk"} {
		t.Run(p, func(t *testing.T) {
			calls := 0
			c := Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.URL.Scheme != "https" {
					t.Fatal("unsafe request")
				}
				var body map[string]string
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				reply := `{"accessToken":"private-token","expireIn":7200}`
				if p == "feishu" {
					if r.URL.Host != "open.feishu.cn" || body["app_id"] != "id" || body["app_secret"] != "secret" {
						t.Fatal("wrong feishu protocol")
					}
					reply = `{"code":0,"tenant_access_token":"private-token","expire":7200}`
				} else if r.URL.Host != "api.dingtalk.com" || body["appKey"] != "id" || body["appSecret"] != "secret" {
					t.Fatal("wrong ding protocol")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(reply)), Header: http.Header{}}, nil
			})}
			if e := c.Verify(context.Background(), p, "id", "secret"); e != nil || calls != 1 {
				t.Fatal(e, calls)
			}
		})
	}
	c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("wecom network call"); return nil, nil })}
	if c.Verify(context.Background(), "wecom", "bot", "secret") == nil {
		t.Fatal("wecom supported")
	}
}
func TestCredentialProbeFailuresAreRedactedAndNeverRedirected(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{{200, `{"code":1,"msg":"sensitive-secret"}`, "channel_credentials_invalid"}, {200, `{"code":0,"tenant_access_token":"token","expire":0}`, "channel_credentials_invalid"}, {302, ``, "channel_validation_unavailable"}, {429, `{}`, "channel_validation_rate_limited"}, {500, `sensitive-secret`, "channel_validation_unavailable"}, {200, `not-json`, "channel_validation_unavailable"}, {200, strings.Repeat("x", 65537), "channel_validation_unavailable"}} {
		c := Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://evil.invalid"}}}, nil
		})}
		e := c.Verify(context.Background(), "feishu", "id", "secret")
		var failure *service.Error
		if !errors.As(e, &failure) || failure.Code != tc.code || strings.Contains(e.Error(), "sensitive") {
			t.Fatal(tc.status, e)
		}
	}
}
