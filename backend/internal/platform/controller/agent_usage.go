package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) agentUsageRoutes(m *http.ServeMux) {
	if s.AgentUsage == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/trigger-rules/test": s.triggerRulesTest,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/trigger-rules":       s.triggerRulesGet,
		"PATCH /api/v1/workspaces/{workspace_id}/agents/{agent_id}/trigger-rules":     s.triggerRulesUpdate,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/usage-config":        s.agentUsageGet,
		"PATCH /api/v1/workspaces/{workspace_id}/agents/{agent_id}/usage-config":      s.agentUsageUpdate,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func (s *Server) agentUsageGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.AgentUsage.Get(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"))
	return v, 200, err
}
func (s *Server) agentUsageUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var req service.AgentUsageResource
	if err = decode(r, &req); err != nil {
		return nil, 0, err
	}
	v, err := s.AgentUsage.Update(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), req)
	if err == nil && s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Employee usage saved", "event", "agent.usage_updated", "trace_id", w.Header().Get("X-Request-ID"), "actor_id", actor.ID, "workspace_id", r.PathValue("workspace_id"), "agent_id", r.PathValue("agent_id"), "version", v.Version)
	}
	return v, 200, err
}
