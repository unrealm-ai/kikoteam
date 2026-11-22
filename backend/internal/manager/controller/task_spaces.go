package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *Server) taskSpaceRoutes(m *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/task-spaces"
	m.HandleFunc("GET "+base, s.wrap("listTaskSpaces", s.listTaskSpaces))
	m.HandleFunc("POST "+base, s.wrap("createTaskSpace", s.createTaskSpace))
	m.HandleFunc("GET "+base+"/{task_space_id}", s.wrap("getTaskSpace", s.getTaskSpace))
	m.HandleFunc("PUT "+base+"/{task_space_id}/agents/{agent_id}", s.wrap("grantTaskSpaceAgent", s.grantTaskSpaceAgent))
}
func (s *Server) createTaskSpace(r *http.Request, actor string) (any, int, error) {
	var q service.TaskSpaceCreate
	if err := decode(r, &q); err != nil {
		return nil, 400, err
	}
	v, replay, err := s.Service.CreateTaskSpace(r.Context(), r.PathValue("workspace_id"), actor, r.Header.Get("Idempotency-Key"), q)
	status := 201
	if replay {
		status = 200
	}
	return receipt{Value: v, Replay: replay, Location: r.URL.Path + "/" + v.ID}, status, err
}
func (s *Server) getTaskSpace(r *http.Request, actor string) (any, int, error) {
	v, err := s.Service.TaskSpace(r.Context(), r.PathValue("workspace_id"), actor, r.PathValue("task_space_id"))
	return v, 200, err
}
func (s *Server) grantTaskSpaceAgent(r *http.Request, actor string) (any, int, error) {
	var q service.TaskSpaceGrant
	if err := decode(r, &q); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.GrantTaskSpaceAgent(r.Context(), r.PathValue("workspace_id"), actor, r.PathValue("task_space_id"), r.PathValue("agent_id"), q)
	return v, 200, err
}

func (s *Server) listTaskSpaces(r *http.Request, actor string) (any, int, error) {
	if err := taskQuery(r, "cursor", "limit"); err != nil {
		return nil, 400, err
	}
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ListTaskSpaces(r.Context(), workspace(r), actor, r.URL.Query().Get("cursor"), n)
	return v, 200, err
}
