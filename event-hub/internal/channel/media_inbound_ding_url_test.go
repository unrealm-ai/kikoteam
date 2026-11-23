package channel

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const testSGDingMediaHost = "lippi-space-sg.oss-ap-southeast-1.aliyuncs.com"

func TestDingMediaRequestsOriginalSchemeAuthorityAndSignedBytes(t *testing.T) {
	const signed = "/private%2fpath/%2B?key=vm%2bE%3D&label=a+b&label=b&escaped=%2F"
	for _, tc := range []struct{ scheme, host string }{
		{"http", testSGDingMediaHost},
		{"http", "sample-bucket.oss-cn-zhangjiakou.aliyuncs.com:80"},
		{"https", "public-attachments.example"},
		{"https", "Public-attachments.example:443"},
	} {
		t.Run(tc.scheme+tc.host, func(t *testing.T) {
			calls := 0
			item, err := fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, tc.scheme+"://"+tc.host+signed, "",
				mediaTestTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != http.MethodGet || r.URL.Scheme != tc.scheme || r.URL.Host != tc.host || r.URL.RequestURI() != signed {
						t.Fatal("request did not preserve scheme/authority/path/query bytes")
					}
					for _, header := range []string{"Authorization", "X-Acs-Dingtalk-Access-Token", "Cookie", "Referer"} {
						if r.Header.Get(header) != "" {
							t.Fatal("download request carried an unrelated credential or Referer")
						}
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data"))}, nil
				}))
			if err != nil || calls != 1 || string(item.data) != "data" {
				t.Fatalf("unexpected download outcome: calls=%d code=%v", calls, err)
			}
		})
	}
}

func TestDingMediaRejectsMalformedOrUnsupportedAuthoritativeURL(t *testing.T) {
	blocked := []string{
		"ftp://" + testSGDingMediaHost + "/file",
		"//" + testSGDingMediaHost + "/file?token=fixture",
		"https://127.0.0.1/file",
		"http://[::1]/file",
		"https://[test.example]/file",
		"https://localhost/file",
		"https://bad_label.example/file",
		"https://test.example/file?token=raw secret",
		"https://test.example/file?token=%zz",
		"http://test.example/file with space",
		"http://" + testSGDingMediaHost + ":443/file",
		"https://" + testSGDingMediaHost + ":80/file",
		"http://" + testSGDingMediaHost + ":/file",
		"http://" + testSGDingMediaHost + ":9443/file",
		"http://user:password@" + testSGDingMediaHost + "/file",
		"http://" + testSGDingMediaHost + "/bad%zz",
		"http:///" + testSGDingMediaHost + "/file",
		"http://" + testSGDingMediaHost + "\\example.com/file",
	}
	for idx, raw := range blocked {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			_, err := fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, raw, "", mediaTestTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("rejected input reached the transport")
				return nil, nil
			}))
			if !errors.Is(err, mediaDownloadURLInvalid) {
				t.Fatalf("invalid initial URL was not rejected: %v", err)
			}
		})
	}
}

func TestDingMediaRedirectSchemesAndSignedRefererProtection(t *testing.T) {
	for _, tc := range []struct{ initial, location string }{
		{initial: "http", location: "http"},
		{initial: "http", location: "https"},
		{initial: "https", location: "https"},
		{initial: "https", location: "http"},
	} {
		t.Run(tc.initial+"-"+tc.location, func(t *testing.T) {
			const file = "/privatePath?privateQuery=privateToken"
			const host = "cdn.generic.example"
			destination := tc.location + "://" + host + file
			calls := 0
			item, err := fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, tc.initial+"://"+testSGDingMediaHost+file, "",
				mediaTestTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Referer() != "" {
						t.Fatal("redirect forwarded a signed Referer")
					}
					if calls == 1 {
						return &http.Response{StatusCode: http.StatusFound, Body: http.NoBody, Header: http.Header{"Location": {destination}}}, nil
					}
					if req.URL.String() != destination {
						t.Fatal("redirect scheme, authority or signature changed")
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data"))}, nil
				}))
			if err != nil || calls != 2 || string(item.data) != "data" {
				t.Fatalf("guarded download redirect failed: calls=%d error=%v", calls, err)
			}
		})
	}
}
