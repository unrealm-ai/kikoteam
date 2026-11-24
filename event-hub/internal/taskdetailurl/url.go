// Package taskdetailurl validates and formats an authorized Manager URL without
// resolving or fetching it. The same link shape is used for history validation.
package taskdetailurl

import (
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const Label = "查看任务详情"
const prefix = "[" + Label + "]("

var (
	pathPattern = regexp.MustCompile(`^/tasks/[A-Za-z0-9_-]{20,256}$`)
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func Valid(raw string) bool {
	if len(raw) > 1024 || strings.ContainsAny(raw, "#\\\r\n\t ") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		strings.ContainsAny(u.Host, "()<>%") || strings.HasSuffix(u.Host, ":") ||
		u.RawQuery != "" || u.ForceQuery || !pathPattern.MatchString(u.EscapedPath()) {
		return false
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() {
			return false
		}
	} else if !hostPattern.MatchString(host) {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		return err == nil && n > 0 && n <= 65535
	}
	// Manager authorized this configured origin. It is only rendered as a
	// navigation target; the Hub never resolves or fetches the bearer link.
	return true
}

// Link renders a previously validated URL as a message hyperlink.
func Link(raw string) string { return prefix + raw + ")" }

// MatchesAppended verifies that delivered contains the exact original body and
// a single valid task-details link. The URL is never returned or persisted here.
func MatchesAppended(original, delivered string) bool {
	raw, ok := strings.CutPrefix(delivered, original+"\n\n"+prefix)
	if !ok {
		return false
	}
	raw, ok = strings.CutSuffix(raw, ")")
	return ok && Valid(raw)
}
