package service

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type TaskLinkSettings struct {
	Version int64  `json:"version"`
	Origin  string `json:"origin"`
	Enabled bool   `json:"enabled"`
}

type TaskLinkSettingsPatch struct {
	ExpectedVersion *int64  `json:"expected_version"`
	Origin          *string `json:"origin"`
	Enabled         *bool   `json:"enabled"`
}

type TaskLinkSettingsRepository interface {
	ReadAdmin(context.Context, string) (TaskLinkSettings, error)
	SaveAdmin(context.Context, string, int64, string, bool) (TaskLinkSettings, error)
}

type TaskLinkSettingsService struct{ Store TaskLinkSettingsRepository }

func (s *TaskLinkSettingsService) Get(ctx context.Context, actor string) (TaskLinkSettings, error) {
	return s.Store.ReadAdmin(ctx, actor)
}

func (s *TaskLinkSettingsService) Save(ctx context.Context, actor string, p TaskLinkSettingsPatch) (TaskLinkSettings, error) {
	if p.ExpectedVersion == nil || *p.ExpectedVersion < 0 || p.Origin == nil || p.Enabled == nil {
		return TaskLinkSettings{}, Fail("invalid_request", 400, "请提供当前版本、任务链接域名和发卡设置")
	}
	origin, err := NormalizeTaskLinkOrigin(*p.Origin)
	if err != nil {
		return TaskLinkSettings{}, err
	}
	return s.Store.SaveAdmin(ctx, actor, *p.ExpectedVersion, origin, *p.Enabled)
}

var taskLinkDNSLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var taskLinkNumericLabel = regexp.MustCompile(`^(0x[0-9a-f]+|[0-9]+)$`)

// NormalizeTaskLinkOrigin accepts an explicitly selected HTTP or HTTPS origin, never
// a path, credential, query, or fragment. It does not resolve or contact it.
func NormalizeTaskLinkOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	bad := Fail("invalid_task_link_origin", 400, "任务链接地址必须以 http:// 或 https:// 开头，不能包含路径、查询或凭据")
	if len(value) > 270 || strings.ContainsAny(value, "\\%?#") {
		return "", bad
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return "", bad
	}
	host, ipv6, ok := normalizedTaskLinkHost(u.Host)
	if !ok {
		return "", bad
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", bad
		}
		if (u.Scheme == "http" && n != 80) || (u.Scheme == "https" && n != 443) {
			return u.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(n)), nil
		}
	}
	if ipv6 {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func normalizedTaskLinkHost(authority string) (host string, ipv6, ok bool) {
	if strings.HasSuffix(authority, ":") {
		return "", false, false
	}
	u := url.URL{Host: authority}
	host = strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is6() != strings.HasPrefix(authority, "[") {
			return "", false, false
		}
		return ip.String(), ip.Is6(), true
	}
	if strings.HasPrefix(authority, "[") || strings.Contains(host, ":") {
		return "", false, false
	}
	return host, false, validTaskLinkHost(host)
}

func validTaskLinkHost(host string) bool {
	if len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	// Clients can interpret numeric final labels as noncanonical IPv4. Permit
	// addresses only when the strict parser above accepted them.
	if taskLinkNumericLabel.MatchString(labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels {
		if !taskLinkDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}
