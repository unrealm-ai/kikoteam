package repository

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestEmployeeOverviewHTTPSeparatesUseFromConfiguration(t *testing.T) {
	ctx := t.Context()
	db, models, workspace, owner := modelFixture(t)
	model := saveTestModel(t, models, workspace, owner)
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "overview-reader@example.test")
	if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, member); err != nil {
		t.Fatal(err)
	}
	const token = "isolated-overview-reader"
	if _, err := db.Exec(`INSERT INTO platform_sessions(token_hash,account_id,expires_at) VALUES($1,$2,$3)`, service.HashToken(token), member, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	a, err := agents.Create(ctx, workspace, owner, service.AgentCreate{
		Name: "客服员工", Role: "协助处理订单问题", Instruction: "restricted-prompt", ModelProfileID: model.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	packages := &service.PackageService{Store: NewPackageStore(db)}
	p, err := packages.Create(ctx, workspace, owner, a.ID, service.PackageCreate{Name: "restricted-package", Skills: []service.PackageSkill{}, Plugins: []service.PackagePlugin{}})
	if err != nil {
		t.Fatal(err)
	}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Agents: agents, Packages: packages}).Handler()
	get := func(path string, expected int) []byte {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Cookie", "kiko_session="+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != expected {
			t.Fatalf("GET %s expected %d got %d: %s", path, expected, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	base := "/api/v1/workspaces/" + workspace + "/agents"
	raw := get(base+"/"+a.ID, 200)
	var overview service.Agent
	if err := json.Unmarshal(raw, &overview); err != nil || overview.CanManage || !overview.CanChat || overview.Role != a.Role {
		t.Fatal(overview, err)
	}
	for _, b := range [][]byte{raw, get(base, 200)} {
		if body := string(b); strings.Contains(body, "restricted-prompt") || strings.Contains(body, model.ID) {
			t.Fatal("non-manager response leaked private configuration")
		}
	}
	pack := base + "/" + a.ID + "/capability-packages"
	get(pack, 403)
	get(pack+"/"+p.ID, 403)
	if _, err = agents.SaveMaintainers(ctx, workspace, owner, a.ID, service.EmployeeMaintainersUpdate{Version: 1, People: []service.SelectionRef{
		{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: a.ID, ExternalID: member},
	}}); err != nil {
		t.Fatal(err)
	}
	if raw := string(get(base+"/"+a.ID, 200)); !strings.Contains(raw, "restricted-prompt") || !strings.Contains(raw, `"can_manage":true`) {
		t.Fatal("maintenance did not restore full configuration")
	}
	get(pack, 200)
	get(pack+"/"+p.ID, 200)
}
