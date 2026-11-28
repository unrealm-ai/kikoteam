package platform

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type fixture struct {
	agent  ps.Agent
	pkg    ps.CapabilityPackage
	caller ps.EmployeeManagementCaller
}

func (f *fixture) Read(_ context.Context, c ps.EmployeeManagementCaller, id string) (ps.Agent, error) {
	if c != f.caller || id != f.agent.ID {
		return ps.Agent{}, invalid("wrong identity")
	}
	return f.agent, nil
}
func (*fixture) Employees(context.Context, ps.EmployeeManagementCaller, ps.ManagementQuery) (ps.ManagementPage[ps.EmployeeSummary], error) {
	return ps.ManagementPage[ps.EmployeeSummary]{}, nil
}
func (*fixture) Resources(_ context.Context, _ ps.EmployeeManagementCaller, _ string, kind string, q ps.ManagementQuery) (ps.ManagementPage[ps.ManagementResource], error) {
	return ps.ManagementPage[ps.ManagementResource]{Page: q.Page, Items: []ps.ManagementResource{{ID: q.ID, Name: q.Name, Source: kind}}}, nil
}
func (*fixture) ResolveModel(context.Context, ps.EmployeeManagementCaller, string) (string, string, error) {
	return "00000000-0000-0000-0000-000000000010", "workspace", nil
}
func (*fixture) Create(context.Context, string, string, ps.AgentCreate) (ps.Agent, error) {
	return ps.Agent{}, nil
}
func (f *fixture) Update(_ context.Context, _, actor, id string, p ps.AgentPatch) (ps.Agent, error) {
	if actor != f.caller.Actor || id != f.agent.ID || p.Version != f.agent.Version {
		return ps.Agent{}, invalid("wrong identity/version")
	}
	f.agent.Version++
	if p.Instruction != nil {
		f.agent.Instruction = *p.Instruction
	}
	if p.ModelProfileID != nil {
		f.agent.ModelProfileID = *p.ModelProfileID
	}
	return f.agent, nil
}
func (f *fixture) Get(context.Context, string, string, string, string) (ps.CapabilityPackage, error) {
	return f.pkg, nil
}
func (f *fixture) List(context.Context, string, string, string, int, int) (ps.PackagePage, error) {
	return ps.PackagePage{Items: []ps.CapabilityPackage{f.pkg}}, nil
}
func (f *fixture) UpdatePackage(p ps.PackagePatch) (ps.CapabilityPackage, error) {
	f.pkg.Version++
	f.pkg.Skills = *p.Skills
	return f.pkg, nil
}

type fixturePackages struct{ *fixture }

func (f fixturePackages) Update(_ context.Context, _, _, _, _ string, p ps.PackagePatch) (ps.CapabilityPackage, error) {
	return f.UpdatePackage(p)
}

