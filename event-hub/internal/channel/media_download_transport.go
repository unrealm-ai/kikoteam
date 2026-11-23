package channel

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type mediaIPLookup func(context.Context, string, string) ([]netip.Addr, error)
type mediaSocketDial func(context.Context, string, string) (net.Conn, error)

var (
	// The Go resolver preserves A records as IPv4 and AAAA records as IPv6
	// instead of treating them interchangeably. The public policy rejects mapped AAAA.
	mediaDownloadLookup = (&net.Resolver{PreferGo: true, StrictErrors: true}).LookupNetIP
	mediaDownloadDial   = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
	// Separate pools keep public-policy requests from reusing private connections.
	mediaDownloadTransport        = newMediaDownloadTransport(mediaDownloadLookup, mediaDownloadDial, true)
	mediaPrivateDownloadTransport = newMediaDownloadTransport(mediaDownloadLookup, mediaDownloadDial, false)
)

func newMediaDownloadTransport(lookup mediaIPLookup, dial mediaSocketDial, validatePublicIP bool) *http.Transport {
	return &http.Transport{
		// No environment proxy: it would resolve and connect outside this policy.
		DialContext:           mediaDownloadDialer(lookup, dial, validatePublicIP),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func mediaDownloadDialer(lookup mediaIPLookup, dial mediaSocketDial, validatePublicIP bool) mediaSocketDial {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || (port != "443" && port != "80") {
			return nil, mediaDownloadURLInvalid
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second) // Match the default HTTP transport's dial budget.
		defer cancel()
		// Ask for an absolute DNS name to avoid local search-domain rewriting.
		ips, err := lookup(ctx, "ip", strings.TrimSuffix(host, ".")+".")
		if err != nil {
			return nil, mediaFailureCode(err, mediaDownloadFailed)
		}
		if len(ips) == 0 {
			return nil, mediaDownloadFailed
		}
		if validatePublicIP {
			for _, ip := range ips {
				if !publicMediaIP(ip) {
					return nil, mediaDownloadURLInvalid // Reject mixed public/non-public answers before dialing any of them.
				}
			}
		}
		failure := mediaDownloadFailed
		for _, ip := range ips {
			// Dial the resolved literal. The HTTP transport still uses the
			// original URL hostname for TLS SNI/verification and the Host header.
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			failure = mediaFailureCode(err, mediaDownloadFailed)
			if ctx.Err() != nil {
				return nil, mediaFailureCode(ctx.Err(), failure)
			}
		}
		return nil, failure
	}
}

var mediaGlobalIPv6 = netip.MustParsePrefix("2000::/3")

// In addition to IsGlobalUnicast, deny IANA special-purpose allocations
// (https://www.iana.org/assignments/iana-ipv4-special-registry and
// https://www.iana.org/assignments/iana-ipv6-special-registry).
// IPv6 outside global unicast is denied wholesale, including translation
// prefixes, local networks, and cloud metadata using unique-local space.
var mediaSpecialUsePrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("168.63.129.16/32"), // Azure virtual platform address.
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
}

func publicMediaIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.Zone() != "" || ip.Is4In6() || ip.Is6() && !mediaGlobalIPv6.Contains(ip) {
		return false
	}
	for _, denied := range mediaSpecialUsePrefixes {
		if denied.Contains(ip) {
			return false
		}
	}
	return true
}
