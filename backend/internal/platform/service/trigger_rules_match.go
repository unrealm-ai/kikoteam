package service

import (
	"context"
	"github.com/unrealm-ai/kikoteam/shared/trigger"
)

type TriggerRulesTest struct {
	Channel string        `json:"channel"`
	Text    string        `json:"text"`
	Rules   []TriggerRule `json:"rules"`
}
type TriggerRuleMatch struct {
	RuleID      string `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	Type        string `json:"type"`
	MatchedText string `json:"matched_text"`
}
type TriggerRulesResult struct {
	MatchedCount int                `json:"matched_count"`
	Matched      []TriggerRuleMatch `json:"matched"`
}

func (s *AgentUsageService) TestRules(ctx context.Context, w, actor, id string, in TriggerRulesTest) (TriggerRulesResult, error) {
	if _, err := s.Get(ctx, w, actor, id); err != nil {
		return TriggerRulesResult{}, err
	}
	return matchTriggerRules(in)
}
func matchTriggerRules(in TriggerRulesTest) (TriggerRulesResult, error) {
	out := TriggerRulesResult{Matched: []TriggerRuleMatch{}}
	if trigger.ValidateText(in.Text) != nil || in.Channel != "web" && in.Channel != "feishu" {
		return out, Fail("invalid_trigger_test", 400, "测试文本或渠道无效")
	}
	if _, err := ruleTrigger(in.Rules); err != nil {
		return out, err
	}
	for _, r := range in.Rules {
		applies := false
		for _, c := range r.Channels {
			applies = applies || c == in.Channel
		}
		if !r.Enabled || !applies {
			continue
		}
		matcher, err := trigger.Compile(r.Type, r.Pattern)
		if err != nil {
			return out, err
		}
		matched, hit := matcher.Match(in.Text)
		if hit {
			out.Matched = append(out.Matched, TriggerRuleMatch{r.ID, r.Name, r.Type, matched})
		}
	}
	out.MatchedCount = len(out.Matched)
	return out, nil
}
