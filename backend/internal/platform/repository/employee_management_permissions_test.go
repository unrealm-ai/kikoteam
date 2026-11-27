package repository

import (
	"database/sql"
	"errors"
	"testing"

	ms "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestEmployeeManagementMCPWebPermissionMatrix(t *testing.T) {
	db, manager, _, workspace, owner, assistant, _ := managerFixture(t)
	ctx := t.Context()
	addMember := func(label, role string) string {
		id := ps.NewID()
		insertWorkspaceAccount(t, db, id, label+"@management-permission.example.test")
		reviewExec(t, db, "INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,$3)", workspace, id, role)
		return id
	}
	admin := addMember("admin", "admin")
	creator := addMember("creator", "member")
	maintainer := addMember("maintainer", "member")
	member := addMember("member", "member")
	blocked := addMember("blocked", "member")
	agentService := &ps.AgentService{Store: NewAgentStore(db)}
	source, err := agentService.Get(ctx, workspace, owner, assistant)
	if err != nil {
		t.Fatal(err)
	}
	employee, err := agentService.Create(ctx, workspace, creator, ps.AgentCreate{Name: "目标员工", ModelProfileID: source.ModelProfileID})
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, "UPDATE platform_workspaces SET assistant_agent_id=$2 WHERE id=$1", workspace, assistant)
	reviewExec(t, db, "INSERT INTO platform_agent_maintainers(workspace_id,agent_id,user_id,granted_by) VALUES($1,$2,$3,$4)", workspace, employee.ID, maintainer, owner)
	p, err := (&ps.PackageService{Store: NewPackageStore(db)}).Create(ctx, workspace, owner, employee.ID, ps.PackageCreate{Name: "页面预建能力包"})
	if err != nil {
		t.Fatal(err)
	}
	plugin, version := createManagementMatrixMCP(t, db, workspace)
	usage := &ps.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	for _, id := range []string{assistant, employee.ID} {
		before, err := manager.ResolveWebConversation(ctx, workspace, blocked, id)
		if err != nil {
			t.Fatal(err)
		}
		initial, _, err := manager.CreateSession(ctx, workspace, blocked, ps.NewID(), managementMatrixInput(before.ID))
		if err != nil {
			t.Fatal(err)
		}
		staleGrant := managementMCP(t, db, workspace, initial.ID, *initial.InitialInputID)
		v, err := usage.Get(ctx, workspace, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		v.UsageConfig.Access.Deny = []ps.UsageSelector{{Channel: "web", Kind: "user", ID: blocked}}
		if _, err = usage.Update(ctx, workspace, owner, id, v); err != nil {
			t.Fatal(err)
		}
		if status, _ := staleGrant.rpc(t, "tools/list", "", nil); status != 403 {
			t.Fatal("revoked use permission still allowed the MCP entry", status)
		}
	}
	for _, role := range []struct {
		label, id string
		manages   bool
	}{
		{"workspace_owner", owner, true}, {"workspace_admin", admin, true},
		{"employee_creator_member", creator, true}, {"explicit_maintainer_member", maintainer, true},
		{"ordinary_member", member, false}, {"member_without_use", blocked, false},
	} {
		for _, surface := range []struct {
			label, source string
			tools         int
		}{{"ordinary_employee", employee.ID, 3}, {"assistant", assistant, 5}} {
			t.Run(role.label+"/"+surface.label, func(t *testing.T) {
				c, err := manager.ResolveWebConversation(ctx, workspace, role.id, surface.source)
				if err != nil {
					t.Fatal("existing history should stay readable under revoked use", err)
				}
				session, _, err := manager.CreateSession(ctx, workspace, role.id, ps.NewID(), managementMatrixInput(c.ID))
				if role.id == blocked {
					var apiErr *ms.Error
					if !errors.As(err, &apiErr) || session.ID != "" {
						t.Fatal("blocked member must not admit a new task", session.ID, err)
					}
					t.Log("existing history read allowed; new task denied:", apiErr.Code)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				client := managementMCP(t, db, workspace, session.ID, *session.InitialInputID)
				status, list := client.rpc(t, "tools/list", "", nil)
				if status != 200 || len(list["tools"].([]any)) != surface.tools {
					t.Fatal(status, list)
				}
				names := map[string]bool{}
				for _, item := range list["tools"].([]any) {
					names[item.(map[string]any)["name"].(string)] = true
				}
				if !names["get_employee"] || !names["list_employee_resources"] || !names["update_employee"] ||
					names["create_employee"] != (surface.tools == 5) || names["list_employees"] != (surface.tools == 5) {
					t.Fatal("unexpected tools", names)
				}
				target := map[string]any{}
				if surface.tools == 5 {
					target["employee_id"] = employee.ID
					list := client.expect(t, "list_employees", map[string]any{"id": employee.ID}, "")
					if list["total"] != float64(1) {
						t.Fatal(list)
					}
					if role.id == member {
						created := client.expect(t, "create_employee", map[string]any{"name": "普通成员从助手创建"}, "")
						var createdBy string
						if err = db.QueryRow("SELECT creator_account_id::text FROM platform_agents WHERE id=$1", created["id"]).Scan(&createdBy); err != nil || createdBy != member {
							t.Fatal(created, createdBy, err)
						}
					}
				}
				resource := client.expect(t, "list_employee_resources", managementMatrixArgs(target, map[string]any{"resource_type": "mcp", "id": plugin}), "")
				entry := resource["items"].([]any)[0].(map[string]any)
				if entry["id"] != plugin || entry["version_id"] != version {
					t.Fatal(resource)
				}
				overview := client.expect(t, "get_employee", target, "")
				_, configs := overview["available_configs"]
				if configs != role.manages {
					t.Fatal(overview)
				}
				checkManagementMatrixBinding(t, client, db, target, p.ID, entry, role.manages)
			})
		}
	}
}

func managementMatrixInput(conversation string) ms.CreateSession {
	return ms.CreateSession{ConversationID: conversation, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "核对当前员工并管理测试 MCP"}}
}

