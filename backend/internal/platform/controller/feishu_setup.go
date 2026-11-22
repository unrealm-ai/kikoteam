package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) feishuSetupRoutes(m *http.ServeMux) {
	if s.FeishuSetup == nil {
		return
	}
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/feishu"
	for _, action := range []string{"status", "start", "poll", "retry", "cancel", "diagnostics"} {
		method, path := "POST", p+"/registration"
		if action == "status" {
			method = "GET"
		} else if action == "diagnostics" {
			path = p + "/diagnostics"
		} else if action != "start" {
			path += "/{registration_id}/" + action
		}
		m.HandleFunc(method+" "+path, s.wrap(method+" "+path, s.feishuSetupHandler(action)))
	}
}
func (s *Server) feishuSetupHandler(action string) func(http.ResponseWriter, *http.Request) (any, int, error) {
	return func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		w.Header().Set("Cache-Control", "no-store")
		actor, e := s.actor(r)
		if e != nil {
			return nil, 0, e
		}
		var allContacts *bool
		var replaceID string
		if action == "start" {
			var body struct {
				AllContacts           *bool  `json:"all_contacts,omitempty"`
				ReplaceRegistrationID string `json:"replace_registration_id,omitempty"`
			}
			if e = decode(r, &body); e != nil {
				return nil, 0, e
			}
			allContacts = body.AllContacts
			replaceID = body.ReplaceRegistrationID
		} else if action != "status" {
			var body struct{}
			if e = decode(r, &body); e != nil {
				return nil, 0, e
			}
		}
		workspace, agent, id := r.PathValue("workspace_id"), r.PathValue("agent_id"), r.PathValue("registration_id")
		var out any
		status := 200
		switch action {
		case "status":
			out, e = s.FeishuSetup.Status(r.Context(), workspace, actor.ID, agent)
		case "start":
			keys := r.Header.Values("Idempotency-Key")
			if len(keys) != 1 {
				return nil, 0, service.Fail("invalid_idempotency_key", 400, "需要唯一 Idempotency-Key")
			}
			out, e = s.FeishuSetup.StartRegistration(r.Context(), workspace, actor.ID, agent, keys[0], allContacts, replaceID)
			status = 202
		case "poll":
			out, e = s.FeishuSetup.Poll(r.Context(), workspace, actor.ID, agent, id)
		case "retry":
			out, e = s.FeishuSetup.Retry(r.Context(), workspace, actor.ID, agent, id)
		case "cancel":
			out, e = s.FeishuSetup.Cancel(r.Context(), workspace, actor.ID, agent, id)
		case "diagnostics":
			out, e = s.FeishuSetup.Diagnose(r.Context(), workspace, actor.ID, agent)
		}
		return out, status, e
	}
}
