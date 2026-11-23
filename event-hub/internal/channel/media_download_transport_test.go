package channel

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"
)

func TestMediaPublicIPPolicy(t *testing.T) {
	for _, raw := range []string{
		"0.0.0.0", "10.1.2.3", "100.100.100.200", "127.0.0.1",
		"168.63.129.16", "169.254.169.254", "172.19.0.1", "192.0.0.9",
		"192.0.2.1", "192.31.196.1", "192.52.193.1", "192.88.99.2",
		"192.168.0.1", "192.175.48.1", "198.18.0.1", "198.51.100.3",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:8.8.8.8", "::ffff:127.0.0.1", "64:ff9b::a00:1",
		"100::1", "2001::1", "2001:db8::7", "2002:7f00:1::", "2620:4f:8000::1",
		"3fff:0::1", "5f00::1", "fd00:ec2::254", "fe80::1%en0", "ff00::1", "2606:4700::1%en0",
	} {
		if publicMediaIP(netip.MustParseAddr(raw)) {
			t.Errorf("permitted special-purpose address %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111", "2001:4860::8888"} {
		if !publicMediaIP(netip.MustParseAddr(raw)) {
			t.Errorf("denied public address %s", raw)
		}
	}
}

func TestMediaDialRejectsMixedDNSBeforeAnyConnection(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "fd00::1", "::ffff:8.8.8.8"} {
		lookups := 0
		dial := mediaDownloadDialer(
			func(_ context.Context, network, host string) ([]netip.Addr, error) {
				lookups++
				if network != "ip" || host != "files.dingtalk.com." {
					t.Fatal("lookup did not request the exact absolute host")
				}
				return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(blocked)}, nil
			},
			func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("mixed DNS answer permitted a dial")
				return nil, nil
			},
		)
		if _, err := dial(t.Context(), "tcp", "files.dingtalk.com:443"); !errors.Is(err, mediaDownloadURLInvalid) || lookups != 1 {
			t.Fatalf("mixed answer not safely denied: %v, lookups=%d", err, lookups)
		}
	}
}

func TestMediaDialPinsEachVettedLiteral(t *testing.T) {
	want := []string{"[2606:4700::1111]:443", "8.8.8.8:443"}
	lookups := 0
	var attempted []string
	dial := mediaDownloadDialer(
		func(context.Context, string, string) ([]netip.Addr, error) {
			lookups++
			return []netip.Addr{netip.MustParseAddr("2606:4700::1111"), netip.MustParseAddr("8.8.8.8")}, nil
		},
		func(_ context.Context, _ string, target string) (net.Conn, error) {
			attempted = append(attempted, target)
			return nil, errors.New("fixture unavailable")
		},
	)
	if _, err := dial(t.Context(), "tcp", "files.dingtalk.com:443"); !errors.Is(err, mediaDownloadFailed) || lookups != 1 ||
		!slices.Equal(attempted, want) {
		t.Fatalf("expected one lookup and only literal dials; attempts=%v lookups=%d error=%v", attempted, lookups, err)
	}
}

func TestMediaTransportDirectTLSAndChecksRedirectDial(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1234")
	t.Setenv("ALL_PROXY", "http://proxy.invalid:1234")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "files.example.com" || req.TLS == nil || req.TLS.ServerName != "files.example.com" {
			t.Error("literal dial lost original Host or TLS ServerName")
		}
		http.Redirect(w, req, "https://files.qq.com/fixture", http.StatusFound)
	}))
	server.StartTLS()
	defer server.Close()
	lookups := make(chan string, 4)
	dials := make(chan string, 4)
	transport := newMediaDownloadTransport(
		func(_ context.Context, _, host string) ([]netip.Addr, error) {
			lookups <- host
			ips := []netip.Addr{netip.MustParseAddr("8.8.8.8")}
			if host == "files.qq.com." {
				ips = append(ips, netip.MustParseAddr("192.168.1.1"))
			}
			return ips, nil
		},
		func(ctx context.Context, network, target string) (net.Conn, error) {
			dials <- target
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	)
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinVersion: tls.VersionTLS12}
	if transport.Proxy != nil {
		t.Fatal("media transport enabled a proxy")
	}
	client := &http.Client{Transport: transport, CheckRedirect: mediaURLPolicy("wecom").checkRedirect}
	resp, err := client.Get("https://files.example.com/fixture")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, mediaDownloadURLInvalid) || len(lookups) != 2 || len(dials) != 1 {
		t.Fatalf("expected private redirect dial denied; lookups=%d dials=%d error=%v", len(lookups), len(dials), err)
	}
	if <-lookups != "files.example.com." || <-lookups != "files.qq.com." || <-dials != "8.8.8.8:443" {
		t.Fatal("transport did not vet each hop and pin the safe literal")
	}
	resp, err = client.Get("https://wrong-host.dingtalk.com/fixture")
	if resp != nil {
		_ = resp.Body.Close()
	}
	var tlsErr *tls.CertificateVerificationError
	if !errors.As(err, &tlsErr) {
		t.Fatal("literal dial disabled verification of the original hostname")
	}
}

