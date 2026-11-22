package feishugroups

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundtrip func(*http.Request) (*http.Response, error)

func (f roundtrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(raw string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}
}
func fixtureClient(t *testing.T, tenant, page string) *Client {
	t.Helper()
	return &Client{HTTPClient: &http.Client{Transport: roundtrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "open.feishu.cn" {
			t.Fatal("untrusted host", r.URL)
		}
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			if r.Method != "POST" {
				t.Fatal(r.Method)
			}
			return response(`{"code":0,"tenant_access_token":"private-token"}`), nil
		case "/open-apis/tenant/v2/tenant/query":
			if r.Header.Get("Authorization") != "Bearer private-token" {
				t.Fatal("token missing")
			}
			return response(tenant), nil
		case "/open-apis/im/v1/chats":
			if r.URL.Query().Get("page_size") != "100" || r.URL.Query().Get("user_id_type") != "open_id" {
				t.Fatal(r.URL)
			}
			return response(page), nil
		default:
			t.Fatal(r.URL)
			return nil, nil
		}
	})}}
}
func TestJoinedGroupPageAndIdentity(t *testing.T) {
	c := fixtureClient(t, `{"code":0,"data":{"tenant":{"tenant_key":"tenant"}}}`, `{"code":0,"data":{"items":[{"chat_id":"chat","name":"群组","tenant_key":"tenant"}],"has_more":true,"page_token":"next"}}`)
	p, e := c.List(context.Background(), "app", "secret", "tenant", "")
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != "chat" || p.NextCursor != "next" {
		t.Fatal(p, e)
	}
}
func TestJoinedGroupsFailClosed(t *testing.T) {
	for name, page := range map[string]string{"missing_items": `{"has_more":false}`, "loop": `{"items":[],"has_more":true,"page_token":"old"}`, "foreign": `{"items":[{"chat_id":"one"},{"chat_id":"two","tenant_key":"foreign"}],"has_more":false}`, "duplicate": `{"items":[{"chat_id":"same"},{"chat_id":"same"}],"has_more":false}`, "missing_more": `{"items":[]}`} {
		t.Run(name, func(t *testing.T) {
			c := fixtureClient(t, `{"code":0,"data":{"tenant":{"tenant_key":"tenant"}}}`, `{"code":0,"data":`+page+`}`)
			p, e := c.List(context.Background(), "app", "secret", "tenant", "old")
			if e == nil || len(p.Items) != 0 {
				t.Fatal("partial/invalid accepted", p, e)
			}
		})
	}
	c := fixtureClient(t, `{"code":0,"data":{"tenant":{"display_id":"tenant"}}}`, `{}`)
	if _, e := c.List(context.Background(), "app", "secret", "tenant", ""); e == nil {
		t.Fatal("display id trusted")
	}
}
func TestJoinedGroupsRedirectBudgetAndRedaction(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "provider"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := Client{HTTPClient: &http.Client{Transport: roundtrip(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "redirect":
					res := response("")
					res.StatusCode = 302
					res.Header.Set("Location", "https://evil.invalid/secret")
					return res, nil
				case "oversize":
					return response(strings.Repeat("x", maxBody+1)), nil
				default:
					return response(`{"code":999,"msg":"secret=private-token"}`), nil
				}
			})}}
			_, e := c.List(context.Background(), "app", "secret", "tenant", "")
			if e == nil || calls != 1 || strings.Contains(e.Error(), "private-token") {
				t.Fatal(calls, e)
			}
		})
	}
}
