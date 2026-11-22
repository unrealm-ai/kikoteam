package controller

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) sessionRoutes(mux *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/sessions"
	routes := map[string]operation{"POST " + base: s.createSession, "GET " + base: s.listSessions, "GET " + base + "/{session_id}": s.getSession, "PATCH " + base + "/{session_id}": s.renameSession, "POST " + base + "/{session_id}/inputs": s.submitInput, "GET " + base + "/{session_id}/inputs": s.listInputs, "GET " + base + "/{session_id}/inputs/{input_id}": s.getInput, "POST " + base + "/{session_id}/inputs/{input_id}/withdraw": s.withdrawInput, "GET " + base + "/{session_id}/turns": s.listTurns, "GET " + base + "/{session_id}/turns/{turn_id}": s.getTurn, "GET " + base + "/{session_id}/events": s.listEvents, "POST " + base + "/{session_id}/stop": s.stopSession, "POST " + base + "/{session_id}/archive": s.archiveSession}
	for route, op := range routes {
		mux.HandleFunc(route, s.wrap(route, op))
	}
	mux.HandleFunc("GET "+base+"/{session_id}/events/stream", s.streamEvents)
}
func workspace(r *http.Request) string { return r.PathValue("workspace_id") }
func sessionID(r *http.Request) string { return r.PathValue("session_id") }
func limit(r *http.Request) (int, error) {
	if !r.URL.Query().Has("limit") {
		return 0, nil
	}
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 || n > 200 {
		return 0, service.Fail("invalid_argument", 400, "分页大小无效")
	}
	return n, nil
}
func empty(r *http.Request) error { var v struct{}; return decode(r, &v) }
func controlStatus(v service.Session) int {
	if v.Execution.State == "stopping" || v.Execution.State == "blocked" {
		return 202
	}
	return 200
}

