package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	actor, err := s.authorize(r)
	q := r.URL.Query()
	after := r.Header.Get("Last-Event-ID")
	if after == "" {
		after = q.Get("after")
	}
	var items []json.RawMessage
	var next string
	if err == nil {
		items, next, err = s.Service.StreamEvents(r.Context(), workspace(r), actor, sessionID(r), q.Get("turn_id"), q.Get("type"), after)
	}
	if err != nil {
		s.wrap("streamSessionEvents", func(*http.Request, string) (any, int, error) { return nil, 0, err })(w, r)
		return
	}
	control := http.NewResponseController(w)
	if err = control.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Request-ID", service.NewID())
	w.WriteHeader(200)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastHeartbeat := time.Now()
	for {
		if !s.writeSessionEventBatch(control, w, items, func(seq int64) (string, error) {
			return s.Service.EventID(workspace(r), actor, sessionID(r), q.Get("turn_id"), q.Get("type"), seq)
		}) {
			return
		}
		if time.Since(lastHeartbeat) >= 15*time.Second {
			if _, err = s.authorize(r); err != nil {
				s.streamError(control, w, sessionID(r))
				return
			}
			if err = s.writeSSE(control, w, ": heartbeat\n\n"); err != nil {
				return
			}
			lastHeartbeat = time.Now()
		}
		if err = control.Flush(); err != nil {
			return
		}
		after = next
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		items, next, err = s.Service.StreamEvents(r.Context(), workspace(r), actor, sessionID(r), q.Get("turn_id"), q.Get("type"), after)
		if err != nil {
			s.streamError(control, w, sessionID(r))
			return
		}
	}
}

func (s *Server) writeSessionEventBatch(control *http.ResponseController, w http.ResponseWriter, items []json.RawMessage, eventID func(int64) (string, error)) bool {
	for _, data := range items {
		var event struct {
			Type string `json:"type"`
			Seq  string `json:"seq"`
		}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		seq, err := strconv.ParseInt(event.Seq, 10, 64)
		if err != nil {
			return false
		}
		id, err := eventID(seq)
		if err != nil || s.writeSSE(control, w, fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, event.Type, data)) != nil {
			return false
		}
	}
	return true
}

func (s *Server) writeSSE(control *http.ResponseController, w http.ResponseWriter, text string) error {
	if err := control.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	_, err := fmt.Fprint(w, text)
	return err
}
func (s *Server) streamError(control *http.ResponseController, w http.ResponseWriter, session string) {
	// Connection-level diagnostics still use the flat event envelope. No SSE id
	// is emitted, so this cannot advance the reliable resume position.
	data, err := json.Marshal(map[string]any{"type": "stream.error", "schema_version": 1, "seq": nil, "session_id": session, "input_id": nil, "turn_id": nil, "occurred_at": time.Now().UTC(), "code": "stream_unavailable", "message": "重新鉴权并从可靠游标续读", "request_id": w.Header().Get("X-Request-ID")})
	if err != nil {
		return
	}
	if err := s.writeSSE(control, w, "event: stream.error\ndata: "+string(data)+"\n\n"); err == nil {
		if e := control.Flush(); e != nil && s.Logger != nil {
			s.Logger.Debug("SSE error delivery failed", "event", "stream.closed")
		}
	}
}
