package service

import "testing"

func TestAgentUsageCapabilitiesAndSafeDefault(t *testing.T) {
	creator := NewID()
	v := DefaultAgentUsage(creator)
	if err := ValidateAgentUsage(v); err != nil {
		t.Fatal(err)
	}
	if v.Access.Mode != "allowlist" || len(v.Access.Allow) != 1 || v.Access.Allow[0].ID != creator || v.History.Enabled {
		t.Fatal(v)
	}
	cases := map[string]func(*AgentUsage){
		"keyword dingtalk": func(v *AgentUsage) {
			v.Trigger.Keywords = []UsagePattern{{Text: "help", Channels: []string{"dingtalk"}}}
		},
		"regex wecom":   func(v *AgentUsage) { v.Trigger.Regexes = []UsagePattern{{Text: ".*", Channels: []string{"wecom"}}} },
		"invalid regex": func(v *AgentUsage) { v.Trigger.Regexes = []UsagePattern{{Text: "[", Channels: []string{"web"}}} },
		"duplicate channel": func(v *AgentUsage) {
			v.Trigger.Keywords = []UsagePattern{{Text: "help", Channels: []string{"web", "web"}}}
		},
		"history dingtalk":      func(v *AgentUsage) { v.History.Channels = []string{"dingtalk"} },
		"history empty enabled": func(v *AgentUsage) { v.History.Enabled = true },
		"web external identity": func(v *AgentUsage) { v.Access.Allow[0].BindingID = NewID() },
		"external no epoch": func(v *AgentUsage) {
			v.Access.Allow = []UsageSelector{{Channel: "feishu", Kind: "user", ID: "ou_a", BindingID: NewID()}}
		},
		"wecom department": func(v *AgentUsage) {
			v.Access.Allow = []UsageSelector{{Channel: "wecom", Kind: "department", ID: "dep", BindingID: NewID(), IdentityEpoch: 1}}
		},
		"nil list": func(v *AgentUsage) { v.Access.Deny = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := DefaultAgentUsage(creator)
			mutate(&v)
			if ValidateAgentUsage(v) == nil {
				t.Fatal("invalid capability accepted")
			}
		})
	}
}
