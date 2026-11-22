package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Exercise the production combination, not just each module's isolated mux.
// ServeMux rejects ambiguous cross-module wildcard routes during registration.
func TestCompleteServerAssemblyAndHTTPStartup(t *testing.T) {
	api := &Server{
		Auth:       &service.AuthService{},
		Workspaces: &service.WorkspaceService{},
		Models:     &service.ModelService{},
		Agents:     &service.AgentService{},
		Skills:     &service.SkillService{},
		Plugins:    &service.PluginService{},
		Packages:   &service.PackageService{},
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ws := "/api/v1/workspaces/00000000-0000-0000-0000-000000000001"
	id := "00000000-0000-0000-0000-000000000002"
	for _, path := range []string{
		"/skills", "/skills/" + id, "/skills/" + id + "/versions",
		"/skill-imports/" + id,
		"/plugins", "/plugins/" + id + "/versions",
		"/agents/" + id + "/capability-packages", "/capability-package-templates",
	} {
		res, err := server.Client().Get(server.URL + ws + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s: status=%d; expected registered auth-gated route", path, res.StatusCode)
		}
	}
}
