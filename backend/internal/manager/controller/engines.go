package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/assistant"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) internalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp/plugin-connections/{connection_id}", s.pluginRelay)
	mux.HandleFunc("POST /api/internal/v1/task-results/callback", s.internal(s.taskResult))
	mux.HandleFunc("POST /api/internal/v1/sessions/{session_id}/capability-calls", s.internalCredential(s.capabilityCall, s.capabilityCredential))
	mux.HandleFunc("GET /api/internal/v1/engines/{engine_id}/sandboxes", s.internal(s.sandboxes))
	mux.HandleFunc("POST /api/internal/v1/sandbox-instances/{sandbox_id}/state", s.internal(s.sandboxState))
	mux.HandleFunc("GET /api/internal/v1/sandbox-instances/{sandbox_id}/config", s.internalCredential(s.sandboxConfig, func(r *http.Request) string {
		return s.runnerToken(r.PathValue("sandbox_id"), r.URL.Query().Get("engine_id"))
	}))
	for pattern, op := range map[string]func(*http.Request) (any, int, error){"POST /api/internal/v1/engines": s.registerEngine, "POST /api/internal/v1/engines/{engine_id}/heartbeat": s.heartbeatEngine, "GET /api/internal/v1/engines/{engine_id}/assignments": s.assignments, "GET /api/internal/v1/sessions/{session_id}/harness-config": s.harnessConfig, "POST /api/internal/v1/sessions/{session_id}/assignments/{assignment_id}/release": s.releaseAssignment} {
		mux.HandleFunc(pattern, s.internal(op))
	}
}
func (s *Server) internal(op func(*http.Request) (any, int, error)) http.HandlerFunc {
	return s.internalCredential(op, func(*http.Request) string { return s.EngineToken })
}
func (s *Server) internalCredential(op func(*http.Request) (any, int, error), credential func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		requestID := service.NewID()
		w.Header().Set("X-Request-ID", requestID)
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || len(s.EngineToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+credential(r))) != 1 {
			w.WriteHeader(401)
			if err := json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthenticated", "message": "内部服务身份无效", "request_id": requestID}}); err != nil && s.Logger != nil {
				s.Logger.Debug("Internal response closed", "event", "http.closed")
			}
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var value any
		status := 200
		var err error
		if s.Engines == nil {
			err = service.Fail("dependency_unavailable", 503, "执行模块未配置")
		} else {
			value, status, err = op(r)
		}
		if err != nil {
			status = 503
			code, message := "dependency_unavailable", "执行依赖暂时不可用"
			var e *service.Error
			var pe *platform.Error
			if errors.As(err, &e) {
				status = e.Status
				code = e.Code
				message = e.Message
			} else if errors.As(err, &pe) {
				status, code, message = pe.Status, pe.Code, pe.Message
			}
			value = map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": requestID}}
			if s.Logger != nil {
				s.Logger.Warn("Engine operation failed", "event", "engine.request_failed", "route", r.Pattern, "error_code", code, "trace_id", requestID)
			}
		}
		w.WriteHeader(status)
		if err = json.NewEncoder(w).Encode(value); err != nil && s.Logger != nil {
			s.Logger.Debug("Internal response closed", "event", "http.closed")
		}
	}
}
func (s *Server) registerEngine(r *http.Request) (any, int, error) {
	var v service.Engine
	if err := decode(r, &v); err != nil {
		return nil, 400, err
	}
	u, err := url.Parse(v.Endpoint)
	if !service.ValidID(v.ID) || len(v.HostID) < 1 || len(v.HostID) > 256 || v.Capacity < 1 || v.Capacity > 1000 || err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, 400, service.Fail("invalid_argument", 400, "Engine 注册参数无效")
	}
	err = s.Engines.RegisterEngine(r.Context(), v)
	return v, 201, err
}
func (s *Server) heartbeatEngine(r *http.Request) (any, int, error) {
	var req struct {
		Seq       int64 `json:"heartbeat_seq,string"`
		Accepting bool  `json:"accepting"`
	}
	if err := decode(r, &req); err != nil {
		return nil, 400, err
	}
	id := r.PathValue("engine_id")
	if !service.ValidID(id) || req.Seq < 1 {
		return nil, 400, service.Fail("invalid_argument", 400, "心跳身份或序号无效")
	}
	err := s.Engines.HeartbeatEngine(r.Context(), id, req.Seq, req.Accepting)
	return map[string]bool{"accepted": err == nil}, 200, err
}
func (s *Server) assignments(r *http.Request) (any, int, error) {
	id := r.PathValue("engine_id")
	if !service.ValidID(id) {
		return nil, 400, service.Fail("invalid_argument", 400, "Engine 标识无效")
	}
	v, err := s.Engines.Assignments(r.Context(), id)
	return map[string]any{"items": v}, 200, err
}
func (s *Server) harnessConfig(r *http.Request) (any, int, error) {
	q := r.URL.Query()
	generation, err := strconv.ParseInt(q.Get("generation"), 10, 64)
	if err != nil || generation < 1 || !service.ValidID(q.Get("engine_id")) || !service.ValidID(q.Get("assignment_id")) || !service.ValidID(q.Get("input_id")) || !service.ValidID(r.PathValue("session_id")) {
		return nil, 400, service.Fail("invalid_argument", 400, "执行身份参数无效")
	}
	v, actor, err := s.Engines.HarnessConfig(r.Context(), q.Get("engine_id"), r.PathValue("session_id"), q.Get("assignment_id"), generation, q.Get("input_id"))
	if err != nil {
		return nil, 0, err
	}
	if s.ResolveModelKey == nil {
		return nil, 503, service.Fail("configuration_unusable", 503, "模型配置未接入")
	}
	ctx := v.ExecutionContext(r.Context())
	if v.Configuration.PlatformModelID != "" {
		if s.ResolveOfficialModel == nil {
			return nil, 503, service.Fail("configuration_unusable", 503, "官方模型未接入")
		}
		v.APIKey, err = s.ResolveOfficialModel(ctx, v.WorkspaceID, actor, v.AgentID, &v.Configuration)
	} else {
		v.APIKey, err = s.ResolveModelKey(ctx, v.WorkspaceID, actor, v.Configuration)
	}
	if err == nil && v.Mode == "task" && s.Runtime != nil {
		v.Resources, err = s.Runtime.Resolve(ctx, v.WorkspaceID, actor, v.AgentID, v.Configuration.Capabilities, v.SessionID, true)
		if err == nil {
			for i := range v.Resources.Plugins {
				p := &v.Resources.Plugins[i]
				if len(p.Headers) == 0 {
					continue
				}
				token, e := signRelay(s.EngineToken, p.AccountID, v.InputID, service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
				if e != nil {
					return nil, 0, e
				}
				p.Endpoint = "/mcp/plugin-connections/" + p.AccountID
				p.Headers = map[string]string{"Authorization": "Bearer " + token}
			}
			v.CapabilityToken, err = service.SignCapability(s.EngineToken, service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
		}
	}
	if err == nil && v.Mode == "task" {
		err = s.signTaskContextCredentials(&v, service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
	}
	if err == nil && v.Mode == "chat" {
		if v.ChatCatalogChannel && !v.ConfirmationOnly {
			if settings, ok := s.Service.Store.(interface {
				TaskDetailLinksEnabled(context.Context) (bool, error)
			}); ok {
				enabled, settingsErr := settings.TaskDetailLinksEnabled(ctx)
				v.TaskDetailCardEnabled = settingsErr == nil && enabled
			}
		}
		err = s.signChatCredentials(&v, q.Get("input_id"), service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
	}
	if err == nil && v.HumanTools {
		err = s.signHumanToolCredentials(&v, service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
	}
	if err == nil && v.WorkRole != "" {
		err = s.signWorkCredentials(&v, service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation})
	}
	if err == nil && !v.ReportOnly && !v.ConfirmationOnly {
		id := service.DelegationIdentity{SessionID: v.SessionID, EngineID: q.Get("engine_id"), AssignmentID: q.Get("assignment_id"), Generation: generation}
		if v.EmployeeManagementEligible {
			v.EmployeeManagementToken, err = assistant.Token(s.EngineToken, v.InputID, "platform", id)
		}
		if err == nil && s.AssistantToolAllowed != nil {
			err = s.assistantCredential(ctx, &v, actor, id)
		}
	}
	if err == nil {
		service.ApplyHarnessPrompt(&v)
	}
	return v, 200, err
}

func (s *Server) signChatCredentials(v *service.HarnessConfig, input string, id service.DelegationIdentity) error {
	var err error
	if !v.ConfirmationOnly {
		v.DelegationToken, err = service.SignDelegation(s.EngineToken, id)
		if err != nil {
			return err
		}
	}
	grant := service.ChatToolCatalogGrant{Identity: id, InputID: input, Family: service.ChatToolCatalogDelegation, Channel: v.ChatCatalogChannel, Group: v.ChatCatalogGroup}
	v.ChatDelegationCatalogToken, err = service.SignChatToolCatalog(s.EngineToken, grant)
	if err == nil && v.ChatCatalogPrivate {
		grant.Family = service.ChatToolCatalogHuman
		v.ChatHumanCatalogToken, err = service.SignChatToolCatalog(s.EngineToken, grant)
	}
	return err
}

func (s *Server) signTaskContextCredentials(v *service.HarnessConfig, id service.DelegationIdentity) error {
	var err error
	if v.ConversationID != "" {
		v.AttachmentToken, err = service.SignInputAttachment(s.EngineToken, id)
		if err == nil && v.TriggerID == "" {
			v.GroupHistoryToken, err = service.SignGroupHistory(s.EngineToken, id)
		}
	}
	if err == nil && v.TaskSpaceID != "" {
		v.TaskSpaceHistoryToken, err = service.SignTaskSpaceHistory(s.EngineToken, service.TaskSpaceHistoryIdentity{DelegationIdentity: id, InputID: v.InputID})
	}
	return err
}

func (s *Server) signWorkCredentials(v *service.HarnessConfig, id service.DelegationIdentity) error {
	var err error
	v.WorkLocalToken, err = service.SignWorkTool(s.EngineToken, "local", v.InputID, id)
	if err != nil {
		return err
	}
	v.WorkMCPToken, err = service.SignWorkTool(s.EngineToken, "mcp-"+v.WorkRole, v.InputID, id)
	return err
}

func (s *Server) signHumanToolCredentials(v *service.HarnessConfig, identity service.DelegationIdentity) error {
	token, err := service.SignHumanRuntime(s.EngineToken, v.InputID, identity)
	if err != nil {
		return err
	}
	v.HumanToolsToken = token
	v.HumanMCPToken, err = service.SignHumanMCP(s.EngineToken, v.Mode, v.InputID, identity)
	return err
}

func (s *Server) releaseAssignment(r *http.Request) (any, int, error) {
	var req struct {
		EngineID   string `json:"engine_id"`
		Generation int64  `json:"generation,string"`
	}
	if err := decode(r, &req); err != nil {
		return nil, 400, err
	}
	if !service.ValidID(req.EngineID) || !service.ValidID(r.PathValue("session_id")) || !service.ValidID(r.PathValue("assignment_id")) || req.Generation < 1 {
		return nil, 400, service.Fail("invalid_argument", 400, "分配身份无效")
	}
	err := s.Engines.Release(r.Context(), req.EngineID, r.PathValue("session_id"), r.PathValue("assignment_id"), req.Generation)
	return map[string]bool{"released": err == nil}, 200, err
}

func (s *Server) assistantCredential(ctx context.Context, v *service.HarnessConfig, actor string, id service.DelegationIdentity) error {
	if v.Mode != "task" || v.Configuration.Capabilities == nil {
		return nil
	}
	const key = "demo"
	ok, err := s.AssistantToolAllowed(ctx, v.WorkspaceID, actor, v.AgentID, key, v.Configuration.Capabilities)
	if err != nil || !ok {
		return err
	}
	v.AssistantToolSet = key
	v.AssistantMCPToken, err = assistant.Token(s.EngineToken, v.InputID, key, id)
	return err
}
