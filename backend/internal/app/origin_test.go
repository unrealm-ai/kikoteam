package app

import (
	"net/url"
	"testing"
)

func TestPublicOriginHTTPIPOptIn(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		optIn    bool
		accepted bool
	}{
		{"http://203.0.113.7:8080", false, false},
		{"http://203.0.113.7:8080", true, true},
		{"http://[2001:db8::1]:8080", true, true},
		{"http://example.com", true, false},
		{"http://user@203.0.113.7/path", true, false},
		{"http://localhost:18080", false, true},
		{"https://example.com", false, true},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := validOrigin(u, tc.optIn); got != tc.accepted {
			t.Errorf("validOrigin(%q, %t) = %t, want %t", tc.raw, tc.optIn, got, tc.accepted)
		}
	}
}
