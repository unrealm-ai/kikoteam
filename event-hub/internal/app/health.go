package app

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Health struct {
	mu      sync.RWMutex
	phase   string
	pid     int
	workers bool
}

type snapshot struct {
	Phase             string `json:"phase"`
	SessionPID        int    `json:"session_pid,omitempty"`
	WorkersConfigured bool   `json:"workers_configured"`
}

func (h *Health) set(phase string, pid int, workers bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if (h.phase == "stopping" || h.phase == "failed" || h.phase == "stopped") && phase != "failed" && phase != "stopped" {
		return false
	}
	changed := h.phase != phase || h.pid != pid || h.workers != workers
	h.phase, h.pid, h.workers = phase, pid, workers
	return changed
}

func (h *Health) Handler(logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := rand.Text()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		h.mu.RLock()
		s := snapshot{h.phase, h.pid, h.workers}
		h.mu.RUnlock()
		code := http.StatusOK
		known := r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/status"
		if !known {
			code = http.StatusNotFound
		} else if r.Method != http.MethodGet {
			code = http.StatusMethodNotAllowed
			w.Header().Set("Allow", "GET")
		} else if r.URL.Path == "/readyz" && s.Phase != "standby" && s.Phase != "running" {
			code = http.StatusServiceUnavailable
		} else if r.URL.Path == "/livez" && (s.Phase == "failed" || s.Phase == "stopped") {
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		var body any = s
		if code >= 400 {
			errorCode, message := "not_ready", "Event Hub is not ready"
			switch code {
			case http.StatusNotFound:
				errorCode, message = "not_found", "Unknown health endpoint"
			case http.StatusMethodNotAllowed:
				errorCode, message = "method_not_allowed", "Use GET"
			default:
				if r.URL.Path == "/livez" {
					errorCode, message = "not_live", "Event Hub has stopped"
				}
			}
			body = map[string]any{"error": map[string]any{
				"code": errorCode, "message": message, "request_id": id, "details": s,
			}}
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			logger.Warn("health response write failed", "event", "health.write_failed", "trace_id", id)
		}
		// Do not log raw paths/queries: they may contain user-controlled secret data.
		logger.Info("health request", "event", "http.completed", "trace_id", id,
			"status", code, "duration_ms", time.Since(start).Milliseconds())
	})
}
