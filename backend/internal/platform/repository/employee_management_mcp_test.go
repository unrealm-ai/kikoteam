package repository

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	amcp "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/assistant"
	pmcp "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/platform"
	mr "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	ms "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type managementMCPFixture struct {
	mux          *http.ServeMux
	grant, input string
}

func managementMCP(t *testing.T, db *sql.DB, w, s, input string) managementMCPFixture {
	t.Helper()
	id := ms.DelegationIdentity{SessionID: s, EngineID: ms.NewID(), AssignmentID: ms.NewID(), Generation: 1}
	turn := ms.NewID()
	store := &mr.Store{DB: db}
	if err := store.RegisterEngine(t.Context(), ms.Engine{ID: id.EngineID, HostID: "management-test-" + s, Endpoint: "http://127.0.0.1:9000", Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, s, input, id.AssignmentID)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1,last_turn_no=1 WHERE id=$1`, s, turn, id.EngineID, id.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	sec := strings.Repeat("m", 40)
	token, err := amcp.Token(sec, input, "platform", id)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	all := pmcp.Tools{Directory: &EmployeeManagementStore{DB: db}, Agents: &ps.AgentService{Store: NewAgentStore(db)}, Packages: &ps.PackageService{Store: NewPackageStore(db)}}
	amcp.MountPlatform(mux, store, sec, all.Server(true), all.Server(false))
	return managementMCPFixture{mux, token, input}
}

func (f managementMCPFixture) rpc(t *testing.T, method, name string, args map[string]any) (int, map[string]any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "method": method, "id": 1}
	if name != "" {
		req["params"] = map[string]any{"name": name, "arguments": args}
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/mcp/assistant/platform", bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+f.grant)
	r.Header.Set("X-Kiko-Assistant-Input", f.input)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != 200 {
		return w.Code, nil
	}
	var msg struct {
		Result map[string]any
		Error  map[string]any
	}
	if err = json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatal(w.Body.String(), err)
	}
	if msg.Error != nil {
		t.Fatal(msg.Error)
	}
	if method == "tools/call" {
		data, ok := msg.Result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatal(msg.Result)
		}
		return w.Code, data
	}
	return w.Code, msg.Result
}

func (f managementMCPFixture) expect(t *testing.T, tool string, args map[string]any, code string) map[string]any {
	t.Helper()
	status, out := f.rpc(t, "tools/call", tool, args)
	if status != 200 || out["success"] != (code == "") || code != "" && out["code"] != code {
		t.Fatal(tool, status, out, "expected", code)
	}
	data, _ := out["data"].(map[string]any)
	return data
}

func TestEmployeeManagementMCPRealWebAndSharedSkill(t *testing.T) {
	db, svc, _, w, owner, employee, _ := managerFixture(t)
	ctx := t.Context()
	member := ps.NewID()
	insertWorkspaceAccount(t, db, member, "ordinary-management@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, member)
	c, err := svc.ResolveWebConversation(ctx, w, member, employee)
	if err != nil {
		t.Fatal(err)
	}
	req := ms.CreateSession{ConversationID: c.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "已验证 GitHub Skill，请安装到当前员工"}}
	sess, _, err := svc.CreateSession(ctx, w, member, ps.NewID(), req)
	if err != nil {
		t.Fatal(err)
	}
	f := managementMCP(t, db, w, sess.ID, *sess.InitialInputID)
	packages := &ps.PackageService{Store: NewPackageStore(db)}
	initial, err := packages.List(ctx, w, owner, employee, 1, 20)
	if err != nil || initial.Total != 1 || len(initial.Items) != 1 {
		t.Fatal(initial, err)
	}
	p := initial.Items[0]
	assertDefaultEmployeePackage(t, p, employee)
	if status, list := f.rpc(t, "tools/list", "", nil); status != 200 || len(list["tools"].([]any)) != 3 {
		t.Fatal(list)
	}
	overview := f.expect(t, "get_employee", nil, "")
	if len(overview) != 1 {
		t.Fatal(overview)
	}
	f.expect(t, "get_employee", map[string]any{"config_key": "instruction"}, "employee_management_forbidden")
	resourceSearch := map[string]any{"resource_type": "skill", "name": "GitHub logistics", "page": 1, "page_size": 1}
	if empty := f.expect(t, "list_employee_resources", resourceSearch, ""); empty["total"] != float64(0) {
		t.Fatal(empty)
	}
	assertOfficialManagementCatalogVisible(t, db, f)
	f.expect(t, "update_employee", map[string]any{"config_key": "profile", "name": "不应更新", "version": 1}, "employee_management_forbidden")
	unrelated := f.expect(t, "list_employee_resources", resourceSearch, "")
	if unrelated["total"] != float64(0) {
		t.Fatal(unrelated)
	}
	skill, ver := ps.NewID(), ps.NewID()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // original test error retained
	if _, err = tx.Exec(`INSERT INTO platform_skills(id,workspace_id,name,description,version,current_version_id) VALUES($1,$2,'GitHub logistics','查询物流签收凭证',1,$3)`, skill, w, ver); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO platform_skill_versions(id,workspace_id,skill_id,sequence,version_label,technical_name,description,manifest,source,storage_identity,key_prefix) VALUES($1,$2,$3,1,'1.0','fixture-skill','','{}','{}','local','test')`, ver, w, skill); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	resourceSearch["id"] = skill
	found := f.expect(t, "list_employee_resources", resourceSearch, "")
	item := found["items"].([]any)[0].(map[string]any)
	if item["version_id"] != ver || item["summary"] != "查询物流签收凭证" || len(item) != 6 {
		t.Fatal(item)
	}
	binding := map[string]any{"package_id": p.ID, "resource_type": "skill", "action": "add", "resource_id": skill, "resource_version_id": ver}
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version, "binding": binding}, "employee_management_forbidden")
	reviewExec(t, db, `INSERT INTO platform_agent_maintainers(workspace_id,agent_id,user_id,granted_by) VALUES($1,$2,$3,$4)`, w, employee, member, owner)
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version, "binding": binding}, "")
	after := f.expect(t, "get_employee", map[string]any{"config_key": "capability_packages"}, "")["items"].([]any)[0].(map[string]any)
	if after["skills"].([]any)[0].(map[string]any)["skill_id"] != skill {
		t.Fatal(after)
	}
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version, "binding": binding}, "package_version_conflict")
	binding["action"] = "remove"
	delete(binding, "resource_version_id")
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version + 1, "binding": binding}, "")
	mcpID, mcpVersion := ps.NewID(), ps.NewID()
	pluginTx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer pluginTx.Rollback() //nolint:errcheck // Commit owns success.
	if _, err = pluginTx.Exec(`INSERT INTO platform_plugins(id,workspace_id,name,description,version,current_version_id) VALUES($1,$2,'物流 MCP','查运单签收',1,$3)`, mcpID, w, mcpVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = pluginTx.Exec(`INSERT INTO platform_plugin_versions(id,workspace_id,plugin_id,version,name,description,endpoint,auth_type)
 VALUES($1,$2,$3,1,'物流 MCP','查运单签收','https://mcp.example.test','none')`, mcpVersion, w, mcpID); err != nil {
		t.Fatal(err)
	}
	if err = pluginTx.Commit(); err != nil {
		t.Fatal(err)
	}
	mcps := f.expect(t, "list_employee_resources", map[string]any{"resource_type": "mcp", "id": mcpID, "page_size": 1}, "")
	mcpItem := mcps["items"].([]any)[0].(map[string]any)
	if mcpItem["version_id"] != mcpVersion || mcpItem["endpoint"] != nil {
		t.Fatal(mcpItem)
	}
	mcpBinding := map[string]any{"package_id": p.ID, "resource_type": "mcp", "action": "add", "resource_id": mcpID, "resource_version_id": mcpVersion}
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version + 2, "binding": mcpBinding}, "")
	attached := f.expect(t, "get_employee", map[string]any{"config_key": "capability_packages"}, "")["items"].([]any)[0].(map[string]any)
	if attached["mcps"].([]any)[0].(map[string]any)["version_id"] != mcpVersion {
		t.Fatal(attached)
	}
	delete(mcpBinding, "resource_version_id")
	mcpBinding["action"] = "remove"
	f.expect(t, "update_employee", map[string]any{"config_key": "capability_packages", "version": p.Version + 3, "binding": mcpBinding}, "")
	f.expect(t, "update_employee", map[string]any{"config_key": "instruction", "version": 1, "instruction": "新的岗位约束"}, "")
	got := f.expect(t, "get_employee", map[string]any{"config_key": "instruction"}, "")
	if got["instruction"] != "新的岗位约束" {
		t.Fatal(got)
	}
	reviewExec(t, db, `UPDATE platform_workspaces SET assistant_agent_id=$2 WHERE id=$1`, w, employee)
	if status, list := f.rpc(t, "tools/list", "", nil); status != 200 || len(list["tools"].([]any)) != 5 {
		t.Fatal(list)
	}
	listed := f.expect(t, "list_employees", map[string]any{"name": "Worker", "id": employee, "page": 1, "page_size": 1}, "")
	if listed["total"] != float64(1) {
		t.Fatal(listed)
	}
	caller := ps.EmployeeManagementCaller{Workspace: w, Actor: member, Employee: employee, Input: f.input}
	if id, src, err := (&EmployeeManagementStore{DB: db}).ResolveModel(ctx, caller, ""); err != nil || id == "" || src != "workspace" {
		t.Fatal("default model resolution", id, src, err)
	}
	created := f.expect(t, "create_employee", map[string]any{"name": "新员工"}, "")
	if created["name"] != "新员工" {
		t.Fatal(created)
	}
	target := created["id"].(string)
	query := map[string]any{"config_key": "capability_packages", "employee_id": target}
	newPackages := f.expect(t, "get_employee", query, "")
	newItems := newPackages["items"].([]any)
	if len(newItems) != 1 {
		t.Fatal(newPackages)
	}
	defaultPackage := newItems[0].(map[string]any)
	if defaultPackage["name"] != "基础能力" || defaultPackage["editable"] != true {
		t.Fatal(defaultPackage)
	}
	f.expect(t, "update_employee", map[string]any{
		"config_key": "capability_packages", "employee_id": target, "version": defaultPackage["version"],
		"binding": map[string]any{"package_id": defaultPackage["id"], "resource_type": "skill", "action": "add", "resource_id": skill, "resource_version_id": ver},
	}, "")
	bound := f.expect(t, "get_employee", query, "")["items"].([]any)[0].(map[string]any)
	skills := bound["skills"].([]any)
	if len(skills) != 1 || skills[0].(map[string]any)["skill_id"] != skill {
		t.Fatal(bound)
	}
	var creator string
	if err = db.QueryRow(`SELECT creator_account_id FROM platform_agents WHERE id=$1`, created["id"]).Scan(&creator); err != nil || creator != member {
		t.Fatal(creator, err)
	}
	reviewExec(t, db, `UPDATE platform_model_profiles SET is_default=false WHERE workspace_id=$1`, w)
	f.expect(t, "create_employee", map[string]any{"name": "不能在没有默认模型时创建"}, "model_unavailable")
	official := ps.NewID()
	reviewExec(t, db, `INSERT INTO platform_proxy_models(id,name,protocol,base_url,upstream_model_id,credential_ciphertext,
 input_price_units,output_price_units,enabled,is_default,validated,version,tested_at,updated_by)
 VALUES($1,'Default Platform Model','chat_completions','https://example.test','test','',1,1,true,true,true,1,now(),$2)`, official, owner)
	backup := f.expect(t, "create_employee", map[string]any{"name": "使用平台兜底的员工"}, "")
	var fallbackID string
	if err = db.QueryRow(`SELECT platform_model_id FROM platform_agents WHERE id=$1`, backup["id"]).Scan(&fallbackID); err != nil || fallbackID != official {
		t.Fatal(fallbackID, err)
	}
	f.expect(t, "update_employee", map[string]any{"config_key": "profile", "name": "更新物流员工", "role": "负责查运单", "version": 2}, "")
	profile := f.expect(t, "get_employee", map[string]any{"config_key": "profile"}, "")
	if profile["name"] != "更新物流员工" || profile["role"] != "负责查运单" {
		t.Fatal(profile)
	}
	f.expect(t, "update_employee", map[string]any{"config_key": "avatar", "version": 3}, "invalid_employee_management_request")
	f.expect(t, "get_employee", map[string]any{"config_key": "avatar"}, "invalid_employee_management_request")
	employeeID := created["id"].(string)
	readWebRecord := func() ps.Agent {
		t.Helper()
		a, err := (&ps.AgentService{Store: NewAgentStore(db)}).Get(ctx, w, member, employeeID)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	preset := readWebRecord().Avatar
	if !regexp.MustCompile(`^cn-v2-(0[1-9]|1[0-9]|2[0-7])$`).MatchString(preset) {
		t.Fatal("create must save a supported portrait", preset)
	}
	if _, included := created["avatar"]; included {
		t.Fatal("create should not return avatar", created)
	}
	overview = f.expect(t, "get_employee", map[string]any{"employee_id": employeeID}, "")
	if _, included := overview["overview"].(map[string]any)["avatar"]; included {
		t.Fatal("employee overview should not return avatar", overview)
	}
	createdList := f.expect(t, "list_employees", map[string]any{"id": employeeID}, "")
	if _, included := createdList["items"].([]any)[0].(map[string]any)["avatar"]; included {
		t.Fatal("employee list should not return avatar", createdList)
	}
	f.expect(t, "get_employee", map[string]any{"employee_id": employeeID, "config_key": "avatar"}, "invalid_employee_management_request")
	f.expect(t, "update_employee", map[string]any{"employee_id": employeeID, "config_key": "avatar", "version": 1}, "invalid_employee_management_request")
	if after := readWebRecord(); after.Version != 1 || after.Avatar != preset {
		t.Fatal("MCP avatar write changed employee", after)
	}
	webPreset := "cn-v2-01"
	if preset == webPreset {
		webPreset = "cn-v2-02"
	}
	agents := &ps.AgentService{Store: NewAgentStore(db)}
	if _, err = agents.Update(ctx, w, member, employeeID, ps.AgentPatch{Version: 1, Avatar: &webPreset}); err != nil {
		t.Fatal("Web employee save must retain avatar updates", err)
	}
	if after := readWebRecord(); after.Avatar != webPreset || after.Version != 2 {
		t.Fatal(after)
	}
	model := saveTestModel(t, &ps.ModelService{Store: NewModelStore(db), Prober: modelProbeStub{}, Secret: []byte("test-model-secret-at-least-thirty-two-bytes"), NormalizeEndpoint: func(_ string, url string) (string, error) { return url, nil }}, w, owner)
	configs := f.expect(t, "get_employee", map[string]any{"employee_id": employee}, "")["available_configs"].([]any)
	modelConfig := ""
	for _, raw := range configs {
		item := raw.(map[string]any)
		if item["key"] == "avatar" {
			t.Fatal("get must not advertise avatar", configs)
		}
		if item["key"] == "model" && item["writable"] == false {
			modelConfig = item["key"].(string)
		}
	}
	if modelConfig == "" {
		t.Fatal("get did not advertise a read-only model configuration", configs)
	}
	previousModel := f.expect(t, "get_employee", map[string]any{"employee_id": employee, "config_key": modelConfig}, "")
	f.expect(t, "update_employee", map[string]any{"employee_id": employee, "config_key": modelConfig, "version": previousModel["version"]}, "invalid_employee_management_request")
	current := f.expect(t, "get_employee", map[string]any{"config_key": "model"}, "")
	if current["model_id"] != previousModel["model_id"] || current["version"] != previousModel["version"] || current["writable"] != false {
		t.Fatal(current)
	}
	if _, err = agents.Update(ctx, w, owner, employee, ps.AgentPatch{Version: int64(current["version"].(float64)), ModelProfileID: &model.ID}); err != nil {
		t.Fatal("Web employee save must retain model updates", err)
	}
	var shortID, platformShortID string
	if err := db.QueryRow(`SELECT 'm'||management_number::text FROM platform_model_profiles WHERE id=$1`, model.ID).Scan(&shortID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT 'm'||management_number::text FROM platform_proxy_models WHERE id=$1`, official).Scan(&platformShortID); err != nil || platformShortID == shortID {
		t.Fatal(platformShortID, shortID, err)
	}
	if after := f.expect(t, "get_employee", map[string]any{"config_key": "model"}, ""); after["model_id"] != shortID {
		t.Fatal(after)
	}
	reviewExec(t, db, `DELETE FROM platform_agent_maintainers WHERE agent_id=$1 AND user_id=$2`, employee, member)
	f.expect(t, "list_employee_resources", map[string]any{"resource_type": "model", "id": shortID}, "")
	choices := f.expect(t, "list_employee_resources", map[string]any{"purpose": "create", "resource_type": "model", "id": shortID, "page_size": 1}, "")
	choice := choices["items"].([]any)[0].(map[string]any)
	if choice["id"] != shortID || choice["source"] != "workspace" {
		t.Fatal(choice)
	}
	newEmployee := f.expect(t, "create_employee", map[string]any{"name": "指定查询得到的模型", "model_id": choice["id"]}, "")
	readback := f.expect(t, "get_employee", map[string]any{"employee_id": newEmployee["id"], "config_key": "model"}, "")
	if readback["model_id"] != shortID {
		t.Fatal(readback)
	}
	selected := f.expect(t, "create_employee", map[string]any{"name": "指定平台模型", "model_id": platformShortID}, "")
	if err := db.QueryRow(`SELECT platform_model_id FROM platform_agents WHERE id=$1`, selected["id"]).Scan(&fallbackID); err != nil || fallbackID != official {
		t.Fatal(fallbackID, err)
	}
	f.expect(t, "create_employee", map[string]any{"name": "不能替换为默认模型", "model_id": "m9223372036854775807"}, "model_unavailable")
	f.expect(t, "create_employee", map[string]any{"name": "无效短 ID", "model_id": "m+1"}, "invalid_employee_management_request")
	// Synthetic duplicate across catalogs: workspace is authoritative, even
	// if its match is disabled. Normal inserts share one sequence.
	reviewExec(t, db, `UPDATE platform_proxy_models SET management_number=(SELECT management_number FROM platform_model_profiles WHERE id=$2) WHERE id=$1`, official, model.ID)
	id, source, err := (&EmployeeManagementStore{DB: db}).ResolveModel(ctx, caller, shortID)
	if err != nil || id != model.ID || source != "workspace" {
		t.Fatal(id, source, err)
	}
	reviewExec(t, db, `UPDATE platform_model_profiles SET enabled=false,is_default=false WHERE id=$1`, model.ID)
	f.expect(t, "create_employee", map[string]any{"name": "不能跳过已停用的工作区模型", "model_id": shortID}, "model_unavailable")
	f.expect(t, "list_employee_resources", map[string]any{"resource_type": "skill", "purpose": "create"}, "invalid_employee_management_request")
	f.expect(t, "list_employee_resources", map[string]any{"resource_type": "model", "purpose": "create", "employee_id": employee}, "invalid_employee_management_request")
	f.expect(t, "update_employee", map[string]any{"config_key": "profile", "name": "撤权后不修改", "version": 2}, "employee_management_forbidden")
}

func assertOfficialManagementCatalogVisible(t *testing.T, db *sql.DB, f managementMCPFixture) {
	t.Helper()
	skill, skillVersion, plugin, pluginVersion, hidden, hiddenVersion := ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO platform_skills(id,name,description,version,current_version_id,listed) VALUES($1,'Official Skill','公共技能',1,$2,true)`, skill, skillVersion)
	must(`INSERT INTO platform_skill_versions(id,skill_id,sequence,technical_name,description,manifest,source,storage_identity,key_prefix)
 VALUES($1,$2,1,'official-skill','','{}','{}','local','test')`, skillVersion, skill)
	must(`INSERT INTO platform_plugins(id,name,description,version,current_version_id,listed) VALUES($1,'Official Plugin','官方插件',1,$2,true),($3,'Hidden Plugin','未上架',1,$4,false)`, plugin, pluginVersion, hidden, hiddenVersion)
	must(`INSERT INTO platform_plugin_versions(id,plugin_id,version,name,description,endpoint,auth_type)
 VALUES($1,$2,1,'Official Plugin','','https://mcp.example.test','none'),($3,$4,1,'Hidden Plugin','','https://mcp.example.test','none')`, pluginVersion, plugin, hiddenVersion, hidden)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{"skill": skill, "mcp": plugin} {
		page := f.expect(t, "list_employee_resources", map[string]any{"resource_type": kind, "id": id}, "")
		items := page["items"].([]any)
		if len(items) != 1 {
			t.Fatal(kind, page)
		}
		item := items[0].(map[string]any)
		if item["source"] != "platform" || item["id"] != id || item["endpoint"] != nil {
			t.Fatal(item)
		}
	}
	hiddenPage := f.expect(t, "list_employee_resources", map[string]any{"resource_type": "mcp", "id": hidden}, "")
	if hiddenPage["total"] != float64(0) {
		t.Fatal(hiddenPage)
	}
}

