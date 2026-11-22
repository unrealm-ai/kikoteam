package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"strconv"
)

func (s *Server) groupSharingRoutes(m *http.ServeMux) {
	if s.SharingPolicies == nil {
		return
	}
	base := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/sharing-policies"
	for route, op := range map[string]operation{"GET " + base: s.sharingList, "POST " + base: s.sharingSave, "POST " + base + "/preview": s.sharingPreview, "GET " + base + "/{policy_id}": s.sharingGet, "PUT " + base + "/{policy_id}": s.sharingSave, "DELETE " + base + "/{policy_id}": s.sharingDelete} {
		m.HandleFunc(route, s.wrap(route, op))
	}
}
func (s *Server) sharingList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	query := r.URL.Query()
	if len(query["q"]) > 1 {
		return nil, 0, service.Fail("invalid_sharing_query", 400, "查询参数重复")
	}
	q := query.Get("q")
	query.Del("q")
	page, size, err := pagination(query, 20)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.SharingPolicies.List(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), q, page, size)
	return out, 200, err
}
func (s *Server) sharingGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.SharingPolicies.Get(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), r.PathValue("policy_id"))
	return out, 200, err
}
func (s *Server) sharingSave(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var req service.GroupSharingPut
	if err = decode(r, &req); err != nil {
		return nil, 0, err
	}
	out, err := s.SharingPolicies.Save(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), r.PathValue("policy_id"), req)
	code := 200
	if r.PathValue("policy_id") == "" {
		code = 201
	}
	return out, code, err
}
func (s *Server) sharingDelete(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	query := r.URL.Query()
	for k, vs := range query {
		if k != "version" || len(vs) != 1 {
			return nil, 0, service.Fail("invalid_sharing_query", 400, "版本参数无效")
		}
	}
	version, err := strconv.ParseInt(query.Get("version"), 10, 64)
	if err != nil {
		return nil, 0, service.Fail("invalid_sharing_query", 400, "版本参数无效")
	}
	err = s.SharingPolicies.Delete(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), r.PathValue("policy_id"), version)
	return nil, 204, err
}
func (s *Server) sharingPreview(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	actor, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var req service.GroupSharingPut
	if err = decode(r, &req); err != nil {
		return nil, 0, err
	}
	out, err := s.SharingPolicies.Preview(r.Context(), r.PathValue("workspace_id"), actor.ID, r.PathValue("agent_id"), req)
	return out, 200, err
}
