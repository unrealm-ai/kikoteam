package controller

import "net/http"

func (s *Server) resolveWebConversation(r *http.Request, actor string) (any, int, error) {
	var request struct {
		AgentID string `json:"agent_id"`
	}
	if err := decode(r, &request); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ResolveWebConversation(r.Context(), workspace(r), actor, request.AgentID)
	return v, 200, err
}
