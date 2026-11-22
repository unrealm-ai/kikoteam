package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) channelObservedChatRoutes(m *http.ServeMux) {
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/{provider}/groups"
	m.HandleFunc("GET "+p, s.wrap("GET "+p, func(_ http.ResponseWriter, r *http.Request) (any, int, error) {
		actor, err := s.actor(r)
		if err != nil {
			return nil, 0, err
		}
		for k, v := range r.URL.Query() {
			if k != "q" || len(v) != 1 {
				return nil, 0, service.Fail("invalid_argument", 400, "查询参数无效")
			}
		}
		items, err := s.Channels.ObservedChats(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), r.PathValue("provider"), r.URL.Query().Get("q"))
		return map[string]any{"items": items, "evidence": "observed_message", "truncated_possible": len(items) == 100}, 200, err
	}))
}
