package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"strconv"
)

// WebGroupRoutes exposes only the shared room stream, never internal employee sessions.
func (s *Server) WebGroupRoutes(mux *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/web-groups/{group_id}/messages"
	mux.HandleFunc("POST "+base, s.wrap("postWebGroupMessage", s.postWebGroupMessage))
	mux.HandleFunc("GET "+base, s.wrap("webGroupMessages", s.webGroupMessages))
}
func (s *Server) postWebGroupMessage(r *http.Request, actor string) (any, int, error) {
	var v service.WebGroupPost
	if err := decode(r, &v); err != nil {
		return nil, 400, err
	}
	result, replay, err := s.Service.PostWebGroupMessage(r.Context(), workspace(r), actor, r.PathValue("group_id"), r.Header.Get("Idempotency-Key"), v)
	return map[string]any{"message": result, "replayed": replay}, 200, err
}
func (s *Server) webGroupMessages(r *http.Request, actor string) (any, int, error) {
	after, limit := int64(0), 50
	var err error
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, 400, service.Fail("invalid_argument", 400, "消息序号无效")
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return nil, 400, service.Fail("invalid_argument", 400, "分页无效")
		}
	}
	items, err := s.Service.WebGroupMessages(r.Context(), workspace(r), actor, r.PathValue("group_id"), after, limit)
	return map[string]any{"items": items}, 200, err
}
