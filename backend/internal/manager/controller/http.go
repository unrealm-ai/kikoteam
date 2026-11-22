// Package controller adapts authenticated HTTP operations to Manager use cases.
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Server struct {
	TrustedProxies       []netip.Prefix
	Service              *service.Service
	Authenticate         func(context.Context, string) (string, error)
	Origin               string
	Logger               *slog.Logger
	Engines              service.EngineStore
	EngineToken          string
	ResolveModelKey      func(context.Context, string, string, service.Configuration) (string, error)
	ResolveOfficialModel func(context.Context, string, string, string, *service.Configuration) (string, error)
	Runtime              *platform.RuntimeService
}

type operation func(*http.Request, string) (any, int, error)
type receipt struct {
	Value    any
	Replay   bool
	Location string
}

func (s *Server) Routes(mux *http.ServeMux) {
	s.WebGroupRoutes(mux)
	s.humanRoutes(mux)
	s.workRoutes(mux)
	s.workBoardRoutes(mux)
	s.taskSpaceRoutes(mux)
	s.triggerRoutes(mux)
	s.triggerGroupCandidateRoutes(mux)
	s.TriggerNotificationReadRoutes(mux)
	base := "/api/v1/workspaces/{workspace_id}/conversations"
	mux.HandleFunc("POST "+base+"/web-direct", s.wrap("resolveWebConversation", s.resolveWebConversation))
	mux.HandleFunc("POST "+base, s.wrap("createConversation", s.createConversation))
	mux.HandleFunc("GET "+base, s.wrap("listConversations", s.listConversations))
	mux.HandleFunc("GET "+base+"/{conversation_id}", s.wrap("getConversation", s.getConversation))
	mux.HandleFunc("POST "+base+"/{conversation_id}/archive", s.wrap("archiveConversation", s.archiveConversation))
	files := base + "/{conversation_id}/files"
	mux.HandleFunc("GET "+files, s.wrap("listConversationFiles", s.listProjectFiles))
	mux.HandleFunc("POST "+files, s.wrap("uploadConversationFile", s.uploadProjectFile))
	mux.HandleFunc("GET "+files+"/content", s.wrap("downloadConversationFile", s.downloadProjectFile))
	s.sessionRoutes(mux)
	s.taskRoutes(mux)
	s.internalRoutes(mux)
}

func (s *Server) wrap(route string, op operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := service.NewID()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Referrer-Policy", "no-referrer")
		timeout, bodyLimit := 20*time.Second, int64(1<<20)
		if route == "uploadConversationFile" {
			timeout = 2 * time.Minute
			bodyLimit = service.ProjectUploadLimit + (1 << 20)
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		var value any
		var err error
		status := 200
		actor, err := s.authorize(r)
		if err == nil && route == "uploadConversationFile" {
			// The HTTP server's normal socket deadlines are shorter than an upload.
			rc := http.NewResponseController(w)
			deadline := start.Add(timeout)
			if e := rc.SetReadDeadline(deadline); !errors.Is(e, http.ErrNotSupported) {
				err = e
			}
			if e := rc.SetWriteDeadline(deadline.Add(5 * time.Second)); !errors.Is(e, http.ErrNotSupported) {
				err = errors.Join(err, e)
			}
		}
		if err == nil {
			value, status, err = op(r, actor)
		}
		code := ""
		if err != nil {
			status = 503
			code = "dependency_unavailable"
			message := "服务暂时不可用"
			var e *service.Error
			var pe *platform.Error
			if errors.As(err, &e) {
				status = e.Status
				code = e.Code
				message = e.Message
			} else if errors.As(err, &pe) {
				status = pe.Status
				code = pe.Code
				message = pe.Message
			}
			value = map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}}
		}
		if v, ok := value.(receipt); ok {
			value = v.Value
			if v.Replay {
				w.Header().Set("Idempotency-Replayed", "true")
			}
			if v.Location != "" {
				w.Header().Set("Location", v.Location)
			}
		}
		if d, ok := value.(projectDownload); ok {
			d.serve(w, r)
			return
		}
		w.WriteHeader(status)
		if e := json.NewEncoder(w).Encode(value); e != nil {
			code = "response_write_failed"
		}
		if s.Logger != nil {
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			s.Logger.Log(ctx, level, "Manager HTTP request completed", "event", "http.request", "trace_id", id, "route", route, "status", status, "error_code", code, "duration_ms", time.Since(start).Milliseconds())
		}
	}
}

func decode(r *http.Request, v any) error {
	return decodeJSONBody(r, v, false)
}

func decodeJSONBody(r *http.Request, v any, allowEmpty bool) error {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return service.Fail("invalid_argument", 400, "请求正文过大")
	}
	if allowEmpty && len(data) == 0 {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") || uniqueKeys(json.NewDecoder(bytes.NewReader(data)), 0) != nil {
		return service.Fail("invalid_argument", 400, "请求必须是字段不重复的 JSON 对象")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return service.Fail("invalid_argument", 400, "请求正文无效")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return service.Fail("invalid_argument", 400, "请求只能包含一个 JSON 对象")
	}
	return nil
}

func uniqueKeys(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting limit")
	}
	v, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := v.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate JSON field")
			}
			seen[name] = true
			if err = uniqueKeys(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = uniqueKeys(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON")
	}
	_, err = d.Token()
	return err
}
func (s *Server) authorize(r *http.Request) (string, error) {
	if r.Header.Get("Authorization") != "" {
		return "", service.Fail("unsupported_credentials", 401, "此入口使用登录会话")
	}
	if r.Method != "GET" && (r.Header.Get("X-CSRF-Protection") != "1" || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.Origin) {
		return "", service.Fail("csrf_rejected", 403, "请求来源验证失败")
	}
	cookie, err := r.Cookie("kiko_session")
	if err != nil {
		return "", service.Fail("unauthenticated", 401, "请先登录")
	}
	actor, err := s.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		return "", err
	}
	for _, name := range []string{"workspace_id", "conversation_id", "session_id", "input_id", "turn_id"} {
		if v := r.PathValue(name); v != "" && !service.ValidID(v) {
			return "", service.Fail("invalid_argument", 400, "资源标识无效")
		}
	}
	for _, values := range r.URL.Query() {
		if len(values) != 1 {
			return "", service.Fail("invalid_argument", 400, "不允许重复查询参数")
		}
	}
	if r.URL.Query().Has("sort_by") || r.URL.Query().Has("sort_order") {
		return "", service.Fail("invalid_argument", 400, "此接口使用固定排序，不接受排序参数")
	}
	return actor, nil
}
func (s *Server) createConversation(r *http.Request, actor string) (any, int, error) {
	var request service.CreateConversation
	if err := decode(r, &request); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.CreateConversation(r.Context(), r.PathValue("workspace_id"), actor, request)
	return v, 201, err
}
func (s *Server) getConversation(r *http.Request, actor string) (any, int, error) {
	v, err := s.Service.Store.Conversation(r.Context(), r.PathValue("workspace_id"), actor, r.PathValue("conversation_id"))
	return v, 200, err
}
func (s *Server) listConversations(r *http.Request, actor string) (any, int, error) {
	q := r.URL.Query()
	limit := 0
	if q.Has("limit") {
		var err error
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit == 0 {
			return nil, 400, service.Fail("invalid_argument", 400, "分页大小无效")
		}
	}
	v, err := s.Service.ListConversations(r.Context(), r.PathValue("workspace_id"), actor, q.Get("agent_id"), q.Get("lifecycle"), q.Get("cursor"), limit)
	return v, 200, err
}
