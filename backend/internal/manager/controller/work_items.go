package controller

import (
	"net/http"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) workRoutes(mux *http.ServeMux) {
	if _, ok := s.Engines.(service.WorkToolStore); !ok {
		return
	}
	mux.HandleFunc("POST /api/internal/v1/sessions/{session_id}/work-item-tools", s.internalCredential(s.workTool, s.workCredential))
}

func (s *Server) workIdentity(r *http.Request) (service.WorkScope, string, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	input := r.Header.Get(service.WorkInputHeader)
	id, err := service.VerifyWorkTool(s.EngineToken, "local", input, token)
	if err == nil && id.SessionID != r.PathValue("session_id") {
		err = service.Fail("unauthenticated", 401, "事项工具执行身份无效")
	}
	return service.WorkScope{Identity: id, InputID: input}, token, err
}

func (s *Server) workCredential(r *http.Request) string {
	_, token, err := s.workIdentity(r)
	if err != nil {
		return "invalid-scoped-credential"
	}
	return token
}

func (s *Server) workTool(r *http.Request) (any, int, error) {
	scope, _, err := s.workIdentity(r)
	if err != nil {
		return nil, 401, err
	}
	var body struct {
		Operation string                `json:"operation"`
		Arguments service.WorkArguments `json:"arguments"`
	}
	if err = decode(r, &body); err != nil {
		return nil, 400, err
	}
	switch body.Operation {
	case "handoff", "submit", "fenced":
		store, _ := s.Engines.(service.WorkToolStore)
		out, err := store.WorkTool(r.Context(), scope, body.Operation, body.Arguments)
		return out, 200, err
	default:
		return nil, 400, service.Fail("invalid_work_operation", 400, "事项工具操作无效")
	}
}