func TestEmployeeManagementMCPRealChannelIdentity(t *testing.T) {
	for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) { testEmployeeManagementChannel(t, provider) })
	}
}

func testEmployeeManagementChannel(t *testing.T, provider string) {
	db, _, _, w, owner, employee, _ := managerFixture(t)
	binding, principal, c, s, k, i := ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID(), ps.NewID()
	reviewExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,identity_verified_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access) VALUES($1,$2,$3,$4,1,1,1,1,true,'app-one','tenant-one','bot','\x01','{}','{}','{"mode":"all"}')`, binding, w, employee, provider)
	reviewExec(t, db, `INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id) VALUES($1,$2,$3,$4,'tenant-one','open-one')`, principal, w, binding, provider)
	reviewExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id) VALUES($1,$2,$3,$4,$5,'chat-one','direct','tenant-one')`, c, w, employee, principal, binding)
	reviewExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,conversation_id,agent_id) VALUES($1,$2,$3,$4)`, k, w, c, employee)
	reviewExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,channel_created_by,task_id,mode,configuration) VALUES($1,$2,$3,$4,$5,$6,'task','{}')`, s, w, employee, c, principal, k)
	reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text) SELECT $1,$2,$3,$4,1,admission_epoch,1,'修改当前员工' FROM platform_agents WHERE id=$5`, i, w, s, principal, employee)
	f := managementMCP(t, db, w, s, i)
	reviewExec(t, db, `UPDATE platform_workspaces SET assistant_agent_id=$2 WHERE id=$1`, w, employee)
	if status, list := f.rpc(t, "tools/list", "", nil); status != 200 || len(list["tools"].([]any)) != 3 {
		t.Fatal("IM KIKO must not expose Web-only tools", status, list)
	}
	p := map[string]any{"config_key": "profile", "name": "飞书维护人改名", "version": 1}
	f.expect(t, "update_employee", p, "employee_management_forbidden")
	f.expect(t, "list_employee_resources", map[string]any{"resource_type": "model"}, "employee_management_forbidden")
	reviewExec(t, db, `INSERT INTO platform_agent_channel_maintainers(workspace_id,agent_id,binding_id,identity_epoch,external_id,ref,display_name,granted_by) VALUES($1,$2,$3,1,'open-one','{}','Synthetic Maintainer',$4)`, w, employee, binding, owner)
	f.expect(t, "update_employee", p, "")
	got := f.expect(t, "get_employee", map[string]any{"config_key": "profile"}, "")
	if got["name"] != "飞书维护人改名" {
		t.Fatal(got)
	}
	reviewExec(t, db, `UPDATE manager_inputs SET source_kind='human_confirmation' WHERE id=$1`, i)
	if status, _ := f.rpc(t, "tools/list", "", nil); status != 403 {
		t.Fatal("continuation was allowed to use management tools:", status)
	}
	reviewExec(t, db, `UPDATE manager_inputs SET source_kind='user' WHERE id=$1`, i)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=2 WHERE id=$1`, binding)
	if status, _ := f.rpc(t, "tools/list", "", nil); status != 403 {
		t.Fatal("stale binding accepted:", status)
	}
}
