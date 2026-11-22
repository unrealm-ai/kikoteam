package controller

import (
	"net/http"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) modelRoutes(m *http.ServeMux) {
	if s.Models == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"GET /api/v1/workspaces/{workspace_id}/model-profiles":                                 s.modelsList,
		"POST /api/v1/workspaces/{workspace_id}/model-profiles":                                s.modelCreate,
		"POST /api/v1/workspaces/{workspace_id}/model-profiles/test":                           s.modelTest,
		"GET /api/v1/workspaces/{workspace_id}/model-profiles/{model_profile_id}":              s.modelGet,
		"PATCH /api/v1/workspaces/{workspace_id}/model-profiles/{model_profile_id}":            s.modelUpdate,
		"POST /api/v1/workspaces/{workspace_id}/model-profiles/{model_profile_id}/set-default": s.modelAction,
		"POST /api/v1/workspaces/{workspace_id}/model-profiles/{model_profile_id}/revoke-key":  s.modelAction,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}
func (s *Server) modelsList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	query := r.URL.Query()
	include := query["include_platform"]
	if len(include) > 1 || len(include) == 1 && include[0] != "true" && include[0] != "false" {
		return nil, 0, service.Fail("invalid_query", 400, "模型来源参数无效")
	}
	delete(query, "include_platform")
	p, n, e := pagination(query, 50)
	if e != nil {
		return nil, 0, e
	}
	if len(include) == 1 && include[0] == "true" {
		out, e := s.Models.Selection(r.Context(), r.PathValue("workspace_id"), a.ID, p, n)
		return projectedModelChoices(out), 200, e
	}
	out, e := s.Models.List(r.Context(), r.PathValue("workspace_id"), a.ID, p, n)
	return out, 200, e
}

func projectedModelChoices(p service.ModelPage) map[string]any {
	items := make([]any, 0, len(p.Items))
	for _, m := range p.Items {
		if m.Source == "platform" {
			items = append(items, map[string]any{"id": m.ID, "name": m.Name, "source": m.Source, "protocol": m.Protocol,
				"enabled": m.Enabled, "validated": m.Validated, "is_default": m.IsDefault, "prices": m.Prices})
		} else {
			items = append(items, m)
		}
	}
	return map[string]any{"items": items, "total": p.Total, "page": p.Page, "page_size": p.PageSize}
}
func (s *Server) modelGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	out, e := s.Models.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("model_profile_id"))
	return out, 200, e
}
func (s *Server) modelTest(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.ModelTestRequest
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	if req.ProfileID != "" && !modelUUID(req.ProfileID) {
		return nil, 0, service.Fail("invalid_id", 400, "模型标识无效")
	}
	out, e := s.Models.Test(r.Context(), r.PathValue("workspace_id"), a.ID, req)
	return out, 200, e
}
func (s *Server) modelCreate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.ModelSaveRequest
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	out, e := s.Models.Save(r.Context(), r.PathValue("workspace_id"), a.ID, "", req)
	status := 201
	if out.Replayed {
		status = 200
	}
	return out, status, e
}
func (s *Server) modelUpdate(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	// Status-only updates cannot smuggle configuration past the test receipt requirement.
	var req struct {
		service.ModelSaveRequest
		Enabled *bool `json:"enabled,omitempty"`
	}
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	if req.Enabled != nil {
		if req.Name != "" || req.Description != "" || req.Protocol != "" || req.BaseURL != "" || req.ModelID != "" || req.APIKey != nil || req.TestReceipt != "" {
			return nil, 0, service.Fail("invalid_model_patch", 400, "启停和模型配置须分别保存")
		}
		out, e := s.Models.Change(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("model_profile_id"), "status", req.Version, *req.Enabled)
		return out, 200, e
	}
	out, e := s.Models.Save(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("model_profile_id"), req.ModelSaveRequest)
	return out, 200, e
}
func (s *Server) modelAction(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req struct {
		Version int64 `json:"version"`
	}
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	action := "set-default"
	if strings.HasSuffix(r.URL.Path, "/revoke-key") {
		action = "revoke-key"
	}
	out, e := s.Models.Change(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("model_profile_id"), action, req.Version, false)
	return out, 200, e
}

func modelUUID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
