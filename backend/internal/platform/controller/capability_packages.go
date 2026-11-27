package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
)

func (s *Server) packageRoutes(m *http.ServeMux) {
	if s.Packages == nil {
		return
	}
	for pattern, op := range map[string]operation{
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-packages":                s.packageList,
		"POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-upgrades":               s.packageUpgrade,
		"POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-packages":               s.packageCreate,
		"GET /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-packages/{package_id}":   s.packageGet,
		"PATCH /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-packages/{package_id}": s.packageUpdate,
		"GET /api/v1/workspaces/{workspace_id}/capability-package-templates":                         s.packageList,
		"POST /api/v1/workspaces/{workspace_id}/capability-package-templates":                        s.packageCreate,
		"GET /api/v1/workspaces/{workspace_id}/capability-package-templates/{package_id}":            s.packageGet,
		"PATCH /api/v1/workspaces/{workspace_id}/capability-package-templates/{package_id}":          s.packageUpdate,
		"POST /api/v1/workspaces/{workspace_id}/agents/{agent_id}/capability-package-copies":         s.packageCopy,
	} {
		m.HandleFunc(pattern, s.wrap(pattern, op))
	}
}

type packagePresentation struct {
	service.CapabilityPackage
	AssistantTools []service.AssistantToolDescription `json:"assistant_tools,omitempty"`
}

func (s *Server) presentPackage(p service.CapabilityPackage) packagePresentation {
	out := packagePresentation{CapabilityPackage: p}
	if p.AssistantToolSet != "" && s.AssistantToolDescriptions != nil {
		out.AssistantTools = s.AssistantToolDescriptions(p.AssistantToolSet)
	}
	return out
}

func (s *Server) packageList(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	p, n, e := page(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Packages.List(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), p, n)
	items := make([]packagePresentation, 0, len(v.Items))
	for _, p := range v.Items {
		items = append(items, s.presentPackage(p))
	}
	return struct {
		service.PackagePage
		Items []packagePresentation `json:"items"`
	}{v, items}, 200, e
}
func (s *Server) packageGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	v, e := s.Packages.Get(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("package_id"))
	return s.presentPackage(v), 200, e
}
func (s *Server) packageCreate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.PackageCreate
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Packages.Create(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req)
	if e == nil {
		s.packageSaved(w, r, a.ID, "capability_package.created", v)
	}
	return v, 201, e
}
func (s *Server) packageUpdate(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req service.PackagePatch
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	v, e := s.Packages.Update(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("package_id"), req)
	if e == nil {
		s.packageSaved(w, r, a.ID, "capability_package.updated", v)
	}
	return s.presentPackage(v), 200, e
}
func (s *Server) packageCopy(w http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var req struct {
		TemplateID      string `json:"template_id"`
		TemplateVersion int64  `json:"template_version"`
		Name            string `json:"name,omitempty"`
	}
	if e = decode(r, &req); e != nil {
		return nil, 0, e
	}
	// Body identifiers have the same UUID validation as path identifiers.
	if !validPackageID(req.TemplateID) {
		return nil, 0, service.Fail("invalid_template", 400, "模板标识无效")
	}
	v, e := s.Packages.CopyTemplate(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req.TemplateID, service.PackageCopy{TemplateVersion: req.TemplateVersion, Name: req.Name})
	if e == nil {
		s.packageSaved(w, r, a.ID, "capability_package.copied", v)
	}
	return v, 201, e
}
func validPackageID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
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
func (s *Server) packageSaved(w http.ResponseWriter, r *http.Request, actor, event string, p service.CapabilityPackage) {
	if s.Logger != nil {
		s.Logger.InfoContext(r.Context(), "Capability package saved", "event", event, "trace_id", w.Header().Get("X-Request-ID"), "actor_id", actor, "workspace_id", r.PathValue("workspace_id"), "agent_id", p.AgentID, "package_id", p.ID, "version", p.Version)
	}
}

func (s *Server) packageUpgrade(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, err := s.actor(r)
	if err != nil {
		return nil, 0, err
	}
	var req service.ResourceUpgrade
	if err = decode(r, &req); err != nil {
		return nil, 0, err
	}
	err = s.Packages.Upgrade(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), req)
	return map[string]bool{"upgraded": err == nil}, 200, err
}
