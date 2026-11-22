package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

type TriggerRule struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Pattern     string   `json:"pattern"`
	Enabled     bool     `json:"enabled"`
	Description string   `json:"description"`
	Channels    []string `json:"channels"`
}
type TriggerRulesResource struct {
	Version int64         `json:"version"`
	Rules   []TriggerRule `json:"rules"`
}

func rulesFromUsage(v AgentUsageResource) TriggerRulesResource {
	out := TriggerRulesResource{Version: v.Version, Rules: []TriggerRule{}}
	for i, list := range [][]UsagePattern{v.UsageConfig.Trigger.Keywords, v.UsageConfig.Trigger.Regexes} {
		kind := []string{"keyword", "regex"}[i]
		for _, p := range list {
			id := p.ID
			if id == "" {
				channels := append([]string{}, p.Channels...)
				sort.Strings(channels)
				id = fmt.Sprintf("legacy-%x", sha256.Sum256([]byte(kind+"\x00"+p.Text+"\x00"+strings.Join(channels, "\x00"))))
			}
			name := p.Name
			if name == "" {
				name = p.Text
				if len([]rune(name)) > 100 {
					name = string([]rune(name)[:100])
				}
			}
			out.Rules = append(out.Rules, TriggerRule{ID: id, Type: kind, Name: name, Pattern: p.Text, Enabled: p.Enabled == nil || *p.Enabled, Description: p.Description, Channels: p.Channels})
		}
	}
	return out
}
func ruleTrigger(rules []TriggerRule) (UsageTrigger, error) {
	t := UsageTrigger{Keywords: []UsagePattern{}, Regexes: []UsagePattern{}}
	if rules == nil || len(rules) > 48 {
		return t, Fail("invalid_trigger_rules", 400, "触发规则无效")
	}
	for _, r := range rules {
		if strings.TrimSpace(r.Name) == "" || r.Type != "keyword" && r.Type != "regex" {
			return t, Fail("invalid_trigger_rules", 400, "触发规则名称或类型无效")
		}
		enabled := r.Enabled
		p := UsagePattern{ID: r.ID, Name: r.Name, Description: r.Description, Text: r.Pattern, Enabled: &enabled, Channels: r.Channels}
		if r.Type == "keyword" {
			t.Keywords = append(t.Keywords, p)
		} else {
			t.Regexes = append(t.Regexes, p)
		}
	}
	if len(t.Regexes) > 16 {
		return t, Fail("invalid_trigger_rules", 400, "正则规则最多16条")
	}
	v := DefaultAgentUsage("")
	v.Trigger = t
	return t, ValidateAgentUsage(v)
}
func (s *AgentUsageService) GetRules(ctx context.Context, w, actor, id string) (TriggerRulesResource, error) {
	v, err := s.Get(ctx, w, actor, id)
	if err != nil {
		return TriggerRulesResource{}, err
	}
	return rulesFromUsage(v), nil
}
func (s *AgentUsageService) UpdateRules(ctx context.Context, w, actor, id string, in TriggerRulesResource) (TriggerRulesResource, error) {
	old, err := s.Get(ctx, w, actor, id)
	if err != nil {
		return TriggerRulesResource{}, err
	}
	if in.Version != old.Version {
		return TriggerRulesResource{}, Fail("usage_version_conflict", 409, "使用配置已更新，请刷新后重试")
	}
	known := map[string]bool{}
	for _, r := range rulesFromUsage(old).Rules {
		known[r.ID] = true
	}
	for i := range in.Rules {
		if in.Rules[i].ID == "" {
			in.Rules[i].ID = NewID()
		} else if !known[in.Rules[i].ID] {
			return TriggerRulesResource{}, Fail("invalid_trigger_rule_id", 400, "新增规则请省略ID，已有规则ID不能替换")
		}
	}
	t, err := ruleTrigger(in.Rules)
	if err != nil {
		return TriggerRulesResource{}, err
	}
	t.Mention = old.UsageConfig.Trigger.Mention
	old.UsageConfig.Trigger = t
	out, err := s.Update(ctx, w, actor, id, old)
	if err != nil {
		return TriggerRulesResource{}, err
	}
	return rulesFromUsage(out), nil
}
