package service

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
	AgentID string          `json:"agent_id,omitempty"`
	Cards   []AssistantCard `json:"cards"`
	Pending bool            `json:"pending,omitempty"`
}

type AssistantCard struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Input       string `json:"input"`
}

func AssistantCards() []AssistantCard {
	return []AssistantCard{
		{"complaints", "客诉太多，处理不过来？", "两单分开查，哪单先查清就先回报。", "有两起急客诉，帮我拆成两个独立任务同时查，哪单先查清就先告诉我。1. ORD-1042 显示驿站代收，客户就在驿站却找不到，在线等回复。2. ORD-1087 收到的商品型号不对，客户明早活动要用。每个子任务只查自己那一单，用订单号核对订单、物流和客服记录。三项查齐就回报，不用会议、群历史或告警工具。给我关键证据、下一步和一段可直接复制给客户的回复。先别实际联系、退款或补发；回复模板不要声称建议动作已经执行，也不要让客户重复提供已有证据。"},
		{"meeting", "会开完了，谁来跟进？", "核对会后进展，把卡住上线的决定确认下来。", "MEET-301 刚结束，请开一个跟进任务，把会上定的事往前推进。用会议编号从现有系统查会议纪要、发布计划和会后的项目群，不用我重新上传。先列出现在能做的事和周五放行条件。已有负责人和反馈时间的事项直接排入清单，不用再问我。如果仍有会卡住上线的决定，先找出最关键的一项，只发一张提交卡，卡上只留一个简短的文字输入框。收到答复后再把结论补回原任务，给我最终清单和可复制的群消息草稿，不要真的发群。"},
		{"incident", "半夜支付成功率告警，没人管？", "没人盯着，助手接到告警后自己排查，恢复了再回报。", "【P1 告警｜支付服务】\n02:13（北京时间）触发：近 5 分钟支付成功率从 99.7% 降到 93.2%，低于告警阈值 95%。\n告警编号：INC-207\n失败请求 Trace ID：tr-pay-207-a81f\n当前状态：持续告警，尚未恢复。\n处置要求：请开一个后台任务，用当前环境里的告警处置工具排查并按值班手册恢复，完成后回报。不要操作真实生产系统。"},
	}
}

type AssistantToolDescription struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
