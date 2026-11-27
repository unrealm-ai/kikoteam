package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// AgentCardResources is the directory summary shared by all active workspace members.
type AgentCardResources struct {
	Channels     []string `json:"channels"`
	PackageCount int      `json:"package_count"`
	TriggerCount int      `json:"trigger_count"`
}
type Agent struct {
	CardResources   *AgentCardResources `json:"card_resources,omitempty"`
	WebAccess       WebAccess           `json:"web_access"`
	Payer           *AgentPayer         `json:"payer,omitempty"`
	TemplateOrigin  *TemplateOrigin     `json:"template_origin,omitempty"`
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Role            string              `json:"role"`
	Avatar          string              `json:"avatar"`
	Instruction     string              `json:"instruction"`
	ModelProfileID  string              `json:"model_profile_id"`
	PlatformModelID string              `json:"platform_model_id,omitempty"`
	ModelName       string              `json:"model_name"`
	ModelAvailable  bool                `json:"model_available"`
	Harness         string              `json:"harness"`
	Enabled         bool                `json:"enabled"`
	Version         int64               `json:"version"`
	ConfigVersion   int64               `json:"config_version"`
	AdmissionEpoch  int64               `json:"admission_epoch"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}
type AgentPayer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type AgentCreate struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	Avatar          string `json:"avatar"`
	Instruction     string `json:"instruction"`
	ModelProfileID  string `json:"model_profile_id"`
	PlatformModelID string `json:"platform_model_id,omitempty"`
}
type AgentPatch struct {
	ConfirmPlatformBilling bool       `json:"confirm_platform_billing,omitempty"`
	WebAccess              *WebAccess `json:"web_access,omitempty"`
	Version                int64      `json:"version"`
	Name                   *string    `json:"name,omitempty"`
	Role                   *string    `json:"role,omitempty"`
	Avatar                 *string    `json:"avatar,omitempty"`
	Instruction            *string    `json:"instruction,omitempty"`
	ModelProfileID         *string    `json:"model_profile_id,omitempty"`
	PlatformModelID        *string    `json:"platform_model_id,omitempty"`
	Enabled                *bool      `json:"enabled,omitempty"`
}
type AgentListQuery struct {
	Search         string
	Enabled        *bool
	Page, PageSize int
}
type AgentStatusCounts struct {
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
}
type AgentPage struct {
	Counts   AgentStatusCounts `json:"status_counts"`
	Items    []Agent           `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}
type AgentTx interface {
	Member(context.Context, string) (Member, error)
	Model(context.Context, string) (ModelProfile, error)
	OfficialModel(context.Context, string) (ModelProfile, error)
	EnsureModelKey(context.Context, string) error
	Get(context.Context, string) (Agent, error)
	List(context.Context, AgentListQuery) (AgentPage, error)
	Create(context.Context, Agent, string) error
	Update(context.Context, Agent, bool) error
}
type AgentStore interface {
	WithWorkspace(context.Context, string, func(AgentTx) error) error
}
type AgentService struct{ Store AgentStore }

