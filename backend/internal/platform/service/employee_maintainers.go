package service

import "context"

type EmployeeMaintainers struct {
	Version int64             `json:"version"`
	People  []PeopleCandidate `json:"people"`
}

type EmployeeMaintainersUpdate struct {
	Version int64          `json:"version"`
	People  []SelectionRef `json:"people"`
}

type employeeMaintainersStore interface {
	ReadMaintainers(context.Context, string, string, string) (EmployeeMaintainers, error)
	ReplaceMaintainers(context.Context, string, string, string, EmployeeMaintainersUpdate) (EmployeeMaintainers, error)
}

func (s *AgentService) Maintainers(ctx context.Context, w, actor, employee string) (EmployeeMaintainers, error) {
	store, ok := s.Store.(employeeMaintainersStore)
	if !ok {
		return EmployeeMaintainers{}, Fail("employee_maintainers_unavailable", 503, "维护人配置暂不可用")
	}
	return store.ReadMaintainers(ctx, w, actor, employee)
}

func (s *AgentService) SaveMaintainers(ctx context.Context, w, actor, employee string, v EmployeeMaintainersUpdate) (EmployeeMaintainers, error) {
	if v.Version < 1 || v.People == nil || len(v.People) > 100 {
		return EmployeeMaintainers{}, Fail("invalid_maintainers", 400, "请提供当前版本及有效的个人白名单（最多 100 人）")
	}
	for _, ref := range v.People {
		if ref.Kind != "user" || ref.WorkspaceID != w || ref.AgentID != employee || ref.ExternalID == "" || len(ref.ExternalID) > 512 {
			return EmployeeMaintainers{}, Fail("invalid_maintainers", 400, "维护名单仅接受当前员工的个人身份")
		}
	}
	store, ok := s.Store.(employeeMaintainersStore)
	if !ok {
		return EmployeeMaintainers{}, Fail("employee_maintainers_unavailable", 503, "维护人配置暂不可用")
	}
	return store.ReplaceMaintainers(ctx, w, actor, employee, v)
}
