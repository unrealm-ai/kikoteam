package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) dingtalkSetupRoutes(m *http.ServeMux) {
	if s.DingTalkSetup == nil {
		return
	}
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/dingtalk"
	for _, action := range []string{"status", "start", "poll", "confirm", "cancel"} {
		method, path := "POST", p+"/registration"
		if action == "status" {
			method = "GET"
		} else if action == "diagnostics" {
			path = p + "/diagnostics"
		} else if action != "start" {
			path += "/{registration_id}/" + action
		}
		m.HandleFunc(method+" "+path, s.wrap(method+" "+path, s.dingtalkSetupHandler(action)))
	}
}
func (s *Server) dingtalkSetupHandler(action string) func(http.ResponseWriter, *http.Request) (any, int, error) {
	return func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		w.Header().Set("Cache-Control", "no-store")
		actor, e := s.actor(r)
		if e != nil {
			return nil, 0, e
		}
		if action != "status" {
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
			out, e = s.DingTalkSetup.Status(r.Context(), workspace, actor.ID, agent)
		case "start":
			keys := r.Header.Values("Idempotency-Key")
			if len(keys) != 1 {
				return nil, 0, service.Fail("invalid_idempotency_key", 400, "需要唯一 Idempotency-Key")
			}
			out, e = s.DingTalkSetup.Start(r.Context(), workspace, actor.ID, agent, keys[0])
			status = 202
		case "poll":
			out, e = s.DingTalkSetup.Poll(r.Context(), workspace, actor.ID, agent, id)
		case "confirm":
			out, e = s.DingTalkSetup.Confirm(r.Context(), workspace, actor.ID, agent, id)
		case "cancel":
			out, e = s.DingTalkSetup.Cancel(r.Context(), workspace, actor.ID, agent, id)
		}
		return out, status, e
	}
}
