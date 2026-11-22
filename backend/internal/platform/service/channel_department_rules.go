package service

import "strings"

// NormalizeDepartmentRules keeps legacy IDs as a projection, never as an implicit
// recursive grant. An explicit rules array (including empty) is authoritative.
func NormalizeDepartmentRules(access *ChannelAccess) error {
	for _, pair := range []struct {
		ids   *[]string
		rules *[]ChannelDepartmentRule
	}{{&access.AllowDepartmentIDs, &access.AllowDepartmentRules}, {&access.DenyDepartmentIDs, &access.DenyDepartmentRules}} {
		if *pair.rules == nil {
			*pair.rules = []ChannelDepartmentRule{}
			for _, id := range *pair.ids {
				*pair.rules = append(*pair.rules, ChannelDepartmentRule{DepartmentID: id})
			}
		}
		if len(*pair.rules) > 100 {
			return Fail("invalid_channel", 400, "部门规则条目过多")
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, rule := range *pair.rules {
			id := rule.DepartmentID
			if strings.TrimSpace(id) == "" || len(id) > 512 || strings.ContainsAny(id, "\r\n\x00") || seen[id] {
				return Fail("invalid_channel", 400, "部门规则无效或重复")
			}
			seen[id] = true
			ids = append(ids, id)
		}
		*pair.ids = ids
	}
	return nil
}

func HasRecursiveDepartmentRule(rules []ChannelDepartmentRule) bool {
	for _, rule := range rules {
		if rule.IncludeDescendants {
			return true
		}
	}
	return false
}