func agentAuthorize(ctx context.Context, t AgentTx, actor string, write bool) (Member, error) {
	m, e := t.Member(ctx, actor)
	if errors.Is(e, ErrWorkspaceMissing) || e == nil && (m.Status != "active" || write && m.Role != "owner" && m.Role != "admin") {
		return m, Fail("workspace_forbidden", 403, "没有员工管理权限")
	}
	return m, e
}
func (s *AgentService) List(ctx context.Context, w, a string, q AgentListQuery) (AgentPage, error) {
	var out AgentPage
	if e := pageValid(q.Page, q.PageSize); e != nil {
		return out, e
	}
	q.Search = strings.TrimSpace(q.Search)
	if !utf8.ValidString(q.Search) || strings.ContainsRune(q.Search, '\x00') || utf8.RuneCountInString(q.Search) > 2000 {
		return out, Fail("invalid_query", 400, "员工搜索词无效")
	}
	e := s.Store.WithWorkspace(ctx, w, func(t AgentTx) error {
		m, err := agentAuthorize(ctx, t, a, false)
		if err != nil {
			return err
		}
		out, err = t.List(ctx, q)
		if m.Role != "owner" && m.Role != "admin" {
			for i := range out.Items {
				out.Items[i].TemplateOrigin = nil
				out.Items[i].Payer = nil
			}
		}
		return err
	})
	return out, e
}
func (s *AgentService) Get(ctx context.Context, w, a, id string) (Agent, error) {
	var out Agent
	e := s.Store.WithWorkspace(ctx, w, func(t AgentTx) error {
		m, e := agentAuthorize(ctx, t, a, false)
		if e != nil {
			return e
		}
		out, e = t.Get(ctx, id)
		if m.Role != "owner" && m.Role != "admin" {
			out.TemplateOrigin = nil
			out.Payer = nil
		}
		return e
	})
	return out, e
}
func validateAgent(v *Agent) error {
	v.Name = strings.TrimSpace(v.Name)
	v.Role = strings.TrimSpace(v.Role)
	v.Avatar = strings.TrimSpace(v.Avatar)
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 100 || utf8.RuneCountInString(v.Role) > 2000 || utf8.RuneCountInString(v.Avatar) > 100 || utf8.RuneCountInString(v.Instruction) > 8000 || strings.ContainsRune(v.Name+v.Role+v.Avatar+v.Instruction, '\x00') {
		return Fail("invalid_agent", 400, "员工名称、简介、头像或岗位行为规范无效")
	}
	if !ValidModelSelection(v.ModelProfileID, v.PlatformModelID) {
		return Fail("invalid_model_profile", 400, "请选择有效模型")
	}
	return nil
}
func ValidModelSelection(workspace, official string) bool {
	return workspace == "" && agentUUID(official) || official == "" && agentUUID(workspace)
}
func agentUUID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
func ValidateAgentModel(ctx context.Context, t AgentTx, v *Agent) error {
	var m ModelProfile
	var e error
	if v.PlatformModelID != "" {
		m, e = t.OfficialModel(ctx, v.PlatformModelID)
	} else {
		m, e = t.Model(ctx, v.ModelProfileID)
	}
	if errors.Is(e, ErrWorkspaceMissing) {
		return Fail("model_unavailable", 409, "模型不存在或不属于当前工作区")
	}
	if e != nil {
		return e
	}
	if !m.Enabled || !m.Validated {
		return Fail("model_unavailable", 409, "模型未通过验证或已停用")
	}
	v.ModelName = m.Name
	return nil
}
func (s *AgentService) Create(ctx context.Context, w, a string, r AgentCreate) (Agent, error) {
	v := Agent{ID: NewID(), Name: r.Name, Role: r.Role, Avatar: r.Avatar, Instruction: r.Instruction, ModelProfileID: r.ModelProfileID, PlatformModelID: r.PlatformModelID, Harness: "pi", Enabled: true, Version: 1, ConfigVersion: 1, AdmissionEpoch: 1}
	if e := validateAgent(&v); e != nil {
		return Agent{}, e
	}
	e := s.Store.WithWorkspace(ctx, w, func(t AgentTx) error {
		if _, e := agentAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		if e := ValidateAgentModel(ctx, t, &v); e != nil {
			return e
		}
		if e := t.Create(ctx, v, a); e != nil {
			return e
		}
		if e := t.EnsureModelKey(ctx, v.ID); e != nil {
			return e
		}
		var e error
		v, e = t.Get(ctx, v.ID)
		return e
	})
	return v, e
}
func (s *AgentService) Update(ctx context.Context, w, a, id string, r AgentPatch) (Agent, error) {
	var v Agent
	if r.Version < 1 {
		return v, Fail("version_required", 400, "请提供当前员工版本")
	}
	selecting, e := validateAgentPatch(r)
	if e != nil {
		return v, e
	}
	e = s.Store.WithWorkspace(ctx, w, func(t AgentTx) error {
		if _, e := agentAuthorize(ctx, t, a, true); e != nil {
			return e
		}
		var e error
		v, e = t.Get(ctx, id)
		if e != nil {
			return e
		}
		if v.Version != r.Version {
			return Fail("agent_version_conflict", 409, "员工已更新，请刷新后重试")
		}
		old := v
		if r.WebAccess != nil {
			if e := validateWebAccess(ctx, t, *r.WebAccess); e != nil {
				return e
			}
			v.WebAccess = *r.WebAccess
		} else if r.Enabled != nil {
			if v.Enabled == *r.Enabled {
				return nil
			}
			v.Enabled = *r.Enabled
			v.AdmissionEpoch++
		} else {
			applyAgentPatch(&v, r)
			if e := validateAgent(&v); e != nil {
				return e
			}
		}
		changed := v.Name != old.Name || v.Role != old.Role || v.Avatar != old.Avatar || v.Instruction != old.Instruction || v.ModelProfileID != old.ModelProfileID || v.PlatformModelID != old.PlatformModelID
		if v.ModelProfileID != old.ModelProfileID || v.PlatformModelID != old.PlatformModelID || r.Enabled != nil && *r.Enabled {
			if e := ValidateAgentModel(ctx, t, &v); e != nil {
				return e
			}
		}
		if selecting && v.Payer == nil && v.PlatformModelID != "" {
			e = t.EnsureModelKey(ctx, v.ID)
			if e != nil {
				return e
			}
		}
		if changed {
			v.ConfigVersion++
		}
		v.Version++
		if e := t.Update(ctx, v, changed); e != nil {
			return e
		}
		v, e = t.Get(ctx, id)
		return e
	})
	return v, e
}
func validateAgentPatch(r AgentPatch) (bool, error) {
	selecting := r.ModelProfileID != nil || r.PlatformModelID != nil
	if r.ModelProfileID != nil && r.PlatformModelID != nil || r.ModelProfileID != nil && *r.ModelProfileID == "" || r.PlatformModelID != nil && *r.PlatformModelID == "" {
		return false, Fail("invalid_model_profile", 400, "请选择一个有效模型")
	}
	config := r.Name != nil || r.Role != nil || r.Avatar != nil || r.Instruction != nil || selecting
	if r.WebAccess != nil && (config || r.Enabled != nil) || r.WebAccess == nil && (r.Enabled != nil && config || r.Enabled == nil && !config) {
		return false, Fail("invalid_agent_patch", 400, "请分别保存启停状态与员工配置")
	}
	return selecting, nil
}
func applyAgentPatch(v *Agent, r AgentPatch) {
	if r.Name != nil {
		v.Name = *r.Name
	}
	if r.Role != nil {
		v.Role = *r.Role
	}
	if r.Avatar != nil {
		v.Avatar = *r.Avatar
	}
	if r.Instruction != nil {
		v.Instruction = *r.Instruction
	}
	if r.ModelProfileID != nil {
		v.ModelProfileID = *r.ModelProfileID
		v.PlatformModelID = ""
	}
	if r.PlatformModelID != nil {
		v.PlatformModelID = *r.PlatformModelID
		v.ModelProfileID = ""
	}
}
