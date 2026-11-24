package service

import "testing"

func TestNormalizeTaskLinkOrigin(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"   ", ""}, {"https://Example.test/", "https://example.test"},
		{"https://example.test:443", "https://example.test"}, {"https://example.test:8443/", "https://example.test:8443"},
		{"http://example.test:80/", "http://example.test"}, {"http://127.0.0.1:19001/", "http://127.0.0.1:19001"},
		{"https://127.0.0.1", "https://127.0.0.1"}, {"http://[::1]:19001", "http://[::1]:19001"},
		{"https://[fd00:0:0::1]:443/", "https://[fd00::1]"}, {"http://Intranet:19001", "http://intranet:19001"},
	} {
		got, err := NormalizeTaskLinkOrigin(tc.input)
		if got != tc.want || err != nil {
			t.Errorf("%q => %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"example.test", "ftp://example.test", "//example.test", "https://example.test//", "https://example.test/..",
		"https://example.test/?", "https://example.test/#", "https://test@example.test", "https://example.test:0",
		"https://example.test:65800", "https://example.test:", "https://127.1", "http://::1", "http://[127.0.0.1]", "http://[fe80::1%25en0]",
		"https://e_xample.test", "https://-example.test", "http://1234", "https://example.test%2f",
		"https://example.test\\evil.test", "https://0x7f.0x1",
	} {
		if got, err := NormalizeTaskLinkOrigin(bad); err == nil {
			t.Errorf("accepted %q as %q", bad, got)
		}
	}
}
