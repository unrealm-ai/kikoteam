package dingtalksetup

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func client(t *testing.T, change func(string, string) string) Client {
	t.Helper()
	return Client{HTTPClient: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "oapi.dingtalk.com" || r.URL.Scheme != "https" || r.Method != "POST" {
			t.Fatal("unsafe request")
		}
		var b string
		switch r.URL.Path {
		case "/app/registration/init":
			b = `{"errcode":0,"nonce":"private-nonce"}`
		case "/app/registration/begin":
			b = `{"errcode":0,"device_code":"private-device","verification_uri_complete":"https://open.dingtalk.com/scan?challenge=test","expires_in":600,"interval":3}`
		case "/app/registration/poll":
			b = `{"errcode":0,"status":" success ","client_id":"app","client_secret":"private-secret"}`
		default:
			t.Fatal(r.URL.Path)
		}
		if change != nil {
			b = change(r.URL.Path, b)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(b))}, nil
	})}}
}
func TestBeginAndPoll(t *testing.T) {
	c := client(t, nil)
	b, e := c.Begin(context.Background(), "kikoteam")
	if e != nil || b.DeviceCode != "private-device" || b.Interval != 3 {
		t.Fatal(b, e)
	}
	p, e := c.Poll(context.Background(), b.DeviceCode)
	if e != nil || p.State != "SUCCESS" || p.AppID != "app" {
		t.Fatal(p, e)
	}
}
func TestRejectUnsafeProtocol(t *testing.T) {
	for _, value := range []string{`{"errcode":0,"device_code":"private-device","verification_uri_complete":"https://evil.invalid/x"}`, `{"errcode":0,"device_code":"private-device","verification_uri_complete":"https://open.dingtalk.com/x?code=private%252Ddevice"}`, `{"errcode":0,"device_code":"private-device","verification_uri_complete":"https://open.dingtalk.com:443/x"}`, `{"errcode":0,"device_code":"private-device","verification_uri_complete":"https://open.dingtalk.com/x","interval":null}`, strings.Repeat("x", 32769)} {
		c := client(t, func(path, s string) string {
			if strings.HasSuffix(path, "begin") {
				return value
			}
			return s
		})
		if _, e := c.Begin(context.Background(), "kikoteam"); e == nil {
			t.Fatal("accepted", value[:min(80, len(value))])
		}
	}
	if _, e := client(t, nil).Begin(context.Background(), "DING_DWS_CLAW"); e == nil {
		t.Fatal("DWS allowed")
	}
}
func TestPollUnknownAndDenied(t *testing.T) {
	for _, state := range []string{"waiting", "FAIL", "EXPIRED", "new-status"} {
		c := client(t, func(_, s string) string { return `{"errcode":0,"status":"` + state + `"}` })
		p, e := c.Poll(context.Background(), "device")
		if e != nil || p.AppID != "" || p.Secret != "" {
			t.Fatal(p, e)
		}
	}
}
