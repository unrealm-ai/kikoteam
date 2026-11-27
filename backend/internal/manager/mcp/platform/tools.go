package platform

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Directory interface {
	Read(context.Context, ps.EmployeeManagementCaller, string) (ps.Agent, error)
	Employees(context.Context, ps.EmployeeManagementCaller, ps.ManagementQuery) (ps.ManagementPage[ps.EmployeeSummary], error)
	Resources(context.Context, ps.EmployeeManagementCaller, string, string, ps.ManagementQuery) (ps.ManagementPage[ps.ManagementResource], error)
	DefaultModel(context.Context, ps.EmployeeManagementCaller) (string, string, error)
}
type Agents interface {
	Create(context.Context, string, string, ps.AgentCreate) (ps.Agent, error)
	Update(context.Context, string, string, string, ps.AgentPatch) (ps.Agent, error)
}
type Packages interface {
	Get(context.Context, string, string, string, string) (ps.CapabilityPackage, error)
	List(context.Context, string, string, string, int, int) (ps.PackagePage, error)
	Update(context.Context, string, string, string, string, ps.PackagePatch) (ps.CapabilityPackage, error)
}
type Tools struct {
	Directory Directory
	Agents    Agents
	Packages  Packages
}

type toolResponse struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Code    string `json:"code,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func addTool[I any](sv *sdk.Server, name, description string, run func(context.Context, ps.EmployeeManagementCaller, I) (any, error)) {
	sdk.AddTool(sv, &sdk.Tool{Name: name, Description: description}, func(ctx context.Context, _ *sdk.CallToolRequest, in I) (*sdk.CallToolResult, toolResponse, error) {
		caller, ok := ps.EmployeeManagementFrom(ctx)
		if !ok {
			return nil, toolResponse{Msg: "缺少有效的员工管理身份", Code: "employee_management_forbidden"}, nil
		}
		data, err := run(ctx, caller, in)
		if err == nil {
			return nil, toolResponse{Success: true, Msg: "操作成功", Data: data}, nil
		}
		var business *ps.Error
		if errors.As(err, &business) {
			return nil, toolResponse{Msg: business.Message, Code: business.Code}, nil
		}
		if errors.Is(err, ps.ErrWorkspaceMissing) {
			return nil, toolResponse{Msg: "找不到请求的资源", Code: "not_found"}, nil
		}
		return nil, toolResponse{Msg: "员工管理操作未完成，请稍后重试", Code: "employee_management_unavailable"}, nil
	})
}

type targetInput struct {
	EmployeeID string `json:"employee_id,omitempty" jsonschema:"仅 KIKO 助手可指定同工作区其他员工的 ID；省略则是当前员工"`
}

func (v targetInput) target(c ps.EmployeeManagementCaller) (string, error) {
	if v.EmployeeID == "" {
		return c.Employee, nil
	}
	if !ps.ValidWorkspaceSubject(v.EmployeeID) {
		return "", invalid("员工 ID 无效")
	}
	return v.EmployeeID, nil
}
func invalid(s string) error { return ps.Fail("invalid_employee_management_request", 400, s) }

type getFields struct {
	Key  string `json:"config_key,omitempty" jsonschema:"省略只返回概览；配置 key 以 available_configs 为准"`
	Page int    `json:"page,omitempty" jsonschema:"只用于能力包分页，默认 1"`
}
type resourceFields struct {
	ps.ManagementQuery
	Kind string `json:"resource_type" jsonschema:"model、skill 或 mcp"`
}
type createInput struct {
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	ModelID     string `json:"model_id,omitempty" jsonschema:"取自 list_employee_resources 的 id；省略时优先用工作区默认模型，其次平台默认模型"`
	ModelSource string `json:"model_source,omitempty" jsonschema:"指定模型时使用资源列表返回的 source（workspace 或 platform）"`
}
type bindingInput struct {
	PackageID  string `json:"package_id" jsonschema:"从 get_employee 的 capability_packages 取目标能力包的 id"`
	Kind       string `json:"resource_type" jsonschema:"skill 或 mcp"`
	Action     string `json:"action" jsonschema:"add 或 remove"`
	ResourceID string `json:"resource_id" jsonschema:"添加时用资源列表的 id；移除时用目标包已挂载的 skill_id 或 plugin_id"`
	VersionID  string `json:"resource_version_id,omitempty" jsonschema:"添加时填写查询到的资源版本 ID，移除时无需填写"`
}
type updateFields struct {
	Key         string        `json:"config_key" jsonschema:"profile、instruction 或 capability_packages"`
	Version     int64         `json:"version" jsonschema:"从同一 config_key 读取的当前版本；能力包填该包版本"`
	Name        *string       `json:"name,omitempty"`
	Role        *string       `json:"role,omitempty"`
	Instruction *string       `json:"instruction,omitempty"`
	Binding     *bindingInput `json:"binding,omitempty" jsonschema:"仅 config_key=capability_packages，填写一项共享 Skill 或 MCP 的增删动作"`
}

type getInput struct {
	targetInput
	getFields
}
type resourceInput struct {
	targetInput
	resourceFields
	Purpose string `json:"purpose,omitempty" jsonschema:"为创建员工选模型时填 create，且不要传 employee_id；省略用于修改已有员工"`
}
type updateInput struct {
	targetInput
	updateFields
}

func (t Tools) Server(assistant bool) *sdk.Server {
	sv := sdk.NewServer(&sdk.Implementation{Name: "employee-management", Version: "1.0.0"}, nil)
	const getDesc = "读取员工。默认只有概览；可管理时返回 available_configs，其中有配置 key 和可编辑标记。传 config_key 一次读取一项及其版本。"
	const resDesc = "分页查工作区共享模型、Skill 或 MCP，包括已上架可用的官方资源。可按名称搜索或 ID 精确过滤。目录可见不代表有安装权限；不返回凭证。"
	const updateDesc = "按 config_key 保存一项员工配置。先读取对应配置的版本；一次只保存一个模块。只执行用户明确要求的修改。"
	if !assistant {
		addTool(sv, "get_employee", "仅当前员工。"+getDesc, func(ctx context.Context, c ps.EmployeeManagementCaller, i getFields) (any, error) {
			return t.get(ctx, c, getInput{getFields: i})
		})
		addTool(sv, "list_employee_resources", resDesc+" 渠道私聊仍按当前员工管理权限查询。", func(ctx context.Context, c ps.EmployeeManagementCaller, i resourceFields) (any, error) {
			return t.resources(ctx, c, resourceInput{resourceFields: i})
		})
		addTool(sv, "update_employee", "仅当前员工。"+updateDesc, func(ctx context.Context, c ps.EmployeeManagementCaller, i updateFields) (any, error) {
			return t.update(ctx, c, updateInput{updateFields: i})
		})
	} else {
		addTool(sv, "get_employee", getDesc, t.get)
		addTool(sv, "list_employee_resources", resDesc+" 创建员工前可用 purpose=create、resource_type=model 查询，返回 id 和 source，分别作为 model_id 和 model_source。", t.resources)
		addTool(sv, "update_employee", updateDesc, t.update)
		addTool(sv, "list_employees", "在当前工作区分页查找员工；可按名称搜索或用 ID 精确过滤，只返回概览。", func(ctx context.Context, c ps.EmployeeManagementCaller, i ps.ManagementQuery) (any, error) {
			return t.Directory.Employees(ctx, c, i)
		})
		addTool(sv, "create_employee", "按用户明确要求创建 AI 员工，仅 Web 身份支持；平台会自动选头像。", t.create)
	}
	return sv
}

type configOption struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Writable    bool   `json:"writable"`
}

var configOptions = []configOption{
	{"profile", "名称和岗位职责", true},
	{"instruction", "岗位行为规范与约束", true},
	{"model", "当前基座模型及来源（只读）", false},
	{"capability_packages", "每个包的挂载与版本（一次查一页）", true},
}

func modelIdentity(a ps.Agent) (string, string) {
	if a.PlatformModelID != "" {
		return a.PlatformModelID, "platform"
	}
	return a.ModelProfileID, "workspace"
}
func (t Tools) get(ctx context.Context, c ps.EmployeeManagementCaller, i getInput) (any, error) {
	target, e := i.target(c)
	if e != nil {
		return nil, e
	}
	a, e := t.Directory.Read(ctx, c, target)
	if e != nil {
		return nil, e
	}
	if i.Key == "" {
		out := map[string]any{"overview": ps.ManagementSummary(a)}
		if a.CanManage {
			out["available_configs"] = configOptions
		}
		return out, nil
	}
	if !a.CanManage {
		return nil, ps.Fail("employee_management_forbidden", 403, "你没有管理这位 AI 员工的权限，请联系管理员")
	}
	if i.Key == "capability_packages" {
		return t.packagePage(ctx, c, target, i.Page)
	}
	return configSection(a, i.Key)
}
func configSection(a ps.Agent, key string) (any, error) {
	d := map[string]any{"config_key": key, "version": a.Version}
	switch key {
	case "profile":
		d["name"], d["role"] = a.Name, a.Role
	case "instruction":
		d["instruction"] = a.Instruction
	case "model":
		d["model_id"], d["model_source"] = modelIdentity(a)
		d["name"], d["available"], d["writable"] = a.ModelName, a.ModelAvailable, false
	default:
		return nil, invalid("请使用 get_employee 返回的配置 key")
	}
	return d, nil
}

type packageSummary struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Version  int64              `json:"version"`
	Editable bool               `json:"editable"`
	Skills   []ps.PackageSkill  `json:"skills"`
	MCPs     []ps.PackagePlugin `json:"mcps"`
}

func summarizePackage(p ps.CapabilityPackage) packageSummary {
	return packageSummary{p.ID, p.Name, p.Version, p.AssistantToolSet == "", p.Skills, p.Plugins}
}
func (t Tools) packagePage(ctx context.Context, c ps.EmployeeManagementCaller, target string, page int) (any, error) {
	if page == 0 {
		page = 1
	}
	p, e := t.Packages.List(ctx, c.Workspace, c.Actor, target, page, 20)
	if e != nil {
		return nil, e
	}
	out := ps.ManagementPage[packageSummary]{Items: []packageSummary{}, Total: p.Total, Page: p.Page, PageSize: p.PageSize}
	for _, v := range p.Items {
		out.Items = append(out.Items, summarizePackage(v))
	}
	return out, nil
}
func (t Tools) resources(ctx context.Context, c ps.EmployeeManagementCaller, i resourceInput) (any, error) {
	if i.Purpose == "create" {
		if i.Kind != "model" || i.EmployeeID != "" {
			return nil, invalid("创建员工时只查询模型，不传员工 ID")
		}
		return t.Directory.Resources(ctx, c, "", i.Kind, i.ManagementQuery)
	}
	if i.Purpose != "" && i.Purpose != "update" {
		return nil, invalid("purpose 应为 create 或 update")
	}
	target, e := i.target(c)
	if e != nil {
		return nil, e
	}
	return t.Directory.Resources(ctx, c, target, i.Kind, i.ManagementQuery)
}

// Keep the preset range in sync with the portraits served under web/public/employee-avatars.
const portraitPresetCount = 27

func (t Tools) create(ctx context.Context, c ps.EmployeeManagementCaller, i createInput) (any, error) {
	if c.Channel {
		return nil, ps.Fail("employee_creation_web_only", 403, "请使用有创建权限的 Web 账号创建员工")
	}
	id, source := i.ModelID, i.ModelSource
	if id == "" && source == "" {
		var e error
		id, source, e = t.Directory.DefaultModel(ctx, c)
		if e != nil {
			return nil, e
		}
	}
	r := ps.AgentCreate{Name: i.Name, Role: i.Role, Avatar: fmt.Sprintf("cn-v2-%02d", rand.IntN(portraitPresetCount)+1), Instruction: i.Instruction}
	switch source {
	case "workspace":
		r.ModelProfileID = id
	case "platform":
		r.PlatformModelID = id
	default:
		return nil, invalid("请提供模型 ID 和模型来源")
	}
	a, e := t.Agents.Create(ctx, c.Workspace, c.Actor, r)
	if e != nil {
		return nil, e
	}
	return ps.ManagementSummary(a), nil
}

func (t Tools) update(ctx context.Context, c ps.EmployeeManagementCaller, i updateInput) (any, error) {
	target, e := i.target(c)
	if e != nil {
		return nil, e
	}
	current, e := t.Directory.Read(ctx, c, target)
	if e != nil {
		return nil, e
	}
	if !current.CanManage {
		return nil, ps.Fail("employee_management_forbidden", 403, "你没有管理这位 AI 员工的权限，请联系管理员")
	}
	if i.Version < 1 {
		return nil, invalid("请先读取要修改的配置，并传入其当前版本")
	}
	if e = i.validateSection(); e != nil {
		return nil, e
	}
	patch := ps.AgentPatch{Version: i.Version, Name: i.Name, Role: i.Role, Instruction: i.Instruction}
	if i.Key == "capability_packages" {
		return t.updateBinding(ctx, c, target, i.Version, *i.Binding)
	}
	a, e := t.Agents.Update(ctx, c.Workspace, c.Actor, target, patch)
	if e != nil {
		return nil, e
	}
	return configSection(a, i.Key)
}

func (i updateInput) validateSection() error {
	if i.Key == "model" {
		return invalid("此工具不支持修改模型，请在网页员工设置中保存")
	}
	if i.Key == "avatar" {
		return invalid("此工具不支持修改员工头像")
	}
	n := 0
	if i.Name != nil || i.Role != nil {
		n++
	}
	if i.Instruction != nil {
		n++
	}
	if i.Binding != nil {
		n++
	}
	if n == 1 {
		switch i.Key {
		case "profile":
			if i.Name != nil || i.Role != nil {
				return nil
			}
		case "instruction":
			if i.Instruction != nil {
				return nil
			}
		case "capability_packages":
			if i.Binding != nil {
				return nil
			}
		}
	}
	return invalid("一次只提交与 config_key 对应的一项配置")
}

func (t Tools) updateBinding(ctx context.Context, c ps.EmployeeManagementCaller, target string, version int64, b bindingInput) (any, error) {
	if !ps.ValidWorkspaceSubject(b.PackageID) || !ps.ValidWorkspaceSubject(b.ResourceID) || b.Action != "add" && b.Action != "remove" ||
		b.Action == "add" && !ps.ValidWorkspaceSubject(b.VersionID) || b.Action == "remove" && b.VersionID != "" ||
		b.Kind != "skill" && b.Kind != "mcp" {
		return nil, invalid("请提供有效的包 ID、动作、资源类型、资源 ID 和对应版本")
	}
	p, e := t.Packages.Get(ctx, c.Workspace, c.Actor, target, b.PackageID)
	if e != nil {
		return nil, e
	}
	if p.AssistantToolSet != "" {
		return nil, ps.Fail("builtin_package_read_only", 409, "不能修改内置包的挂载")
	}
	if p.Version != version {
		return nil, ps.Fail("package_version_conflict", 409, "能力包已更新，请重新查询")
	}
	patch := ps.PackagePatch{Version: version}
	if b.Kind == "skill" {
		patch.Skills, e = changeBindings(p.Skills, func(v ps.PackageSkill) string { return v.SkillID }, ps.PackageSkill{SkillID: b.ResourceID, VersionID: b.VersionID}, b)
	} else {
		patch.Plugins, e = changeBindings(p.Plugins, func(v ps.PackagePlugin) string { return v.PluginID }, ps.PackagePlugin{PluginID: b.ResourceID, VersionID: b.VersionID}, b)
	}
	if e != nil {
		return nil, e
	}
	if patch.Skills == nil && patch.Plugins == nil {
		return summarizePackage(p), nil
	}
	p, e = t.Packages.Update(ctx, c.Workspace, c.Actor, target, b.PackageID, patch)
	if e != nil {
		return nil, e
	}
	return summarizePackage(p), nil
}
func changeBindings[T comparable](items []T, id func(T) string, add T, b bindingInput) (*[]T, error) {
	idx := slices.IndexFunc(items, func(v T) bool { return id(v) == b.ResourceID })
	if b.Action == "add" {
		if idx < 0 {
			items = append(items, add)
			return &items, nil
		}
		if items[idx] == add {
			return nil, nil
		}
		return nil, ps.Fail("resource_already_attached", 409, "资源已挂载；本工具不更换已挂载资源的版本")
	}
	if idx < 0 {
		return nil, nil
	}
	items = slices.Delete(items, idx, idx+1)
	return &items, nil
}
