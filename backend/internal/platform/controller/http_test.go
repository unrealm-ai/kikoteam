package controller

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestTrustedProxySource(t *testing.T) {
	s := &Server{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	for _, tc := range []struct{ peer, header, want string }{{"192.0.2.3:123", "198.51.100.2", "192.0.2.3"}, {"10.0.0.2:123", "198.51.100.2, 10.0.0.3", "198.51.100.2"}, {"10.0.0.2:123", "spoof, 198.51.100.2", "198.51.100.2"}, {"10.0.0.2:123", "invalid", "10.0.0.2"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.header)
		if got := s.source(r); got != tc.want {
			t.Errorf("source=%s want=%s", got, tc.want)
		}
	}
}
func TestPatchNullAndInvalidID(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":null}`))
	var v struct {
		Name *string `json:"name"`
	}
	if e := decode(r, &v); e == nil {
		t.Fatal("null accepted")
	}
	r.SetPathValue("workspace_id", "not-uuid")
	if e := validIDs(r); e == nil {
		t.Fatal("invalid ID accepted")
	}
}
