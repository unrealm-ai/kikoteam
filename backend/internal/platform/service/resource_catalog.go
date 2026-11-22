package service

import (
	"context"
	"errors"
)

type ResourceListFilter struct {
	Scope string
	Query string
}

func (f *ResourceListFilter) normalize() error {
	if f.Scope == "" {
		f.Scope = "workspace"
	}
	switch f.Scope {
	case "workspace", "official", "all":
		return nil
	default:
		return Fail("invalid_scope", 400, "资源范围无效")
	}
}

type ResourcePublication struct {
	OAuth             *OAuthClientInput `json:"oauth,omitempty"`
	SourceWorkspaceID string            `json:"source_workspace_id"`
	ResourceID        string            `json:"resource_id"`
	VersionID         string            `json:"version_id"`
	Version           int64             `json:"version,omitempty"`
}

type ResourceStateChange struct {
	Version int64 `json:"version"`
	Listed  *bool `json:"listed,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
}

type ResourceUse struct {
	ConnectionSource string `json:"connection_source,omitempty"`
	AgentID          string `json:"agent_id"`
	AgentName        string `json:"agent_name"`
	PackageID        string `json:"package_id"`
	PackageName      string `json:"package_name"`
	VersionID        string `json:"version_id"`
	Enabled          bool   `json:"enabled"`
	AccountReady     bool   `json:"account_ready"`
}

type ResourceCatalog interface {
	Official(context.Context, string, string, string, string, int, int, string) (any, error)
	Publish(context.Context, string, string, string, string, ResourcePublication) (any, error)
	Change(context.Context, string, string, string, ResourceStateChange) (any, error)
	Archive(context.Context, string, string, string, string, int64) error
	Uses(context.Context, string, string, string, string, int, int) (any, error)
}

type AssignmentTarget struct {
	AgentID   string `json:"agent_id"`
	PackageID string `json:"package_id"`
	Version   int64  `json:"package_version"`
}
type ResourceAssignment struct {
	VersionID string             `json:"version_id"`
	Targets   []AssignmentTarget `json:"targets"`
}
type AssignmentResult struct {
	AgentID   string `json:"agent_id"`
	PackageID string `json:"package_id"`
	Status    string `json:"status"`
}

func (s *PackageService) Assign(ctx context.Context, w, a, kind, id string, r ResourceAssignment) ([]AssignmentResult, error) {
	if !agentUUID(r.VersionID) || len(r.Targets) == 0 || len(r.Targets) > 100 {
		return nil, Fail("invalid_assignment", 400, "请选择固定版本和 1 至 100 个目标能力包")
	}
	seen := map[string]bool{}
	for _, x := range r.Targets {
		if !agentUUID(x.AgentID) || !agentUUID(x.PackageID) || x.Version < 1 || seen[x.PackageID] {
			return nil, Fail("invalid_assignment", 400, "目标能力包或版本无效")
		}
		seen[x.PackageID] = true
	}
	results := make([]AssignmentResult, 0, len(r.Targets))
	for _, x := range r.Targets {
		status, err := s.assignOne(ctx, w, a, kind, id, r.VersionID, x)
		if err != nil {
			var detail *Error
			if errors.Is(err, ErrWorkspaceMissing) {
				status = "not_found"
			} else if errors.As(err, &detail) && detail.Status != 403 {
				status = detail.Code
			} else {
				return results, err
			}
		}
		results = append(results, AssignmentResult{x.AgentID, x.PackageID, status})
	}
	return results, nil
}
func (s *PackageService) assignOne(ctx context.Context, w, a, kind, id, pin string, target AssignmentTarget) (string, error) {
	status := "assigned"
	err := s.Store.WithWorkspace(ctx, w, func(t PackageTx) error {
		if err := packageAuthorize(ctx, t, a, true); err != nil {
			return err
		}
		pack, err := t.Get(ctx, target.AgentID, target.PackageID)
		if err != nil {
			return err
		}
		previous := pack
		if kind == "skill" {
			for _, p := range pack.Skills {
				if p.SkillID == id {
					status = "already_assigned"
					return nil
				}
			}
			pack.Skills = append(pack.Skills, PackageSkill{SkillID: id, VersionID: pin})
		} else {
			for _, p := range pack.Plugins {
				if p.PluginID == id {
					status = "already_assigned"
					return nil
				}
			}
			binding := PackagePlugin{PluginID: id, VersionID: pin}
			pack.Plugins = append(pack.Plugins, binding)
		}
		if pack.Version != target.Version {
			return Fail("package_version_conflict", 409, "能力包已更新")
		}
		if err := validatePackage(&pack); err != nil {
			return err
		}
		if err := validatePackageReferences(ctx, t, pack, previous); err != nil {
			return err
		}
		pack.Version++
		return t.Save(ctx, pack, false)
	})
	return status, err
}
func (r ResourcePublication) Validate() error {
	if !agentUUID(r.SourceWorkspaceID) || !agentUUID(r.VersionID) || !agentUUID(r.ResourceID) {
		return Fail("invalid_source", 400, "请选择来源资源和固定版本")
	}
	return nil
}
