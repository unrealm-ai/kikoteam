package service

import "testing"

func TestTriggerRulesMetadataPreviewAndLegacy(t *testing.T) {
	v := AgentUsageResource{Version: 3, UsageConfig: DefaultAgentUsage("")}
	v.UsageConfig.Trigger.Keywords = []UsagePattern{{Text: "help", Channels: []string{"feishu", "web"}}}
	r := rulesFromUsage(v)
	if len(r.Rules) != 1 || !r.Rules[0].Enabled || r.Rules[0].ID == "" {
		t.Fatal(r)
	}
	v.UsageConfig.Trigger.Keywords[0].Channels = []string{"web", "feishu"}
	if rulesFromUsage(v).Rules[0].ID != r.Rules[0].ID {
		t.Fatal("legacy ID changed on channel order")
	}
	rules := []TriggerRule{{ID: "one", Name: "关键词", Type: "keyword", Pattern: "help，日报", Enabled: true, Channels: []string{"feishu"}}, {ID: "two", Name: "停用", Type: "regex", Pattern: ".*", Enabled: false, Channels: []string{"feishu"}}, {ID: "three", Name: "其它渠道", Type: "keyword", Pattern: "help", Enabled: true, Channels: []string{"web"}}}
	got, err := matchTriggerRules(TriggerRulesTest{Channel: "feishu", Text: "HELP", Rules: rules})
	if err != nil || got.MatchedCount != 1 || got.Matched[0].RuleID != "one" {
		t.Fatal(got, err)
	}
	rules[0].Type = "regex"
	rules[0].Pattern = "/help/i"
	if _, err = matchTriggerRules(TriggerRulesTest{Channel: "feishu", Text: "help", Rules: rules}); err == nil {
		t.Fatal("JS syntax accepted")
	}
}
