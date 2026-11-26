package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) publicTaskExecutionHistory(w http.ResponseWriter, r *http.Request) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	var page service.EventPage
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		page, err = s.Service.PublicTaskEventHistory(ctx, token, r.URL.Query().Get("cursor"))
	}
	writeTaskDetailJSON(w, page, id, err)
}

func (s *Server) streamPublicTaskExecution(w http.ResponseWriter, r *http.Request) {
	id := taskDetailHeaders(w)
	token, err := taskDetailBearer(r)
	after := r.Header.Get("Last-Event-ID")
	load := func(parent context.Context, cursor string) ([]json.RawMessage, string, func(int64) (string, error), error) {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		return s.Service.PublicTaskEventBatch(ctx, token, cursor)
	}
	var items []json.RawMessage
	var next string
	var eventID func(int64) (string, error)
	if err == nil {
		items, next, eventID, err = load(r.Context(), after)
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
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	heart := time.Now()
	for {
		if !s.writeSessionEventBatch(control, w, items, eventID) {
			return
		}
		if time.Since(heart) >= 15*time.Second {
			if s.writeSSE(control, w, ": heartbeat\n\n") != nil {
				return
			}
			heart = time.Now()
		}
		if control.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		items, next, eventID, err = load(r.Context(), next)
		if err != nil {
			value, _ := taskDetailError(err, id)
			s.writeTaskDetailFrame(control, w, "stream.error", "", value)
			return
		}
	}
}
