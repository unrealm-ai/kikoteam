package service

import (
	"context"
	"github.com/unrealm-ai/kikoteam/shared/trigger"
	"strings"
	"unicode/utf8"
)

// AgentUsage is the employee-level policy source. Channel projections must never
// become independently writable policy. Group access may bypass allow rules, never deny rules.
type AgentUsage struct {
	Access  UsageAccess  `json:"access"`
	Trigger UsageTrigger `json:"trigger"`
	History UsageHistory `json:"history"`
}
type UsageAccess struct {
	GroupMembersEnabled bool            `json:"group_members_enabled"`
	Mode                string          `json:"mode"`
	Allow               []UsageSelector `json:"allow"`
	Deny                []UsageSelector `json:"deny"`
}
type UsageSelector struct {
	Channel            string `json:"channel"`
	Kind               string `json:"kind"`
	ID                 string `json:"id"`
	BindingID          string `json:"binding_id,omitempty"`
	IdentityEpoch      int64  `json:"identity_epoch,omitempty"`
	IncludeDescendants bool   `json:"include_descendants,omitempty"`
}
type UsageTrigger struct {
	Mention  bool           `json:"mention"`
	Keywords []UsagePattern `json:"keywords"`
	Regexes  []UsagePattern `json:"regexes"`
}
type UsagePattern struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
	Text        string   `json:"text"`
	Channels    []string `json:"channels"`
}
type UsageHistory struct {
	Enabled  bool     `json:"enabled"`
	Limit    int      `json:"limit"`
	Channels []string `json:"channels"`
}
type AgentUsageResource struct {
	// Request-only explicit administrator acknowledgement when replacing legacy access.
	ReplaceLegacyAccess bool       `json:"replace_legacy_access,omitempty"`
	Version             int64      `json:"version"`
	UsageConfig         AgentUsage `json:"usage_config"`
}
type AgentUsageStore interface {
	ReadUsage(context.Context, string, string, string) (AgentUsageResource, error)
	ReplaceUsage(context.Context, string, string, string, AgentUsageResource) (AgentUsageResource, error)
}
type AgentUsageService struct{ Store AgentUsageStore }

func (s *AgentUsageService) Get(ctx context.Context, w, actor, id string) (AgentUsageResource, error) {
	return s.Store.ReadUsage(ctx, w, actor, id)
}
func (s *AgentUsageService) Update(ctx context.Context, w, actor, id string, v AgentUsageResource) (AgentUsageResource, error) {
	if v.Version < 1 {
		return AgentUsageResource{}, Fail("invalid_usage_config", 400, "使用配置版本无效")
	}
	if err := ValidateAgentUsage(v.UsageConfig); err != nil {
		return AgentUsageResource{}, err
	}
	return s.Store.ReplaceUsage(ctx, w, actor, id, v)
}
func DefaultAgentUsage(creator string) AgentUsage {
	v := AgentUsage{Access: UsageAccess{GroupMembersEnabled: true, Mode: "allowlist", Allow: []UsageSelector{}, Deny: []UsageSelector{}}, Trigger: UsageTrigger{Mention: true, Keywords: []UsagePattern{}, Regexes: []UsagePattern{}}, History: UsageHistory{Limit: 20, Channels: []string{}}}
	if creator != "" {
		v.Access.Allow = append(v.Access.Allow, UsageSelector{Channel: "web", Kind: "user", ID: creator})
	}
	return v
}

func InitialAgentUsage() AgentUsage {
	usage := DefaultAgentUsage("")
	usage.Access.Mode = "all"
	return usage
}
func ValidateAgentUsage(v AgentUsage) error {
	fail := func() error { return Fail("invalid_usage_config", 400, "使用配置或渠道能力无效") }
	if v.Access.Mode != "all" && v.Access.Mode != "allowlist" {
		return fail()
	}
	for _, list := range [][]UsageSelector{v.Access.Allow, v.Access.Deny} {
		if list == nil || len(list) > 200 {
			return fail()
		}
		seen := map[UsageSelector]bool{}
		for _, item := range list {
			if !validUsageSelector(item) || seen[item] {
				return fail()
			}
			seen[item] = true
		}
	}
	ids := map[string]bool{}
	for index, list := range [][]UsagePattern{v.Trigger.Keywords, v.Trigger.Regexes} {
		if list == nil || len(list) > 32 || index == 1 && len(list) > 16 {
			return fail()
		}
		seen := map[string]bool{}
		for _, item := range list {
			if !validPatternMetadata(item, ids) {
				return fail()
			}
			if item.ID != "" {
				ids[item.ID] = true
			}
			if strings.TrimSpace(item.Text) == "" || utf8.RuneCountInString(item.Text) > 256 || strings.ContainsRune(item.Text, '\x00') || !usageChannels(item.Channels, "web", "feishu") {
				return fail()
			}
			if _, err := trigger.Compile([]string{"keyword", "regex"}[index], item.Text); err != nil {
				return Fail("invalid_trigger_pattern", 400, err.Error())
			}
			for _, ch := range item.Channels {
				key := ch + "\x00" + item.Text
				if seen[key] {
					return fail()
				}
				seen[key] = true
			}
		}
	}
	if v.History.Limit < 1 || v.History.Limit > 100 || v.History.Channels == nil || (len(v.History.Channels) > 0 && !usageChannels(v.History.Channels, "web", "feishu", "wecom")) || v.History.Enabled && len(v.History.Channels) == 0 {
		return fail()
	}
	return nil
}
func validUsageSelector(v UsageSelector) bool {
	if v.ID == "" || len(v.ID) > 512 || strings.TrimSpace(v.ID) != v.ID || strings.ContainsAny(v.ID, "\x00\r\n") {
		return false
	}
	if v.Kind != "user" && v.Kind != "department" && v.Kind != "group" || v.IncludeDescendants && v.Kind != "department" {
		return false
	}
	if v.Channel == "web" {
		return v.Kind != "department" && agentUUID(v.ID) && v.BindingID == "" && v.IdentityEpoch == 0
	}
	if v.Channel != "feishu" && v.Channel != "dingtalk" && v.Channel != "wecom" {
		return false
	}
	if v.Channel == "wecom" && v.Kind == "department" {
		return false
	}
	return agentUUID(v.BindingID) && v.IdentityEpoch > 0
}
func usageChannels(channels []string, allowed ...string) bool {
	if len(channels) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, ch := range channels {
		found := false
		for _, ok := range allowed {
			found = found || ch == ok
		}
		if !found || seen[ch] {
			return false
		}
		seen[ch] = true
	}
	return true
}

func validPatternMetadata(item UsagePattern, ids map[string]bool) bool {
	if item.ID != "" && (len(item.ID) > 128 || strings.ContainsAny(item.ID, "\x00\r\n") || ids[item.ID]) {
		return false
	}
	return utf8.RuneCountInString(item.Name) <= 100 && utf8.RuneCountInString(item.Description) <= 1000
}

// SimplifiedUsageAccess reports whether access is representable by the employee
// personnel-only editor. Historical policies remain readable and enforceable.
func SimplifiedUsageAccess(v UsageAccess) bool {
	if v.Mode != "all" && v.Mode != "allowlist" {
		return false
	}
	if v.Mode == "all" && len(v.Allow) != 0 || v.Mode == "allowlist" && len(v.Deny) != 0 {
		return false
	}
	for _, list := range [][]UsageSelector{v.Allow, v.Deny} {
		for _, ref := range list {
			if ref.Kind != "user" && ref.Kind != "department" {
				return false
			}
		}
	}
	return true
}
