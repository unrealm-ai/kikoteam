package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) wecomContactRoutes(m *http.ServeMux) {
	if s.WecomContacts == nil {
		return
	}
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/wecom/contacts"
	m.HandleFunc("GET "+p+"/permission", s.wrap("GET "+p+"/permission", s.wecomContactPermission))
	m.HandleFunc("GET "+p+"/search", s.wrap("GET "+p+"/search", s.wecomContactSearch))
	m.HandleFunc("POST "+p+"/verifications", s.wrap("POST "+p+"/verifications", s.wecomVerificationStart))
	m.HandleFunc("GET "+p+"/verifications/{verification_id}", s.wrap("GET "+p+"/verifications/{verification_id}", s.wecomVerificationGet))
	m.HandleFunc("POST "+p+"/verifications/{verification_id}/cancel", s.wrap("POST "+p+"/verifications/{verification_id}/cancel", s.wecomVerificationGet))
}
func (s *Server) wecomContactPermission(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	if r.URL.RawQuery != "" {
		return nil, 0, service.Fail("invalid_contact_query", 400, "联系人搜索检测不接受查询参数")
	}
	value, e := s.WecomContacts.ContactPermission(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"))
	return value, 200, e
}
func (s *Server) wecomContactSearch(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["q"]) != 1 {
		return nil, 0, service.Fail("invalid_contact_query", 400, "仅接受一个搜索关键词")
	}
	v, e := s.WecomContacts.Search(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), q.Get("q"))
	return v, 200, e
}
func (s *Server) wecomVerificationStart(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var q struct {
		SearchUserID string `json:"search_user_id"`
		RequestID    string `json:"request_id"`
	}
	if e = decode(r, &q); e != nil {
		return nil, 0, e
	}
	v, e := s.WecomContacts.Start(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), q.SearchUserID, q.RequestID)
	return v, 200, e
}
func (s *Server) wecomVerificationGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.WecomContacts.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("verification_id"), r.Method == "POST")
	return v, 200, e
}