func TestDingMediaResolvedGuardedTransportRejectsPrivateAndMixedDNS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://mixed-destination.example.com/file?new=fixture", http.StatusFound)
	}))
	defer server.Close()
	lookups := make(chan string, 4)
	dials := make(chan string, 4)
	transport := newMediaDownloadTransport(
		func(_ context.Context, _, host string) ([]netip.Addr, error) {
			lookups <- host
			switch host {
			case "private.example.com.":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			case "mixed-destination.example.com.":
				return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.1.1")}, nil
			default:
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
		},
		func(ctx context.Context, network, target string) (net.Conn, error) {
			dials <- target
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	)
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinVersion: tls.VersionTLS12}
	_, err := fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, "http://private.example.com/file?signature=fixture", "", transport)
	if !errors.Is(err, mediaDownloadURLInvalid) || len(lookups) != 1 || len(dials) != 0 {
		t.Fatalf("private resolver answer reached dial: lookups=%d dials=%d error=%v", len(lookups), len(dials), err)
	}
	if <-lookups != "private.example.com." {
		t.Fatal("unexpected initial resolution")
	}
	_, err = fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, "https://permitted.example.com/file?signature=fixture", "", transport)
	if !errors.Is(err, mediaDownloadURLInvalid) || len(lookups) != 2 || len(dials) != 1 {
		t.Fatalf("mixed DNS redirect reached dial: lookups=%d dials=%d error=%v", len(lookups), len(dials), err)
	}
	if <-lookups != "permitted.example.com." || <-lookups != "mixed-destination.example.com." || <-dials != "8.8.8.8:443" {
		t.Fatal("direct initial and redirect dials were not individually vetted")
	}
}

func TestDingMediaResolverHTTPUsesGuardedPort80WithoutCredentials(t *testing.T) {
	const host = "files.public.example"
	const target = "/path%2Ffile?fixture=%2B%3d&label=a+b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || r.Host != host || r.RequestURI != target || r.Method != http.MethodGet {
			t.Error("original HTTP authority or signed request target changed")
		}
		for _, name := range []string{"Authorization", "Cookie", "X-Acs-Dingtalk-Access-Token", "Referer"} {
			if r.Header.Get(name) != "" {
				t.Error("download received an unrelated credential or Referer")
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	lookedUp := make(chan string, 1)
	dialed := make(chan string, 1)
	transport := newMediaDownloadTransport(
		func(_ context.Context, _, requestedHost string) ([]netip.Addr, error) {
			lookedUp <- requestedHost
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed <- address
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	)
	defer transport.CloseIdleConnections()
	_, err := fetchMediaURLWithTransport(t.Context(), resolvedDingMediaPolicy, "http://"+host+target, "", transport)
	if err != nil || len(lookedUp) != 1 || len(dialed) != 1 {
		t.Fatalf("guarded HTTP download failed: %v", err)
	}
	if <-lookedUp != host+"." || <-dialed != "8.8.8.8:80" {
		t.Fatal("HTTP downloader did not use a vetted public literal on port 80")
	}
}
