package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) agentRoutes(m *http.ServeMux) {
	if s.Agents == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"GET /api/v1/workspaces/{workspace_id}/agents":                        s.agentList,
		"POST /api/v1/workspaces/{workspace_id}/agents":                       s.agentCreate,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}":             s.agentGet,
		"PATCH /api/v1/workspaces/{workspace_id}/agents/{agent_id}":           s.agentUpdate,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/maintainers": s.agentMaintainers,
		"PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/maintainers": s.agentSaveMaintainers,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func (s *Server) agentList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	query := r.URL.Query()
	if len(query["q"]) > 1 || len(query["enabled"]) > 1 {
		return nil, 0, service.Fail("invalid_query", 400, "查询参数重复")
	}
	filter := service.AgentListQuery{Search: query.Get("q")}
	if query.Has("enabled") {
		value := query.Get("enabled")
		if value != "true" && value != "false" {
			return nil, 0, service.Fail("invalid_query", 400, "enabled 应为 true 或 false")
		}
		enabled := value == "true"
		filter.Enabled = &enabled
	}
	query.Del("q")
	query.Del("enabled")
	filter.Page, filter.PageSize, e = pagination(query, 50)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Agents.List(r.Context(), r.PathValue("workspace_id"), a.ID, filter)
	return v, 200, e
}
func (s *Server) agentGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Agents.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"))
	return v, 200, e
}
func (s *Server) agentCreate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.AgentCreate
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Agents.Create(r.Context(), r.PathValue("workspace_id"), a.ID, req)
	if e == nil {
		s.agentSaved(w, r, a.ID, "agent.created", v)
	}
	return v, 201, e
}
func (s *Server) agentUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.AgentPatch
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	if req.WebAccess != nil {
		return nil, 0, service.Fail("usage_config_required", 400, "请在员工统一使用配置中修改权限")
	}
	v, e := s.Agents.Update(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req)
	if e == nil {
		s.agentSaved(w, r, a.ID, "agent.updated", v)
	}
	return v, 200, e
}

func (s *Server) agentSaved(w http.ResponseWriter, r *http.Request, actor, event string, v service.Agent) {
	if s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Employee configuration saved", "event", event, "trace_id", w.Header().Get("X-Request-ID"), "actor_id", actor, "workspace_id", r.PathValue("workspace_id"), "agent_id", v.ID, "version", v.Version, "config_version", v.ConfigVersion, "enabled", v.Enabled)
	}
}

func (s *Server) agentMaintainers(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.Agents.Maintainers(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"))
	return v, 200, err
}

func (s *Server) agentSaveMaintainers(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var req service.EmployeeMaintainersUpdate
	if err = decode(r, &req); err != nil {
		return nil, 0, err
	}
	w, id := r.PathValue("workspace_id"), r.PathValue("agent_id")
	v, err := s.Agents.SaveMaintainers(r.Context(), w, a.ID, id, req)
	if err == nil && s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Employee maintainers saved", "event", "agent.maintainers.updated", "actor_id", a.ID, "workspace_id", w, "agent_id", id, "version", v.Version, "count", len(v.People))
	}
	return v, 200, err
}
