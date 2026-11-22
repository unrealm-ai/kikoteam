package service

// The profile is snapshotted at creation. Existing/manual applications never inherit
// newly requested scopes or consent to expanding their contact range.
type FeishuPermissionGroup struct {
	Note          string             `json:"note,omitempty"`
	ID            string             `json:"id"`
	Label         string             `json:"label"`
	Required      bool               `json:"required"`
	Identity      string             `json:"identity"`
	Scopes        []string           `json:"scopes"`
	Status        string             `json:"status,omitempty"`
	ScopeStates   []FeishuPermission `json:"scopeStates,omitempty"`
	MissingScopes []string           `json:"missingScopes,omitempty"`
}
type FeishuPermissionProfile struct {
	Version      string                  `json:"version"`
	ContactRange string                  `json:"contactRange,omitempty"`
	Groups       []FeishuPermissionGroup `json:"groups"`
}

const FeishuContactRangeScope = "application:application.contacts_range:write"

func FeishuProfile() *FeishuPermissionProfile {
	return &FeishuPermissionProfile{
		Version: "conversation-business-v3", ContactRange: "all", Groups: []FeishuPermissionGroup{
			{ID: "conversation-directory", Label: "基础聊天与通讯录", Required: true, Identity: "tenant", Scopes: []string{"tenant:tenant:readonly", "contact:contact.base:readonly", "contact:user.base:readonly", "contact:user.employee:readonly", "contact:user.department:readonly", "contact:department.base:readonly", "contact:department.organize:readonly", "contact:user.employee_id:readonly", "im:message.p2p_msg:readonly", "im:message.group_at_msg:readonly", "im:message:send_as_bot", "im:message:readonly", "im:message.group_msg", "im:chat:readonly"}},
			{ID: "messages-groups", Label: "消息、附件与群", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"im:resource", "im:chat:read", "im:chat.access_event.bot_p2p_chat:read", "im:chat.announcement:read", "im:chat.members:read", "im:message.group_at_msg.include_bot:readonly", "im:message.group_msg.include_bot:read", "im:message.pins:read", "im:message.reactions:read"}},
			{ID: "organization", Label: "组织、部门与成员关系", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"contact:user.basic_profile:readonly", "contact:user.dotted_line_leader_info.read", "directory:department:search", "directory:department:list", "directory:department:read", "directory:department.base:read", "directory:department.department_path:read", "directory:department.has_child:read", "directory:department.name:read", "directory:department.organization:read", "directory:department.parent_id:read", "directory:employee:list", "directory:employee:read", "directory:employee:search", "directory:employee.base.base:read", "directory:employee.base.department:read", "directory:employee.base.department_path:read", "directory:employee.base.leader:read"}},
			{ID: "documents", Label: "文档与评论读写", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"docx:document:readonly", "docx:document:create", "docx:document:write_only", "docs:document.comment:read", "docs:document.comment:create", "docs:document.comment:update", "docs:document.comment:delete"}},
			{ID: "base", Label: "多维表格读写", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"base:app:read", "base:app:create", "base:app:update", "base:app:copy", "base:table:read", "base:table:create", "base:table:update", "base:table:delete", "base:field:read", "base:field:create", "base:field:update", "base:field:delete", "base:record:retrieve", "base:record:create", "base:record:update", "base:record:delete", "base:view:read", "base:view:write_only"}},
			{ID: "calendar", Label: "日程读写与邀请", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"calendar:calendar:read", "calendar:calendar.event:read", "calendar:calendar.event:create", "calendar:calendar.event:update", "calendar:calendar.event:delete", "calendar:calendar.event:reply", "calendar:calendar.free_busy:read"}},
			{ID: "tasks", Label: "待办、清单与评论读写", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"task:task:write", "task:tasklist:write", "task:comment:write"}},
			{ID: "meetings-minutes", Label: "会议纪要与妙记读取", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"vc:meeting.search:read", "vc:meeting.meetingevent:read", "vc:note:read", "vc:record:readonly", "vc:meeting.artifact.note:read", "vc:meeting.artifact.verbatim:read", "minutes:minutes.search:read", "minutes:minutes.basic:read", "minutes:minutes.artifacts:read", "minutes:minutes.transcript:export"}},
			{ID: "approvals", Label: "审批读写（高影响）", Required: false, Note: "仅预申请权限，不表示相关业务工具已接入；执行操作仍须校验具体资源和动作授权。", Identity: "tenant", Scopes: []string{"approval:approval.list:readonly", "approval:instance", "approval:task", "approval:instance.comment"}},
			{ID: "contact-range", Label: "通讯录范围管理（全员）", Required: false, Note: "仅本次扫码新建应用：权限获批后自动设为全部员工，线上立即生效；不修改其他应用、机器人可用范围或员工使用名单。", Identity: "tenant", Scopes: []string{"application:application.contacts_range:write"}},
		}}
}
func FeishuPermissions() []FeishuPermission { return profilePermissions(FeishuProfile()) }
func profilePermissions(profile *FeishuPermissionProfile) []FeishuPermission {
	if profile == nil {
		return legacyFeishuPermissions()
	}
	out := []FeishuPermission{}
	seen := map[string]bool{}
	for _, g := range profile.Groups {
		for _, scope := range g.Scopes {
			if !seen[scope] {
				out = append(out, FeishuPermission{Scope: scope, Required: g.Required, Status: "unknown"})
				seen[scope] = true
			}
		}
	}
	return out
}
func registrationDiagnostics(profile *FeishuPermissionProfile, d FeishuDiagnostics) FeishuDiagnostics {
	inventory := map[string]string{}
	for _, p := range d.Permissions {
		inventory[p.Scope] = p.Status
	}
	d.Permissions = profilePermissions(profile)
	d.Ready = d.CredentialStatus == "pass" && d.BotStatus == "pass" && d.DirectoryStatus == "pass" && d.ScopeStatus == "pass"
	for i := range d.Permissions {
		if status, ok := inventory[d.Permissions[i].Scope]; ok {
			d.Permissions[i].Status = status
		}
		if d.Permissions[i].Required && d.Permissions[i].Status != "granted" {
			d.Ready = false
		}
	}
	if d.Ready {
		d.ErrorCode = ""
	} else if d.ErrorCode == "" {
		d.ErrorCode = "feishu_permissions_or_configuration_required"
	}
	return d
}
func permissionGroups(profile *FeishuPermissionProfile, d *FeishuDiagnostics) []FeishuPermissionGroup {
	if profile == nil {
		return nil
	}
	inventory := map[string]string{}
	if d != nil {
		for _, p := range d.Permissions {
			inventory[p.Scope] = p.Status
		}
	}
	groups := make([]FeishuPermissionGroup, 0, len(profile.Groups))
	for _, g := range profile.Groups {
		g.Status = "granted"
		g.ScopeStates = []FeishuPermission{}
		g.MissingScopes = []string{}
		for _, scope := range g.Scopes {
			status := inventory[scope]
			if status == "" {
				status = "unknown"
			}
			g.ScopeStates = append(g.ScopeStates, FeishuPermission{Scope: scope, Required: g.Required, Status: status})
			if status != "granted" {
				g.MissingScopes = append(g.MissingScopes, scope)
				if status == "missing" || g.Status == "granted" {
					g.Status = status
				}
			}
		}
		groups = append(groups, g)
	}
	return groups
}
