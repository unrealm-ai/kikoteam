package service

type AssistantSettings struct {
	Version int64 `json:"version"`
	Enabled bool  `json:"enabled"`
}

type AssistantSettingsPatch struct {
	ExpectedVersion int64 `json:"expected_version"`
	Enabled         *bool `json:"enabled"`
}

func NewAssistantDemoPackage(agent string) CapabilityPackage {
	rule := PackageConversationRule{Enabled: true, Mode: "inherit_with_blacklist", Allow: []UsageSelector{}, Deny: []UsageSelector{}}
	trigger := DefaultPackageTriggerScope()
	disable := false
	trigger.Enabled = &disable
	return CapabilityPackage{
		ID: NewID(), AgentID: agent, Name: "体验能力", Description: "使用固定样例体验任务处理，不访问真实业务系统。", Enabled: true, Version: 1, AssistantToolSet: "demo",
		Scope: PackageScope{Mode: "inherit_with_blacklist", AllowMemberIDs: []string{}, DenyMemberIDs: []string{}},
		ConversationScope: &PackageConversationScope{
			Private: rule, Group: PackageConversationRule{Enabled: false, Mode: "inherit_with_blacklist", Allow: []UsageSelector{}, Deny: []UsageSelector{}},
		},
		TriggerScope: trigger, WorkScope: &WorkScope{Mode: "allowlist", EmployeeIDs: []string{}}, Skills: []PackageSkill{}, Plugins: []PackagePlugin{},
	}
}

type AssistantEntry struct {
	AgentID     string          `json:"agent_id,omitempty"`
	Cards       []AssistantCard `json:"cards"`
	Pending     bool            `json:"pending,omitempty"`
	Experienced bool            `json:"experienced,omitempty"`
}

type AssistantCard struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Input       string `json:"input"`
}

func AssistantCards() []AssistantCard {
	return []AssistantCard{
		{"complaints", "并行处理两起客诉", "同时查两单，分别给出处理方案。", "有两起急客诉，帮我拆成两个独立任务同时查，哪单先查清就先告诉我。1. ORD-1042 显示驿站代收，客户就在驿站却找不到，在线等回复。2. ORD-1087 收到的商品型号不对，客户明早活动要用。每单都核对订单、物流和客服记录，给我关键证据、下一步和一段可直接复制给客户的回复。先别实际联系、退款或补发；回复模板不要声称建议动作已经执行，也不要让客户重复提供已有证据。"},
		{"meeting", "把开完的会推进下去", "核对会后进展，缺关键信息时发确认卡。", "MEET-301 刚结束，请开一个跟进任务，把会上定的事往前推进。用会议编号从现有系统查会议纪要、发布计划和会后的项目群，不用我重新上传。先列出现在能做的事和周五放行条件。已有负责人和反馈时间的事项直接排入清单，不用再问我。如果仍有会卡住上线的决定，先找出最关键的一项，只发一张提交卡，卡上只留一个简短的文字输入框。收到答复后再把结论补回原任务，给我最终清单和可复制的群消息草稿，不要真的发群。"},
		{"incident", "排查支付成功率下跌", "排查根因，按值班手册恢复并复查。", "支付成功率突然下降，触发告警 INC-207，失败请求 trace ID 是 tr-pay-207-a81f。请开一个任务排查和恢复，按 trace ID 查日志，对照告警指标、发布记录和值班手册。如果证据一致且手册允许，按手册恢复，再核对两个独立的五分钟窗口；只有指标都达标才报告恢复。最后把原因、处理和验证结果告诉我。"},
	}
}

type AssistantToolDescription struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
