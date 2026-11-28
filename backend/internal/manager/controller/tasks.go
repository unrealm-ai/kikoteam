package controller

import (
	"mime"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) taskRoutes(mux *http.ServeMux) {
	base := "/api/v1/workspaces/{workspace_id}/tasks"
	mux.HandleFunc("GET "+base+"/{task_id}/artifacts/{artifact_id}/content", s.wrap("getTaskArtifactContent", s.taskArtifact))
	mux.HandleFunc("GET "+base, s.wrap("listTasks", s.listTasks))
	mux.HandleFunc("GET "+base+"/{task_id}", s.wrap("getTask", s.getTask))
	mux.HandleFunc("GET "+base+"/{task_id}/reports", s.wrap("listTaskReports", s.listTaskReports))
	mux.HandleFunc("GET "+base+"/{task_id}/artifacts/{artifact_id}", s.wrap("downloadTaskArtifact", s.downloadTaskArtifact))
}

type taskArtifactDownload struct {
	Item service.Artifact
	Data []byte
}

func (d taskArtifactDownload) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Item.Name}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(d.Data) //nolint:errcheck // The recipient may disconnect during download.
}
func taskQuery(r *http.Request, allowed ...string) error {
	known := map[string]bool{}
	for _, name := range allowed {
		known[name] = true
	}
	for name, values := range r.URL.Query() {
		if !known[name] || len(values) != 1 {
			return service.Fail("invalid_argument", 400, "未知或重复的查询参数")
		}
	}
	return nil
}
func (s *Server) listTasks(r *http.Request, a string) (any, int, error) {
	if err := taskQuery(r, "q", "agent_id", "conversation_id", "task_space_id", "work_item_id", "include_counts", "execution_state", "latest_turn_status", "lifecycle", "cursor", "limit"); err != nil {
		return nil, 400, err
	}
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	counts, err := includeCounts(r)
	if err != nil {
		return nil, 400, err
	}
	v, err := s.Service.ListTasks(r.Context(), workspace(r), a, q.Get("cursor"), service.TaskFilter{WorkItem: q.Get("work_item_id"), IncludeCounts: counts, TaskSpace: q.Get("task_space_id"), Q: q.Get("q"), Agent: q.Get("agent_id"), Conversation: q.Get("conversation_id"), ExecutionState: q.Get("execution_state"), LatestTurnStatus: q.Get("latest_turn_status"), Lifecycle: q.Get("lifecycle"), Limit: n})
	return v, 200, err
}
func (s *Server) getTask(r *http.Request, a string) (any, int, error) {
	if err := taskQuery(r); err != nil {
		return nil, 400, err
	}
	v, err := s.Service.GetTask(r.Context(), workspace(r), a, r.PathValue("task_id"))
	return v, 200, err
}
func (s *Server) listTaskReports(r *http.Request, a string) (any, int, error) {
	if err := taskQuery(r, "turn_id", "input_id", "cursor", "limit"); err != nil {
		return nil, 400, err
	}
	n, err := limit(r)
	if err != nil {
		return nil, 400, err
	}
	q := r.URL.Query()
	v, err := s.Service.ListTaskReports(r.Context(), workspace(r), a, r.PathValue("task_id"), q.Get("cursor"), service.ReportFilter{Turn: q.Get("turn_id"), Input: q.Get("input_id"), Limit: n})
	return v, 200, err
}

func (s *Server) downloadTaskArtifact(r *http.Request, a string) (any, int, error) {
	if err := taskQuery(r); err != nil {
		return nil, 400, err
	}
	item, data, err := s.Service.TaskArtifact(r.Context(), workspace(r), a, r.PathValue("task_id"), r.PathValue("artifact_id"))
	if err != nil {
		return nil, 0, err
	}
	return taskArtifactDownload{Item: item, Data: data}, http.StatusOK, nil
}

func includeCounts(r *http.Request) (bool, error) {
	switch r.URL.Query().Get("include_counts") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, service.Fail("invalid_argument", 400, "include_counts 必须为布尔值")
	}
}