func managementMatrixArgs(base, extras map[string]any) map[string]any {
	args := map[string]any{}
	for k, v := range base {
		args[k] = v
	}
	for k, v := range extras {
		args[k] = v
	}
	return args
}

func createManagementMatrixMCP(t *testing.T, db *sql.DB, workspace string) (string, string) {
	t.Helper()
	plugin, version := ps.NewID(), ps.NewID()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // The test checks commit or original error.
	if _, err = tx.Exec(`INSERT INTO platform_plugins(id,workspace_id,name,description,version,current_version_id) VALUES($1,$2,'测试 MCP','仅做挂载验收',1,$3)`, plugin, workspace, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO platform_plugin_versions(id,workspace_id,plugin_id,version,name,description,endpoint,auth_type)
 VALUES($1,$2,$3,1,'测试 MCP','仅做挂载验收','https://mcp.example.test','none')`, version, workspace, plugin); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return plugin, version
}

func checkManagementMatrixBinding(t *testing.T, client managementMCPFixture, db *sql.DB, target map[string]any, packageID string, resource map[string]any, manages bool) {
	t.Helper()
	packages := managementMatrixArgs(target, map[string]any{"config_key": "capability_packages"})
	binding := map[string]any{"package_id": packageID, "resource_type": "mcp", "action": "add", "resource_id": resource["id"], "resource_version_id": resource["version_id"]}
	if !manages {
		client.expect(t, "get_employee", packages, "employee_management_forbidden")
		var v int64
		if err := db.QueryRow("SELECT version FROM platform_capability_packages WHERE id=$1", packageID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		client.expect(t, "update_employee", managementMatrixArgs(packages, map[string]any{"version": v, "binding": binding}), "employee_management_forbidden")
		delete(binding, "resource_version_id")
		binding["action"] = "remove"
		client.expect(t, "update_employee", managementMatrixArgs(packages, map[string]any{"version": v, "binding": binding}), "employee_management_forbidden")
		var after int64
		if err := db.QueryRow("SELECT version FROM platform_capability_packages WHERE id=$1", packageID).Scan(&after); err != nil || after != v {
			t.Fatal("a denied operation changed the package", after, v, err)
		}
		return
	}
	read := func() map[string]any {
		t.Helper()
		item := client.expect(t, "get_employee", packages, "")["items"].([]any)[0].(map[string]any)
		if item["id"] != packageID {
			t.Fatal(item)
		}
		return item
	}
	initial := read()
	binding["package_id"] = initial["id"]
	client.expect(t, "update_employee", managementMatrixArgs(packages, map[string]any{"version": initial["version"], "binding": binding}), "")
	attached := read()
	refs := attached["mcps"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["version_id"] != resource["version_id"] {
		t.Fatal(attached)
	}
	delete(binding, "resource_version_id")
	binding["action"] = "remove"
	client.expect(t, "update_employee", managementMatrixArgs(packages, map[string]any{"version": attached["version"], "binding": binding}), "")
	removed := read()
	if len(removed["mcps"].([]any)) != 0 || removed["version"].(float64) != initial["version"].(float64)+2 {
		t.Fatal(removed)
	}
}
