package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const publicTaskDetail = "/api/v1/public/task-detail"

func (s *Server) publicTaskDetailRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+publicTaskDetail, s.getPublicTaskDetail)
	mux.HandleFunc("GET "+publicTaskDetail+"/events", s.streamPublicTaskDetail)
	mux.HandleFunc("GET "+publicTaskDetail+"/artifacts/{artifact_id}", s.downloadPublicTaskArtifact)
	for _, action := range []string{"append", "stop", "continue"} {
		mux.HandleFunc("POST "+publicTaskDetail+"/"+action, func(w http.ResponseWriter, r *http.Request) {
			s.mutatePublicTaskDetail(w, r, action)
		})
	}
}

func taskDetailHeaders(w http.ResponseWriter) string {
	id := service.NewID()
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return id
}

func taskDetailBearer(r *http.Request) (string, error) {
	headers := r.Header.Values("Authorization")
	if len(headers) == 1 {
		scheme, value, ok := strings.Cut(headers[0], " ")
		if ok && strings.EqualFold(scheme, "Bearer") && len(value) <= 128 && strings.TrimSpace(value) == value && value != "" {
			return value, nil
		}
	}
	return "", service.Fail("unauthenticated", 401, "需要有效的任务详情链接")
}

func taskDetailError(err error, id string) (map[string]any, int) {
	status, code, message := 503, "dependency_unavailable", "任务详情暂不可用"
	var parsed *service.Error
	if errors.As(err, &parsed) {
		status, code, message = parsed.Status, parsed.Code, parsed.Message
	}
	return map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}}, status
}

func writeTaskDetailJSON(w http.ResponseWriter, value any, id string, err error) {
	status := http.StatusOK
	if err != nil {
		value, status = taskDetailError(err, id)
	}
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value) //nolint:errcheck // Client may have gone away; no secret is logged.
}

func (s *Server) getPublicTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	var view service.TaskDetailSnapshot
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		view, err = s.Service.TaskDetailPage(ctx, token, "")
	}
	writeTaskDetailJSON(w, view, id, err)
}

func (s *Server) mutatePublicTaskDetail(w http.ResponseWriter, r *http.Request, action string) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	var view service.TaskDetailSnapshot
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req service.TaskDetailMutation
		err = decode(r, &req)
		keys := r.Header.Values("Idempotency-Key")
		if err == nil && len(keys) != 1 {
			err = service.Fail("idempotency_key_required", 400, "任务操作需要唯一去重键")
		}
		if err == nil {
			view, err = s.Service.MutateTaskDetail(ctx, token, action, keys[0], req)
		}
	}
	writeTaskDetailJSON(w, view, id, err)
}

func (s *Server) downloadPublicTaskArtifact(w http.ResponseWriter, r *http.Request) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	if err != nil {
		writeTaskDetailJSON(w, nil, id, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	item, data, err := s.Service.PublicTaskArtifact(ctx, token, r.PathValue("artifact_id"))
	if err != nil {
		writeTaskDetailJSON(w, nil, id, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": item.Name}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data) //nolint:errcheck // The recipient may disconnect during download.
}

func (s *Server) streamPublicTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	after := r.Header.Get("Last-Event-ID")
	var initial service.TaskDetailSnapshot
	if err == nil {
		initial, err = s.taskDetailStreamSnapshot(r.Context(), token, after)
	}
	if err != nil {
		writeTaskDetailJSON(w, nil, id, err)
		return
	}
	control := http.NewResponseController(w)
	if err = control.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if after != initial.Cursor {
		if !s.writeTaskDetailFrame(control, w, "task.updated", initial.Cursor, initial) {
			return
		}
	} else if control.Flush() != nil {
		return
	}
	s.pollPublicTaskDetail(r.Context(), control, w, token, initial.Cursor, id)
}

func (s *Server) pollPublicTaskDetail(ctx context.Context, control *http.ResponseController, w http.ResponseWriter, token, cursor, id string) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	heart := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		view, err := s.taskDetailStreamSnapshot(ctx, token, cursor)
		if err != nil {
			value, _ := taskDetailError(err, id)
			s.writeTaskDetailFrame(control, w, "stream.error", "", value)
			return
		}
		if view.Cursor != cursor {
			if !s.writeTaskDetailFrame(control, w, "task.updated", view.Cursor, view) {
				return
			}
			cursor = view.Cursor
			heart = time.Now()
		} else if time.Since(heart) >= 15*time.Second {
			if s.writeSSE(control, w, ": heartbeat\n\n") != nil || control.Flush() != nil {
				return
			}
			heart = time.Now()
		}
	}
}

func (s *Server) taskDetailStreamSnapshot(ctx context.Context, token, cursor string) (service.TaskDetailSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.Service.TaskDetailPage(ctx, token, cursor)
}

func (s *Server) writeTaskDetailFrame(control *http.ResponseController, w http.ResponseWriter, event, id string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	head := ""
	if id != "" {
		head = "id: " + id + "\n"
	}
	return s.writeSSE(control, w, fmt.Sprintf("%sevent: %s\ndata: %s\n\n", head, event, body)) == nil && control.Flush() == nil
}
