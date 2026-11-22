package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *Server) runnerToken(id, engine string) string {
	mac := hmac.New(sha256.New, []byte(s.EngineToken))
	mac.Write([]byte("runner-config:" + id + ":" + engine))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) sandboxes(r *http.Request) (any, int, error) {
	engine := r.PathValue("engine_id")
	if !service.ValidID(engine) {
		return nil, 400, service.Fail("invalid_argument", 400, "Engine 标识无效")
	}
	out, err := s.Engines.Sandboxes(r.Context(), engine)
	for i := range out {
		out[i].ConfigToken = s.runnerToken(out[i].ID, engine)
	}
	return map[string]any{"items": out}, 200, err
}
func (s *Server) sandboxConfig(r *http.Request) (any, int, error) {
	id, engine := r.PathValue("sandbox_id"), r.URL.Query().Get("engine_id")
	if !service.ValidID(id) || !service.ValidID(engine) {
		return nil, 400, service.Fail("invalid_argument", 400, "环境身份无效")
	}
	items, err := s.Engines.Sandboxes(r.Context(), engine)
	if err != nil {
		return nil, 0, err
	}
	for _, v := range items {
		if v.ID == id {
			return v, 200, nil
		}
	}
	return nil, 404, service.Fail("resource_not_found", 404, "环境不存在或不属于当前 Engine")
}
func (s *Server) sandboxState(r *http.Request) (any, int, error) {
	var in struct {
		EngineID string `json:"engine_id"`
		State    string `json:"state"`
	}
	if err := decode(r, &in); err != nil {
		return nil, 400, err
	}
	if !service.ValidID(in.EngineID) || !service.ValidID(r.PathValue("sandbox_id")) || (in.State != "ready" && in.State != "stopped" && in.State != "unknown") {
		return nil, 400, service.Fail("invalid_argument", 400, "环境状态无效")
	}
	err := s.Engines.SandboxState(r.Context(), in.EngineID, r.PathValue("sandbox_id"), in.State)
	return map[string]bool{"accepted": err == nil}, 200, err
}
