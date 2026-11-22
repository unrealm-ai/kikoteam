// Package controller adapts the platform HTTP contract to use cases.
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	mpcontroller "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/controller"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	Catalog          service.ResourceCatalog
	SharingPolicies  *service.GroupSharingService
	PeopleSelectors  *service.PeopleSelectorService
	GroupSelectors   *service.GroupSelectorService
	WecomGroups      *service.WecomGroupRefresh
	ChannelGroups    *service.ChannelGroupDiscovery
	AgentUsage       *service.AgentUsageService
	WebGroups        *service.WebGroupService
	WecomContacts    *service.WecomContactsService
	FeishuSetup      *service.FeishuSetupService
	DingTalkSetup    *service.DingTalkSetupService
	Official         *mp.Models
	Templates        *service.TemplateService
	Admin            *service.AdminService
	MailSettings     *service.MailSettingsService
	OAuthApps        *service.OAuthAppService
	Channels         *service.ChannelService
	Skills           *service.SkillService
	Plugins          *service.PluginService
	Packages         *service.PackageService
	Models           *service.ModelService
	Agents           *service.AgentService
	Auth             *service.AuthService
	Workspaces       *service.WorkspaceService
	Origin           string
	SecureCookies    bool
	Logger           *slog.Logger
	TrustedProxies   []netip.Prefix
	CodeLoginEnabled bool
}
type operation func(http.ResponseWriter, *http.Request) (any, int, error)

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	s.peopleSelectorRoutes(m)
	s.groupSelectorRoutes(m)
	s.groupSharingRoutes(m)
	s.agentUsageRoutes(m)
	s.templateRoutes(m)
	s.adminRoutes(m)
	s.mailSettingsRoutes(m)
	s.oauthAppRoutes(m)
	s.channelRoutes(m)
	s.wecomContactRoutes(m)
	s.feishuSetupRoutes(m)
	s.dingtalkSetupRoutes(m)
	s.modelRoutes(m)
	if s.Official != nil {
		(mpcontroller.ResourceAPI{Official: s.Official, Decode: decode, Paginate: pagination, Wrap: s.wrapModelProxy}).Register(m)
	}
	s.agentRoutes(m)
	s.webGroupRoutes(m)
	s.skillRoutes(m)
	s.catalogRoutes(m)
	s.pluginRoutes(m)
	s.packageRoutes(m)
	routes := map[string]operation{
		"POST /api/v1/workspaces/{workspace_id}/invitations/{invitation_id}/retry-delivery": s.inviteRetry,
		"GET /api/v1/auth/options":      s.authConfig,
		"POST /api/v1/auth/email-codes": s.code, "POST /api/v1/auth/sessions": s.login, "POST /api/v1/auth/registrations": s.register, "POST /api/v1/auth/password-reset": s.reset, "DELETE /api/v1/auth/session": s.logout, "GET /api/v1/me": s.me,
		"GET /api/v1/workspaces": s.list, "POST /api/v1/workspaces": s.create, "GET /api/v1/workspaces/{workspace_id}": s.get, "PATCH /api/v1/workspaces/{workspace_id}": s.update, "GET /api/v1/workspaces/{workspace_id}/members": s.members, "PATCH /api/v1/workspaces/{workspace_id}/members/{user_id}": s.memberUpdate, "DELETE /api/v1/workspaces/{workspace_id}/members/{user_id}": s.memberRemove, "GET /api/v1/workspaces/{workspace_id}/invitations": s.invites, "POST /api/v1/workspaces/{workspace_id}/invitations": s.inviteCreate, "POST /api/v1/workspaces/{workspace_id}/invitations/{invitation_id}/revoke": s.inviteRevoke, "GET /api/v1/invitations/{token}": s.inspect, "POST /api/v1/invitations/{token}/accept": s.accept,
	}
	for pattern, op := range routes {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
	m.HandleFunc("/", s.wrap("unmatched", func(http.ResponseWriter, *http.Request) (any, int, error) {
		return nil, 404, service.Fail("not_found", 404, "接口不存在")
	}))
	return m
}

