package service

import "slices"

type WorkScope struct {
	Mode        string   `json:"mode"`
	EmployeeIDs []string `json:"employee_ids"`
}

func (s *WorkScope) Validate(defaultMode string) error {
	if s.EmployeeIDs == nil {
		s.EmployeeIDs = []string{}
	}
	if len(s.EmployeeIDs) > 200 || (s.Mode != defaultMode && s.Mode != "allowlist" && s.Mode != "denylist") || s.Mode == defaultMode && len(s.EmployeeIDs) != 0 {
		return Fail("invalid_work_scope", 400, "交接范围模式或名单无效")
	}
	seen := map[string]bool{}
	for _, id := range s.EmployeeIDs {
		if !agentUUID(id) || seen[id] {
			return Fail("invalid_work_scope", 400, "来源员工标识无效或重复")
		}
		seen[id] = true
	}
	return nil
}
func (s WorkScope) Allows(source string) bool {
	switch s.Mode {
	case "inherit", "all":
		return true
	case "allowlist":
		return slices.Contains(s.EmployeeIDs, source)
	case "denylist":
		return !slices.Contains(s.EmployeeIDs, source)
	}
	return false
}
