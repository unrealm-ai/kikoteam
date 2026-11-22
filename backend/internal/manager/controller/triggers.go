package controller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const nativeEventRoute = "POST /api/triggers/events/{trigger_id}"

func (s *Server) triggerRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/triggers"
	m.HandleFunc("GET "+base, s.wrap("listTriggers", s.listTriggers))
	m.HandleFunc("POST "+base, s.wrap("createTrigger", s.saveTrigger))
	m.HandleFunc("GET "+base+"/{trigger_id}", s.wrap("getTrigger", s.getTrigger))
	m.HandleFunc("PUT "+base+"/{trigger_id}", s.wrap("saveTrigger", s.saveTrigger))
	m.HandleFunc("GET "+base+"/{trigger_id}/credentials", s.wrap("listTriggerCredentials", s.triggerCredentials))
	m.HandleFunc("POST "+base+"/{trigger_id}/credentials", s.wrap("createTriggerCredential", s.triggerCredentialCreate))
	m.HandleFunc("DELETE "+base+"/{trigger_id}/credentials/{key_id}", s.wrap("revokeTriggerCredential", s.triggerCredentialRevoke))
	m.HandleFunc("GET "+base+"/{trigger_id}/events", s.wrap("listTriggerEvents", s.triggerEvents))
	m.HandleFunc("POST /api/triggers/webhook/{trigger_id}", s.triggerWebhook)
	m.HandleFunc("POST /api/triggers/github/{trigger_id}", s.triggerWebhook)
	m.HandleFunc(nativeEventRoute, s.triggerWebhook)
}
func (s *Server) triggerEndpoint(v *service.Trigger) {
	var cfg struct {
		Adapter string `json:"adapter"`
	}
	if v.Kind != "webhook" || json.Unmarshal(v.Config, &cfg) != nil {
		return
	}
	var path string
	switch cfg.Adapter {
	case "event":
		path = "events"
	case "github_issue":
		path = "github"
	case "generic":
		path = "webhook"
	default:
		return
	}
	v.Endpoint = strings.TrimRight(s.Origin, "/") + "/api/triggers/" + path + "/" + v.ID
}
func (s *Server) listTriggers(r *http.Request, actor string) (any, int, error) {
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	v, e := st.ListTriggers(r.Context(), workspace(r), actor, r.PathValue("agent_id"))
	for i := range v {
		s.triggerEndpoint(&v[i])
	}
	return map[string]any{"items": v}, 200, e
}
func (s *Server) getTrigger(r *http.Request, actor string) (any, int, error) {
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	v, e := st.GetTrigger(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"))
	s.triggerEndpoint(&v)
	return v, 200, e
}
func (s *Server) saveTrigger(r *http.Request, actor string) (any, int, error) {
	var q service.TriggerWrite
	if e := decode(r, &q); e != nil {
		return nil, 400, e
	}
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	v, e := st.SaveTrigger(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"), q)
	s.triggerEndpoint(&v)
	if r.Method == http.MethodPost {
		return struct {
			service.Trigger
			Credential *service.TriggerCredential `json:"initial_credential,omitempty"`
		}{v, v.InitialCredential}, http.StatusCreated, e
	}
	return v, http.StatusOK, e
}
func (s *Server) triggerCredentials(r *http.Request, actor string) (any, int, error) {
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	v, e := st.TriggerCredentials(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"))
	return map[string]any{"items": v}, 200, e
}
func (s *Server) triggerCredentialCreate(r *http.Request, actor string) (any, int, error) {
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	var q struct {
		Token *string `json:"signing_token"`
	}
	if err := decodeJSONBody(r, &q, true); err != nil {
		return nil, 400, err
	}
	if q.Token != nil {
		v, err := st.ImportTriggerCredential(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"), *q.Token)
		return v, 201, err
	}
	v, e := st.CreateTriggerCredential(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"))
	return v, 201, e
}
func (s *Server) triggerCredentialRevoke(r *http.Request, actor string) (any, int, error) {
	st, e := s.Service.Triggers()
	if e != nil {
		return nil, 503, e
	}
	e = st.RevokeTriggerCredential(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"), r.PathValue("key_id"))
	return map[string]bool{"revoked": e == nil}, 200, e
}
func (s *Server) triggerEvents(r *http.Request, actor string) (any, int, error) {
	if e := taskQuery(r, "cursor", "limit"); e != nil {
		return nil, 400, e
	}
	n, e := limit(r)
	if e != nil {
		return nil, 400, e
	}
	v, e := s.Service.ListTriggerEvents(r.Context(), workspace(r), actor, r.PathValue("agent_id"), r.PathValue("trigger_id"), r.URL.Query().Get("cursor"), n)
	return v, 200, e
}

// This route accepts no browser authentication or caller-supplied employee identity.
func (s *Server) triggerWebhook(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var out any
	var err error
	status := 202
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		err = service.Fail("invalid_trigger_route", 400, "Hook路径无效")
	}
	if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
		err = service.Fail("trigger_authentication_failed", 401, "不接受浏览器身份")
	}
	if err == nil && r.Pattern != nativeEventRoute && !s.secureTriggerRequest(r) {
		err = service.Fail("trigger_https_required", 400, "Hook需要HTTPS")
	}
	if err == nil {
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
		err = e
		if err == nil {
			var receipt service.TriggerReceipt
			var ignored bool
			receipt, ignored, err = s.receiveHook(r, body)
			if ignored && err == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			out = receipt
			if receipt.Duplicate {
				status = http.StatusOK
			}
		}
	}
	if err != nil {
		status = 503
		code := "dependency_unavailable"
		message := "服务暂时不可用"
		var e *service.Error
		if errors.As(err, &e) {
			status = e.Status
			code = e.Code
			message = e.Message
			if status == http.StatusTooManyRequests && code == "trigger_rate_limited" {
				w.Header().Set("Retry-After", "1")
			}
		}
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			status = 413
			code = "body_too_large"
			message = "消息过大"
		}
		out = map[string]any{"error": map[string]string{"code": code, "message": message}}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out) //nolint:errcheck // Response stream may be closed.
}

func (s *Server) receiveHook(r *http.Request, body []byte) (service.TriggerReceipt, bool, error) {
	st, err := s.Service.Triggers()
	if err != nil {
		return service.TriggerReceipt{}, false, err
	}
	id, now := r.PathValue("trigger_id"), time.Now().UTC()
	switch {
	case r.Pattern == nativeEventRoute:
		return st.ReceiveNativeEvent(r.Context(), id, service.EventRequest{Header: r.Header, Body: body}, now)
	case strings.HasPrefix(r.URL.Path, "/api/triggers/github/"):
		return st.ReceiveTriggerGithub(r.Context(), id, r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-GitHub-Event"), r.Header.Get("X-GitHub-Delivery"), body, now)
	default:
		v, e := st.ReceiveTriggerWebhook(r.Context(), id, r.Header.Get("X-Key-Id"), r.Header.Get("X-Timestamp"), r.Header.Get("X-Signature"), body, now)
		return v, false, e
	}
}

func (s *Server) secureTriggerRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remote, e := netip.ParseAddrPort(r.RemoteAddr)
	if e != nil {
		return false
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	for _, prefix := range s.TrustedProxies {
		if prefix.Contains(remote.Addr()) {
			return true
		}
	}
	return false
}