func (s *Server) wrapModelProxy(route string, op mpcontroller.Operation) http.HandlerFunc {
	return s.wrap(route, func(_ http.ResponseWriter, r *http.Request) (any, int, error) {
		a, e := s.actor(r)
		if e != nil {
			return nil, 0, e
		}
		return op(r, a.ID)
	})
}
func (s *Server) wrap(route string, op operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := service.NewID()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "application/json")
		timeout, limit := requestPolicy(route)
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		var value any
		var status int
		var err error
		if r.Header.Get("Authorization") != "" {
			err = service.Fail("unsupported_credentials", 401, "此入口仅支持浏览器会话")
		} else if r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("X-CSRF-Protection") != "1" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.Origin)) {
			err = service.Fail("csrf_rejected", 403, "请求来源验证失败")
		} else if e := validIDs(r); e != nil {
			err = e
		} else {
			value, status, err = op(w, r)
		}
		code := ""
		if err != nil {
			status = 503
			code = "internal_error"
			message := "服务暂时不可用，请稍后重试"
			var e *service.Error
			var we *service.WorkspaceError
			if errors.As(err, &e) {
				status = e.Status
				code = e.Code
				message = e.Message
			} else if errors.As(err, &we) {
				status = we.Status
				code = we.Code
				message = we.Message
			} else if errors.Is(err, service.ErrWorkspaceMissing) {
				status = 404
				code = "not_found"
				message = "资源不存在"
			}
			value = map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}}
		}
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		if status != 204 {
			if e := json.NewEncoder(w).Encode(value); e != nil {
				code = "response_write_failed"
				status = 500
			}
		}
		if s.Logger != nil {
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			s.Logger.Log(ctx, level, "HTTP request completed", "event", "http.request", "trace_id", id, "route", route, "status", status, "error_code", code, "duration_ms", time.Since(start).Milliseconds())
		}
	}
}

// requestPolicy keeps large file imports bounded without widening unrelated APIs.
func requestPolicy(route string) (time.Duration, int64) {
	if strings.HasSuffix(route, "/assign") {
		// One batch can contain 100 packages, each with its own fixed tool set.
		return 60 * time.Second, 8 << 20
	}
	if strings.HasPrefix(route, "POST /api/v1/official-skills") || strings.HasSuffix(route, "/skills/{skill_id}/copy") {
		return 60 * time.Second, 16384
	}
	if strings.Contains(route, "agent-templates") {
		return 60 * time.Second, 256 << 10
	}
	if strings.Contains(route, "agents/from-template") {
		return 60 * time.Second, 128 << 10
	}
	if route == "POST /api/v1/workspaces/{workspace_id}/skill-imports" {
		return 60 * time.Second, 28 << 20
	}
	if route == "POST /api/v1/workspaces/{workspace_id}/model-profiles/test" {
		return 45 * time.Second, 16384
	}
	if strings.Contains(route, "/plugin-accounts/") {
		return 25 * time.Second, 65536
	}
	if route == "GET /api/v1/auth/callbacks/plugin-oauth" {
		// The callback exchanges the code, lists tools, and verifies the account upstream.
		return 60 * time.Second, 16384
	}
	if strings.Contains(route, "/agents") {
		return 20 * time.Second, 65536
	}
	return 20 * time.Second, 16384
}

func decode(r *http.Request, v any) error {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return service.Fail("invalid_request", 400, "请求内容过大")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil || fields == nil {
		return service.Fail("invalid_request", 400, "请求参数无效")
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return service.Fail("invalid_request", 400, "字段不能为 null")
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return service.Fail("invalid_request", 400, "请求参数无效")
	}
	return nil
}