func TestMCPManagementContract(t *testing.T) {
	ctx := t.Context()
	const employee = "00000000-0000-0000-0000-000000000001"
	const pkg = "00000000-0000-0000-0000-000000000002"
	const skill = "00000000-0000-0000-0000-000000000003"
	const version = "00000000-0000-0000-0000-000000000004"
	f := &fixture{agent: ps.Agent{ID: employee, Name: "测试", Instruction: "保密岗位设定", Version: 3}, pkg: ps.CapabilityPackage{ID: pkg, Version: 1}, caller: ps.EmployeeManagementCaller{Employee: employee, Actor: "实际操作者"}}
	handler := Tools{Directory: f, Agents: f, Packages: fixturePackages{f}}
	for _, assistant := range []bool{false, true} {
		a, b := sdk.NewInMemoryTransports()
		s, e := handler.Server(assistant).Connect(ps.WithEmployeeManagement(ctx, f.caller), a, nil)
		if e != nil {
			t.Fatal(e)
		}
		client, e := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
		if e != nil {
			t.Fatal(e)
		}
		tools, e := client.ListTools(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		names := []string{}
		for _, x := range tools.Tools {
			names = append(names, x.Name)
			raw, err := json.Marshal(x.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc([]string{"workspace_id", "actor_id", "user_id"}, func(s string) bool { return strings.Contains(string(raw), s) }) || !assistant && strings.Contains(string(raw), "employee_id") {
				t.Fatal("identity or scoped target escaped into schema", string(raw))
			}
			if x.Name == "create_employee" && (strings.Contains(string(raw), `"model_source"`) || !strings.Contains(string(raw), `"model_id"`)) {
				t.Fatal("only the optional short model ID should be advertised", string(raw))
			}
			if (x.Name == "create_employee" || x.Name == "update_employee") && strings.Contains(string(raw), `"avatar"`) {
				t.Fatal("avatar escaped into mutation schema", string(raw))
			}
			if x.Name == "update_employee" && slices.ContainsFunc([]string{
				`"model_id"`, `"model_source"`, `"web_access"`, `"usage_config"`, `"scope"`, `"trigger_scope"`, `"conversation_scope"`, `"maintainers"`, `"enabled"`,
			}, func(key string) bool { return strings.Contains(string(raw), key) }) {
				t.Fatal("model, access, or activation controls must stay out of the model-facing schema", string(raw))
			}
			if x.Name == "list_employee_resources" && strings.Contains(string(raw), `"purpose"`) != assistant {
				t.Fatal("creation selector must only be advertised by the Web assistant")
			}
		}
		slices.Sort(names)
		want := []string{"get_employee", "list_employee_resources", "update_employee"}
		if assistant {
			want = []string{"create_employee", "get_employee", "list_employee_resources", "list_employees", "update_employee"}
		}
		if !slices.Equal(names, want) {
			t.Fatalf("tools %v", names)
		}
		if assistant {
			_ = client.Close()
			_ = s.Close()
			continue
		}
		call := func(name string, args map[string]any) map[string]any {
			t.Helper()
			r, e := client.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
			if e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(r.StructuredContent)
			if e != nil {
				t.Fatal(e)
			}
			var v map[string]any
			if e = json.Unmarshal(b, &v); e != nil || v == nil {
				t.Fatalf("bad response %s %#v %v", b, r.Content, e)
			}
			return v
		}
		check := func(name string, args map[string]any, want bool) map[string]any {
			t.Helper()
			r := call(name, args)
			if r["success"] != want {
				t.Fatal(name, r)
			}
			return r
		}
		overview := check("get_employee", nil, true)["data"].(map[string]any)
		if len(overview) != 2 || overview["available_configs"] != nil {
			t.Fatal("unauthorized catalog", overview)
		}
		check("get_employee", map[string]any{"config_key": "instruction"}, false)
		f.agent.CanManage = true
		overview = check("get_employee", nil, true)["data"].(map[string]any)
		if len(overview) != 3 || overview["available_configs"] == nil {
			t.Fatal(overview)
		}
		check("update_employee", map[string]any{"config_key": "instruction", "instruction": "新设定", "version": 3}, true)
		read := check("get_employee", map[string]any{"config_key": "instruction"}, true)["data"].(map[string]any)
		if read["instruction"] != "新设定" || read["version"] != float64(4) {
			t.Fatal(read)
		}
		check("update_employee", map[string]any{"config_key": "instruction", "instruction": "旧版本", "version": 3}, false)
		check("update_employee", map[string]any{"config_key": "profile", "name": "不应用", "instruction": "混合", "version": 4}, false)
		check("list_employee_resources", map[string]any{"resource_type": "skill", "id": skill, "name": "物流", "page": 2}, true)
		check("update_employee", map[string]any{"config_key": "capability_packages", "version": 1, "binding": map[string]any{"package_id": pkg, "resource_type": "skill", "action": "add", "resource_id": skill, "resource_version_id": version}}, true)
		read = check("get_employee", map[string]any{"config_key": "capability_packages"}, true)["data"].(map[string]any)
		p := read["items"].([]any)[0].(map[string]any)
		if p["version"] != float64(2) || p["skills"].([]any)[0].(map[string]any)["version_id"] != version {
			t.Fatal(p)
		}
		check("update_employee", map[string]any{"config_key": "capability_packages", "version": 2, "binding": map[string]any{"package_id": pkg, "resource_type": "skill", "action": "remove", "resource_id": skill}}, true)
		if len(f.pkg.Skills) != 0 {
			t.Fatal(f.pkg.Skills)
		}
		_ = client.Close()
		_ = s.Close()
	}
}
