package controller

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestResourceRequestPolicy(t *testing.T) {
	for _, tc := range []struct {
		route   string
		timeout time.Duration
		limit   int64
	}{
		{"POST /api/v1/workspaces/{workspace_id}/skill-imports", 60 * time.Second, 28 << 20},
		{"POST /api/v1/workspaces/{workspace_id}/model-profiles/test", 45 * time.Second, 16384},
		{"PATCH /api/v1/workspaces/{workspace_id}/agents/{agent_id}", 20 * time.Second, 65536},
		{"PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/plugin-accounts/{plugin_id}", 25 * time.Second, 65536},
		{"POST /api/v1/auth/sessions", 20 * time.Second, 16384},
	} {
		timeout, limit := requestPolicy(tc.route)
		if timeout != tc.timeout || limit != tc.limit {
			t.Errorf("policy for %s: %v/%d", tc.route, timeout, limit)
		}
	}
}
func TestResourcePathIDsRejectInvalidValues(t *testing.T) {
	for _, name := range []string{"skill_id", "skill_version_id", "version_id", "import_id", "plugin_id", "account_id", "package_id", "template_id"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue(name, "../../other-workspace")
		if validIDs(r) == nil {
			t.Errorf("accepted invalid %s", name)
		}
	}
}