func (s *Server) source(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	trusted := func(ip netip.Addr) bool {
		for _, p := range s.TrustedProxies {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return host
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(forwarded) > 20 {
		return host
	}
	for i := len(forwarded) - 1; i >= 0; i-- {
		ip, e := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if e != nil {
			return host
		}
		if !trusted(ip) {
			return ip.Unmap().String()
		}
	}
	return host
}

func token(r *http.Request) string {
	c, e := r.Cookie("kiko_session")
	if e != nil {
		return ""
	}
	return c.Value
}
func (s *Server) cookie(w http.ResponseWriter, t string) {
	age := 2592000
	if t == "" {
		age = -1
	}
	http.SetCookie(w, &http.Cookie{Name: "kiko_session", Value: t, Path: "/", MaxAge: age, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode})
}
func (s *Server) actor(r *http.Request) (service.Account, error) {
	return s.Auth.Me(r.Context(), token(r))
}
func (s *Server) identity(r *http.Request, a service.Account) (any, error) {
	p, e := s.Workspaces.List(r.Context(), a.ID, 1, 100)
	return map[string]any{"user": a, "workspaces": p.Items}, e
}
func (s *Server) code(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	var b struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
	}
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Auth.RequestCode(r.Context(), b.Email, b.Purpose, s.source(r))
	return v, 200, e
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) (any, int, error) {
	var b struct {
		Email     string `json:"email"`
		Method    string `json:"method"`
		Password  string `json:"password"`
		Code      string `json:"code"`
		Challenge string `json:"challenge_id"`
	}
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	a, t, e := s.Auth.Login(r.Context(), b.Email, b.Method, b.Password, b.Code, b.Challenge, s.source(r))
	if e != nil {
		return nil, 0, e
	}
	s.cookie(w, t)
	if fresh, err := s.Auth.Me(r.Context(), t); err == nil {
		a = fresh
	}
	return map[string]any{"user": a}, 200, nil
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) (any, int, error) {
	var b struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		Method    string `json:"method"`
		Password  string `json:"password"`
		Code      string `json:"code"`
		Challenge string `json:"challenge_id"`
	}
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	var a service.Account
	var t string
	var e error
	switch b.Method {
	case "password":
		if b.Code != "" || b.Challenge != "" {
			return nil, 0, service.Fail("invalid_request", 400, "密码注册不能携带验证码")
		}
		a, t, e = s.Auth.RegisterWithPassword(r.Context(), b.Email, b.Name, b.Password, s.source(r))
	case "", "code":
		a, t, e = s.Auth.Register(r.Context(), b.Email, b.Name, b.Password, b.Code, b.Challenge, s.source(r))
	default:
		return nil, 0, service.Fail("invalid_method", 400, "注册方式无效")
	}
	if e != nil {
		return nil, 0, e
	}
	s.cookie(w, t)
	if fresh, err := s.Auth.Me(r.Context(), t); err == nil {
		a = fresh
	}
	return map[string]any{"user": a}, 201, nil
}
func (s *Server) reset(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	var b struct {
		Email     string `json:"email"`
		Code      string `json:"code"`
		Challenge string `json:"challenge_id"`
		Password  string `json:"new_password"`
	}
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	return nil, 204, s.Auth.ResetPassword(r.Context(), b.Email, b.Code, b.Challenge, b.Password, s.source(r))
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) (any, int, error) {
	e := s.Auth.Logout(r.Context(), token(r))
	if e == nil {
		s.cookie(w, "")
	}
	return nil, 204, e
}
func (s *Server) me(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.identity(r, a)
	return v, 200, e
}
func page(r *http.Request) (int, int, error) {
	return pagination(r.URL.Query(), 50)
}
func pagination(query map[string][]string, defaultSize int) (int, int, error) {
	p, n := 1, defaultSize
	for k, vals := range query {
		if len(vals) != 1 {
			return 0, 0, service.Fail("invalid_query", 400, "查询参数重复")
		}
		if k != "page" && k != "page_size" {
			return 0, 0, service.Fail("invalid_query", 400, "未知查询参数")
		}
		v, e := strconv.Atoi(vals[0])
		if e != nil || v < 1 {
			return 0, 0, service.Fail("invalid_query", 400, "分页参数无效")
		}
		if k == "page" {
			p = v
		} else {
			n = v
		}
	}
	return p, n, nil
}
func (s *Server) list(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.List(r.Context(), a.ID, p, n)
	return v, 200, e
}
func (s *Server) create(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.Create(r.Context(), a.ID, b.Name, b.Description)
	return v, 201, e
}
func (s *Server) get(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.Get(r.Context(), a.ID, r.PathValue("workspace_id"))
	return v, 200, e
}
func (s *Server) update(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.WorkspacePatch
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.Update(r.Context(), a.ID, r.PathValue("workspace_id"), b)
	return v, 200, e
}
func (s *Server) members(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.Members(r.Context(), a.ID, r.PathValue("workspace_id"), p, n)
	return v, 200, e
}
func (s *Server) memberUpdate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b service.MemberPatch
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.UpdateMember(r.Context(), a.ID, r.PathValue("workspace_id"), r.PathValue("user_id"), b)
	return v, 200, e
}
func (s *Server) memberRemove(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	version, e := strconv.ParseInt(q.Get("version"), 10, 64)
	if e != nil || len(q["version"]) != 1 || len(q) != 1 {
		return nil, 0, service.Fail("invalid_version", 400, "需要版本号")
	}
	return nil, 204, s.Workspaces.RemoveMember(r.Context(), a.ID, r.PathValue("workspace_id"), r.PathValue("user_id"), version)
}
func (s *Server) invites(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.Invitations(r.Context(), a.ID, r.PathValue("workspace_id"), p, n)
	return v, 200, e
}
func (s *Server) inviteCreate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Email         string   `json:"email"`
		Emails        []string `json:"emails"`
		Role          string   `json:"role"`
		Kind          string   `json:"kind"`
		ExpiresInDays *int     `json:"expires_in_days"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if (b.Kind == "" || b.Kind == "email") && s.MailSettings == nil && !s.CodeLoginEnabled {
		return nil, 0, service.Fail("email_code_disabled", 403, "当前部署未启用邮箱验证码，请使用通用邀请链接")
	}
	days := 0
	if b.ExpiresInDays != nil {
		days = *b.ExpiresInDays
		if days < 1 || days > 180 {
			return nil, 0, service.Fail("invalid_expiry", 400, "有效期应为1至180天")
		}
	}
	if b.Role == "" {
		b.Role = "member"
	}
	v, e := s.Workspaces.CreateInvite(r.Context(), a.ID, r.PathValue("workspace_id"), service.InvitationOptions{Email: b.Email, Emails: b.Emails, Role: b.Role, Kind: b.Kind, ExpiresInDays: days})
	return v, 201, e
}
func (s *Server) inviteRevoke(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	return nil, 204, s.Workspaces.RevokeInvitation(r.Context(), a.ID, r.PathValue("workspace_id"), r.PathValue("invitation_id"))
}
func (s *Server) inspect(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	v, e := s.Workspaces.InspectInvitation(r.Context(), strings.TrimSpace(r.PathValue("token")))
	return v, 200, e
}
func (s *Server) accept(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Workspaces.AcceptInvitation(r.Context(), a.ID, r.PathValue("token"))
	return v, 200, e
}

func validIDs(r *http.Request) error {
	for _, name := range []string{"workspace_id", "user_id", "invitation_id", "model_profile_id", "agent_id", "skill_id", "skill_version_id", "version_id", "import_id", "plugin_id", "account_id", "package_id", "template_id", "model_id", "usage_id"} {
		v := r.PathValue(name)
		if v == "" {
			continue
		}
		if len(v) != 36 || v[8] != '-' || v[13] != '-' || v[18] != '-' || v[23] != '-' {
			return service.Fail("invalid_id", 400, "资源标识无效")
		}
		for i, c := range v {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				continue
			}
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return service.Fail("invalid_id", 400, "资源标识无效")
			}
		}
	}
	return nil
}

func (s *Server) authConfig(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	policy := service.MailPolicy{Enabled: s.CodeLoginEnabled, VerificationRequired: s.Auth.VerificationRequired}
	if s.MailSettings != nil {
		var err error
		policy, err = s.MailSettings.Policy(r.Context())
		if err != nil {
			return nil, 0, err
		}
	}
	return map[string]bool{"password_registration_enabled": !policy.VerificationRequired, "email_code_enabled": policy.Enabled, "registration_verification_required": policy.VerificationRequired}, 200, nil
}

func (s *Server) inviteRetry(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	enabled := s.CodeLoginEnabled
	if s.MailSettings != nil {
		policy, err := s.MailSettings.Policy(r.Context())
		if err != nil {
			return nil, 0, err
		}
		enabled = policy.Enabled
	}
	if !enabled {
		return nil, 0, service.Fail("email_code_disabled", 403, "当前部署未启用邮件发送")
	}
	var body struct {
		Acknowledge bool `json:"acknowledge_possible_duplicate"`
	}
	if e = decode(r, &body); e != nil {
		return nil, 0, e
	}
	result, e := s.Workspaces.RetryInvitationDelivery(r.Context(), a.ID, r.PathValue("workspace_id"), r.PathValue("invitation_id"), body.Acknowledge)
	return result, 200, e
}
