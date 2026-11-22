package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanStore interface {
	HumanChatList(context.Context, service.HumanRuntimeScope, service.HumanListQuery) (any, error)
	HumanPendingCount(context.Context, string, string) (int, error)
	HumanHistory(context.Context, string, string, string, string, int) (service.HumanHistoryPage, error)
	HumanRecipients(context.Context, string, string, string, *service.HumanRecipients) (service.HumanRecipients, error)
	HumanInboxPage(context.Context, string, string, int, string) ([]service.HumanRequest, int, error)
	AnswerHumanRequest(context.Context, string, string, string, service.HumanAnswer) (any, error)
	HumanTaskTool(context.Context, service.HumanRuntimeScope, string, string, *service.HumanQuestion) (any, error)
	HumanChatTool(context.Context, service.HumanRuntimeScope, string, string, *service.HumanAnswer) (any, error)
	HumanChatSubmission(context.Context, service.HumanRuntimeScope, string, string, *service.HumanAnswer) (any, error)
}

func (s *Server) humanRoutes(mux *http.ServeMux) {
	if _, ok := s.Engines.(humanStore); !ok {
		return
	}
	base := "/api/v1/workspaces/{workspace_id}"
	s.humanRecoveryRoutes(mux, base)
	mux.HandleFunc("GET "+base+"/agents/{agent_id}/human-recipients", s.wrap("getHumanRecipients", s.humanRecipients))
	mux.HandleFunc("PATCH "+base+"/agents/{agent_id}/human-recipients", s.wrap("updateHumanRecipients", s.humanRecipients))
	mux.HandleFunc("GET "+base+"/human-requests", s.wrap("listHumanRequests", s.humanInbox))
	mux.HandleFunc("GET "+base+"/human-requests/summary", s.wrap("getHumanRequestSummary", s.humanSummary))
	mux.HandleFunc("GET "+base+"/human-requests/history", s.wrap("listHumanRequestHistory", s.humanHistory))
	mux.HandleFunc("POST "+base+"/human-requests/{request_id}/answer", s.wrap("answerHumanRequest", s.humanAnswer))
	mux.HandleFunc("POST /api/internal/v1/sessions/{session_id}/human-tools", s.internalCredential(s.humanTool, s.humanCredential))
}

func (s *Server) humanRecipients(r *http.Request, actor string) (any, int, error) {
	var update *service.HumanRecipients
	if r.Method == http.MethodPatch {
		update = &service.HumanRecipients{}
		if err := decode(r, update); err != nil {
			return nil, 400, err
		}
	}
	store, _ := s.Engines.(humanStore)
	out, err := store.HumanRecipients(r.Context(), workspace(r), actor, r.PathValue("agent_id"), update)
	return out, 200, err
}

func (s *Server) humanInbox(r *http.Request, actor string) (any, int, error) {
	store, _ := s.Engines.(humanStore)
	if err := taskQuery(r, "offset", "request_id"); err != nil {
		return nil, 400, err
	}
	offset := 0
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
	}
	id := r.URL.Query().Get("request_id")
	if err != nil || offset < 0 || id != "" && (!service.ValidID(id) || offset != 0) {
		return nil, 400, service.Fail("invalid_argument", 400, "收件箱查询参数无效")
	}
	out, total, err := store.HumanInboxPage(r.Context(), workspace(r), actor, offset, id)
	return map[string]any{"items": out, "total": total}, 200, err
}

func (s *Server) humanSummary(r *http.Request, actor string) (any, int, error) {
	if err := taskQuery(r); err != nil {
		return nil, 400, err
	}
	store, _ := s.Engines.(humanStore)
	count, err := store.HumanPendingCount(r.Context(), workspace(r), actor)
	return map[string]int{"pending": count}, 200, err
}

func (s *Server) humanHistory(r *http.Request, actor string) (any, int, error) {
	if err := taskQuery(r, "offset", "request_id", "filter"); err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	filter, id := q.Get("filter"), q.Get("request_id")
	if filter == "" {
		filter = "all"
	}
	offset := 0
	var err error
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
	}
	if err != nil || offset < 0 || (id != "" && (!service.ValidID(id) || offset != 0)) || (filter != "all" && filter != "pending" && filter != "resolved") {
		return nil, 400, service.Fail("invalid_argument", 400, "确认记录查询参数无效")
	}
	store, _ := s.Engines.(humanStore)
	page, err := store.HumanHistory(r.Context(), workspace(r), actor, filter, id, offset)
	return page, 200, err
}

func (s *Server) humanAnswer(r *http.Request, actor string) (any, int, error) {
	var answer service.HumanAnswer
	if err := decode(r, &answer); err != nil {
		return nil, 400, err
	}
	store, _ := s.Engines.(humanStore)
	out, err := store.AnswerHumanRequest(r.Context(), workspace(r), actor, r.PathValue("request_id"), answer)
	return out, 200, err
}

func (s *Server) humanIdentity(r *http.Request) (service.HumanRuntimeScope, string, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	input := r.Header.Get("X-Kiko-Human-Input")
	v, err := service.VerifyHumanRuntime(s.EngineToken, input, token)
	if err == nil && v.SessionID != r.PathValue("session_id") {
		err = service.Fail("unauthenticated", 401, "确认工具执行身份无效")
	}
	return service.HumanRuntimeScope{Identity: v, InputID: input}, token, err
}

func (s *Server) humanCredential(r *http.Request) string {
	_, token, err := s.humanIdentity(r)
	if err != nil {
		return "invalid-scoped-credential"
	}
	return token
}

func (s *Server) humanTool(r *http.Request) (any, int, error) {
	scope, _, err := s.humanIdentity(r)
	if err != nil {
		return nil, 401, err
	}
	var req struct {
		Operation   string                 `json:"operation"`
		OperationID string                 `json:"operationId,omitempty"`
		Page        *int                   `json:"page,omitempty"`
		PageSize    *int                   `json:"pageSize,omitempty"`
		RequestID   string                 `json:"request_id,omitempty"`
		Question    *service.HumanQuestion `json:"question,omitempty"`
		Answer      *service.HumanAnswer   `json:"answer,omitempty"`
	}
	if err = decode(r, &req); err != nil {
		return nil, 400, err
	}
	if (req.Operation == "fenced" || req.Operation == "answer" || req.Operation == "propose" || req.Operation == "receipt") && !service.ValidID(req.RequestID) {
		return nil, 400, service.Fail("invalid_reference", 400, "确认请求标识无效")
	}
	store, _ := s.Engines.(humanStore)
	var out any
	switch req.Operation {
	case "recipients", "request", "fenced":
		out, err = store.HumanTaskTool(r.Context(), scope, req.Operation, req.RequestID, req.Question)
	case "pending":
		out, err = store.HumanChatList(r.Context(), scope, service.HumanListQuery{RequestID: req.RequestID, Page: req.Page, PageSize: req.PageSize})
	case "answer", "propose":
		if req.OperationID != "" && req.Answer != nil {
			out, err = store.HumanChatSubmission(r.Context(), scope, req.RequestID, req.OperationID, req.Answer)
		} else {
			out, err = store.HumanChatTool(r.Context(), scope, req.Operation, req.RequestID, req.Answer)
		}
	case "receipt":
		out, err = store.HumanChatSubmission(r.Context(), scope, req.RequestID, req.OperationID, nil)
	default:
		err = service.Fail("invalid_human_operation", 400, "确认工具操作无效")
	}
	return out, 200, err
}
