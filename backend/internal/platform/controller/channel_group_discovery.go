package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) channelGroupDiscoveryRoutes(m *http.ServeMux) {
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/feishu/groups/discover"
	m.HandleFunc("POST "+p, s.wrap("discoverFeishuGroups", s.discoverFeishuGroups))
}
func (s *Server) discoverFeishuGroups(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	if s.ChannelGroups == nil {
		return nil, 503, service.Fail("dependency_unavailable", 503, "群发现不可用")
	}
	var q struct {
		Cursor         string `json:"cursor"`
		SelectionToken string `json:"selection_token"`
	}
	if e = decode(r, &q); e != nil {
		return nil, 400, e
	}
	if q.SelectionToken != "" {
		if q.Cursor != "" {
			return nil, 400, service.Fail("invalid_group_cursor", 400, "群选择不能同时传分页游标")
		}
		out, err := s.ChannelGroups.Select(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q.SelectionToken)
		return out, 200, err
	}
	out, e := s.ChannelGroups.Discover(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q.Cursor)
	return out, 200, e
}