func (s *Server) createSession(r *http.Request, a string) (any, int, error) {
	var raw struct {
		ConversationID string          `json:"conversation_id"`
		TaskSpaceID    string          `json:"task_space_id"`
		AgentID        string          `json:"agent_id"`
		Mode           string          `json:"mode"`
		Title          *string         `json:"title"`
		Input          json.RawMessage `json:"input"`
	}
	if err := decode(r, &raw); err != nil {
		return nil, 400, err
	}
	req := service.CreateSession{ConversationID: raw.ConversationID, TaskSpaceID: raw.TaskSpaceID, AgentID: raw.AgentID, Mode: raw.Mode, Title: raw.Title}
	if len(raw.Input) > 0 {
		var input struct {
			AttachmentPaths []string `json:"attachment_paths"`
			Text            string   `json:"text"`
			DeliveryMode    string   `json:"delivery_mode"`
		}
		if string(raw.Input) == "null" {
			return nil, 400, service.Fail("invalid_argument", 400, "input 不能为 null")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw.Input, &fields); err != nil || (fields["text"] == nil && fields["attachment_paths"] == nil) {
			return nil, 400, service.Fail("invalid_argument", 400, "输入参数无效")
		}
		for k := range fields {
			if k != "text" && k != "delivery_mode" && k != "attachment_paths" {
				return nil, 400, service.Fail("invalid_argument", 400, "未知输入字段")
			}
		}
		if err := json.Unmarshal(raw.Input, &input); err != nil {
			return nil, 400, service.Fail("invalid_argument", 400, "输入参数无效")
		}
		req.Input = &struct {
			Text string `json:"text"`
		}{Text: input.Text}
		req.AttachmentPaths = input.AttachmentPaths
		req.DeliveryMode = input.DeliveryMode
	}
	v, replay, err := s.Service.CreateSession(r.Context(), workspace(r), a, r.Header.Get("Idempotency-Key"), req)
	status := 201
	if replay {
		status = 200
	}
	return receipt{Value: v, Replay: replay, Location: r.URL.Path + "/" + v.ID}, status, err
}
func (s *Server) getSession(r *http.Request, a string) (any, int, error) {
	v, err := s.Service.Store.Session(r.Context(), workspace(r), a, sessionID(r))
	return service.PublicSession(v), 200, err
}
func (s *Server) listSessions(r *http.Request, a string) (any, int, error) {
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	v, err := s.Service.ListSessions(r.Context(), workspace(r), a, q.Get("cursor"), service.SessionFilter{TaskSpace: q.Get("task_space_id"), Conversation: q.Get("conversation_id"), Agent: q.Get("agent_id"), Mode: q.Get("mode"), Lifecycle: q.Get("lifecycle"), Limit: n})
	return v, 200, err
}
func (s *Server) renameSession(r *http.Request, a string) (any, int, error) {
	var raw struct {
		Title    json.RawMessage `json:"title"`
		Revision string          `json:"expected_revision"`
	}
	if err := decode(r, &raw); err != nil {
		return nil, 400, err
	}
	var title *string
	if raw.Title == nil || json.Unmarshal(raw.Title, &title) != nil {
		return nil, 400, service.Fail("invalid_argument", 400, "必须提供 title")
	}
	revision, err := strconv.ParseInt(raw.Revision, 10, 64)
	if err != nil || strconv.FormatInt(revision, 10) != raw.Revision {
		return nil, 400, service.Fail("invalid_argument", 400, "修订号无效")
	}
	v, err := s.Service.RenameSession(r.Context(), workspace(r), a, sessionID(r), title, revision)
	return v, 200, err
}
func (s *Server) submitInput(r *http.Request, a string) (any, int, error) {
	var req struct {
		AttachmentPaths []string              `json:"attachment_paths"`
		Confirmation    *service.Confirmation `json:"confirmation"`
		Text            *string               `json:"text"`
		DeliveryMode    string                `json:"delivery_mode"`
	}
	if err := decode(r, &req); err != nil {
		return nil, 400, err
	}
	var v service.Input
	var replay bool
	var err error
	key := r.Header.Get("Idempotency-Key")
	switch {
	case req.Confirmation != nil && req.Text == nil && req.AttachmentPaths == nil && req.DeliveryMode == "":
		v, replay, err = s.Service.AnswerConfirmation(r.Context(), workspace(r), a, sessionID(r), key, *req.Confirmation)
	case req.Confirmation == nil && (req.Text != nil || len(req.AttachmentPaths) > 0):
		var text string
		if req.Text != nil {
			text = *req.Text
		}
		v, replay, err = s.Service.SubmitInputMode(r.Context(), workspace(r), a, sessionID(r), key, text, req.DeliveryMode, req.AttachmentPaths...)
	default:
		return nil, 400, service.Fail("invalid_argument", 400, "请选择消息或确认答复")
	}
	status := 201
	if replay {
		status = 200
	}
	return receipt{Value: v, Replay: replay, Location: r.URL.Path + "/" + v.ID}, status, err
}
func (s *Server) getInput(r *http.Request, a string) (any, int, error) {
	v, err := s.Service.Store.Input(r.Context(), workspace(r), a, sessionID(r), r.PathValue("input_id"))
	return v, 200, err
}
func (s *Server) listInputs(r *http.Request, a string) (any, int, error) {
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	v, err := s.Service.ListInputs(r.Context(), workspace(r), a, sessionID(r), q.Get("status"), q.Get("cursor"), n)
	return v, 200, err
}
func (s *Server) withdrawInput(r *http.Request, a string) (any, int, error) {
	if err := empty(r); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.WithdrawInput(r.Context(), workspace(r), a, sessionID(r), r.PathValue("input_id"))
	return v, 200, err
}
func (s *Server) getTurn(r *http.Request, a string) (any, int, error) {
	v, err := s.Service.Store.Turn(r.Context(), workspace(r), a, sessionID(r), r.PathValue("turn_id"))
	return v, 200, err
}
func (s *Server) listTurns(r *http.Request, a string) (any, int, error) {
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	v, err := s.Service.ListTurns(r.Context(), workspace(r), a, sessionID(r), q.Get("status"), q.Get("cursor"), n)
	return v, 200, err
}
func (s *Server) listEvents(r *http.Request, a string) (any, int, error) {
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	v, err := s.Service.ListEvents(r.Context(), workspace(r), a, sessionID(r), q.Get("turn_id"), q.Get("type"), q.Get("cursor"), n, q.Get("direction"))
	return v, 200, err
}
func (s *Server) stopSession(r *http.Request, a string) (any, int, error) {
	if err := empty(r); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ControlSession(r.Context(), workspace(r), a, sessionID(r), false)
	return v, controlStatus(v), err
}
func (s *Server) archiveSession(r *http.Request, a string) (any, int, error) {
	if err := empty(r); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ControlSession(r.Context(), workspace(r), a, sessionID(r), true)
	return v, controlStatus(v), err
}
func (s *Server) archiveConversation(r *http.Request, a string) (any, int, error) {
	if err := empty(r); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ArchiveConversation(r.Context(), workspace(r), a, r.PathValue("conversation_id"))
	status := 200
	if v.CleanupPending {
		status = 202
	}
	return v, status, err
}
