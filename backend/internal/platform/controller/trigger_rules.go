package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) triggerRulesGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.AgentUsage.GetRules(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"))
	return out, 200, err
}
func (s *Server) triggerRulesUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var in service.TriggerRulesResource
	if err = decode(r, &in); err != nil {
		return nil, 0, err
	}
	out, err := s.AgentUsage.UpdateRules(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), in)
	if err == nil && s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Employee trigger rules saved", "event", "agent.trigger_rules_updated", "trace_id", w.Header().Get("X-Request-ID"), "actor_id", actor.ID, "workspace_id", r.PathValue("workspace_id"), "agent_id", r.PathValue("agent_id"), "version", out.Version)
	}
	return out, 200, err
}
func (s *Server) triggerRulesTest(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var in service.TriggerRulesTest
	if err = decode(r, &in); err != nil {
		return nil, 0, err
	}
	out, err := s.AgentUsage.TestRules(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), in)
	return out, 200, err
}
