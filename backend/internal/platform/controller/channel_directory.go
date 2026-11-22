package controller

import (
	"net/http"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) channelDirectoryRoutes(m *http.ServeMux) {
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/{provider}/directory"
	m.HandleFunc("GET "+p, s.wrap("GET "+p, s.channelDirectoryStatus))
	m.HandleFunc("POST "+p+"/sync", s.wrap("POST "+p+"/sync", s.channelDirectorySync))
	m.HandleFunc("GET "+p+"/users", s.wrap("GET "+p+"/users", s.channelDirectoryPage))
	m.HandleFunc("GET "+p+"/departments", s.wrap("GET "+p+"/departments", s.channelDirectoryPage))
}
func (s *Server) channelDirectoryStatus(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.Channels.DirectoryStatusProvider(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"))
	return v, 200, err
}
func (s *Server) channelDirectorySync(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	v, err := s.Channels.RequestDirectorySyncProvider(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"))
	return v, 202, err
}
func (s *Server) channelDirectoryPage(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	values := r.URL.Query()
	for key, v := range values {
		if len(v) != 1 || (key != "q" && key != "parent_id" && key != "department_id" && key != "limit" && key != "cursor" && key != "snapshot_version") {
			return nil, 0, service.Fail("invalid_directory_query", 400, "通讯录查询参数无效")
		}
	}
	q := service.DirectoryQuery{Kind: "users", Q: values.Get("q"), ParentID: values.Get("parent_id"), DepartmentID: values.Get("department_id"), Limit: 50, Cursor: values.Get("cursor")}
	if r.Pattern == "GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/{provider}/directory/departments" {
		q.Kind = "departments"
	}
	if raw := values.Get("limit"); raw != "" {
		q.Limit, err = strconv.Atoi(raw)
		if err != nil {
			return nil, 0, service.Fail("invalid_directory_query", 400, "分页大小无效")
		}
	}
	if raw := values.Get("snapshot_version"); raw != "" {
		q.SnapshotVersion, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, 0, service.Fail("invalid_directory_query", 400, "快照版本无效")
		}
	}
	v, err := s.Channels.DirectoryPageProvider(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"), q)
	return v, 200, err
}
