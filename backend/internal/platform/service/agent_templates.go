package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

type TemplateParameter struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
}
type TemplatePin struct {
	ResourceKey string `json:"resource_key"`
}
type TemplatePluginPin struct {
	ResourceKey string `json:"resource_key"`
}
type TemplatePackage struct {
	Key         string              `json:"package_key"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Onboarding  string              `json:"onboarding_requirement"`
	Skills      []TemplatePin       `json:"skills"`
	Plugins     []TemplatePluginPin `json:"plugins"`
}
type TemplateResource struct {
	Key         string `json:"resource_key"`
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ID          string `json:"id,omitempty"`
	VersionID   string `json:"version_id,omitempty"`
}
type TemplateExample struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Text        string   `json:"text"`
	Output      string   `json:"output"`
	PackageKeys []string `json:"package_keys"`
}
type TemplateContent struct {
	SchemaVersion     int                 `json:"schema_version"`
	Name              string              `json:"name"`
	Avatar            string              `json:"avatar"`
	Role              string              `json:"role"`
	Category          string              `json:"category"`
	Audience          string              `json:"audience"`
	Deliverables      string              `json:"deliverables"`
	ModelRequirements string              `json:"model_requirements"`
	Instruction       string              `json:"instruction_template"`
	Parameters        []TemplateParameter `json:"parameters"`
	Packages          []TemplatePackage   `json:"packages"`
	Resources         []TemplateResource  `json:"resource_refs"`
	Examples          []TemplateExample   `json:"examples"`
}
type AgentTemplate struct {
	BuiltinKey            string          `json:"builtin_key,omitempty"`
	ID                    string          `json:"id"`
	Scope                 string          `json:"scope"`
	OwnerWorkspaceID      string          `json:"owner_workspace_id,omitempty"`
	Version               int64           `json:"version"`
	LatestVersionID       string          `json:"latest_version_id,omitempty"`
	PublishedVersionID    string          `json:"published_version_id,omitempty"`
	IsListed              bool            `json:"is_listed"`
	RecommendedRank       int             `json:"recommended_rank"`
	HasUnpublishedChanges bool            `json:"has_unpublished_changes"`
	Status                string          `json:"status"`
	Content               TemplateContent `json:"content"`
	Sequence              int64           `json:"sequence"`
	TemplateVersionID     string          `json:"template_version_id"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	PublishedAt           *time.Time      `json:"published_at"`
}
type TemplateVersion struct {
	ID          string          `json:"id"`
	TemplateID  string          `json:"template_id"`
	Sequence    int64           `json:"sequence"`
	Content     TemplateContent `json:"content"`
	ChangeNote  string          `json:"change_note"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
}
type TemplateQuery struct {
	Page, PageSize                               int
	Q, Category, SortBy, SortOrder, View, Status string
}
type TemplatePage struct {
	Items    []AgentTemplate `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}
type TemplateVersionPage struct {
	Items    []TemplateVersion `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}
type TemplatePatch struct {
	Version         int64            `json:"version"`
	Content         *TemplateContent `json:"content,omitempty"`
	ChangeNote      string           `json:"change_note,omitempty"`
	RecommendedRank *int             `json:"recommended_rank,omitempty"`
	IsListed        *bool            `json:"is_listed,omitempty"`
}
type TemplatePublish struct {
	Version           int64  `json:"version"`
	TemplateVersionID string `json:"template_version_id"`
}
type TemplateCreation struct {
	RequiredPackagesOnly bool           `json:"required_packages_only,omitempty"`
	Starter              bool           `json:"-"`
	TemplateID           string         `json:"template_id"`
	TemplateVersionID    string         `json:"template_version_id"`
	Name                 string         `json:"name"`
	ModelProfileID       string         `json:"model_profile_id"`
	PlatformModelID      string         `json:"platform_model_id,omitempty"`
	Parameters           map[string]any `json:"parameters"`
	Role                 *string        `json:"role,omitempty"`
	Avatar               *string        `json:"avatar,omitempty"`
	InstructionOverride  *string        `json:"instruction_override,omitempty"`
}
type TemplateAsset struct {
	Resource                                          TemplateResource
	Skill                                             SkillVersion
	Plugin                                            Plugin
	ImportID, TargetID, TargetVersionID, TargetPrefix string
	Imported                                          bool
}
type TemplateTx interface {
	Get(context.Context, string, bool) (AgentTemplate, error)
	List(context.Context, TemplateQuery) (TemplatePage, error)
	Versions(context.Context, string, TemplateQuery) (TemplateVersionPage, error)
	Version(context.Context, string, string, bool) (TemplateVersion, error)
	Save(context.Context, *AgentTemplate, *TemplateContent, string) error
	Publish(context.Context, *AgentTemplate) error
	Delete(context.Context, AgentTemplate) error
	CheckResources(context.Context, TemplateContent, bool) error
}
type TemplateRepository interface {
	With(context.Context, string, string, bool, func(TemplateTx) error) error
	LoadCreation(context.Context, string, string, TemplateCreation) (TemplateVersion, []TemplateAsset, error)
	Stage(context.Context, string, string, []TemplateAsset) error
	Ready(context.Context, string, string, []TemplateAsset) error
	CommitCreation(context.Context, string, string, TemplateCreation, TemplateVersion, []TemplateAsset, string, map[string]any) (Agent, error)
	FromAgent(context.Context, string, string, string, int64, int64, string) (TemplateContent, error)
	Setup(context.Context, string, string, string) (map[string]any, error)
}
type TemplateService struct {
	Store  TemplateRepository
	Assets SkillAssets
}

var templateKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var templatePlaceholder = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

func validateTemplate(c TemplateContent, publish bool) error {
	b, err := json.Marshal(c)
	if err != nil {
		return Fail("invalid_template_content", 400, "模板参数默认值无效")
	}
	if c.SchemaVersion != 1 || len(b) > 256<<10 || len(c.Packages) > 20 || len(c.Resources) > 50 || len(c.Parameters) > 20 || len(c.Examples) > 10 || len([]rune(c.Name)) > 100 || strings.ContainsRune(string(b), 0) {
		return Fail("invalid_template_content", 400, "模板格式或大小无效")
	}
	if publish && (strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Instruction) == "" || len(c.Examples) == 0 || !slices.Contains([]string{"engineering", "marketing", "sales", "analytics", "office"}, c.Category)) {
		return Fail("invalid_template_content", 400, "请补齐岗位、分类、指令和示例")
	}
	params := map[string]bool{}
	for _, p := range c.Parameters {
		if !templateKey.MatchString(p.Key) || params[p.Key] || !slices.Contains([]string{"short_text", "long_text", "single_select", "multi_select"}, p.Type) || p.MaxLength < 0 || p.MaxLength > 8000 {
			return Fail("invalid_template_content", 400, "岗位参数定义无效")
		}
		params[p.Key] = true
		if p.Default != nil {
			check := p
			check.Required = false
			if _, _, err := RenderTemplate(TemplateContent{Parameters: []TemplateParameter{check}}, nil); err != nil {
				return err
			}
		}
	}
	refs, err := validateTemplateResources(c.Resources)
	if err != nil {
		return err
	}
	keys, err := validateTemplatePackages(c.Packages, refs)
	if err != nil {
		return err
	}
	examples := map[string]bool{}
	for _, ex := range c.Examples {
		if !templateKey.MatchString(ex.Key) || examples[ex.Key] || publish && (strings.TrimSpace(ex.Text) == "" || strings.TrimSpace(ex.Output) == "") {
			return Fail("invalid_template_content", 400, "示例需有唯一标识、任务和交付样例")
		}
		examples[ex.Key] = true
		for _, k := range ex.PackageKeys {
			if !keys[k] {
				return Fail("invalid_template_content", 400, "示例引用了不存在的能力")
			}
		}
	}
	for _, m := range templatePlaceholder.FindAllStringSubmatch(c.Instruction, -1) {
		if !params[m[1]] {
			return Fail("invalid_template_content", 400, "指令占位符未声明")
		}
	}
	return nil
}
func validateTemplatePackages(packages []TemplatePackage, refs map[string]TemplateResource) (map[string]bool, error) {
	keys := map[string]bool{}
	for _, p := range packages {
		if !templateKey.MatchString(p.Key) || keys[p.Key] || strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 || !slices.Contains([]string{"required", "deferred"}, p.Onboarding) {
			return nil, Fail("invalid_template_content", 400, "能力包定义无效")
		}
		keys[p.Key] = true
		for _, s := range p.Skills {
			if refs[s.ResourceKey].Kind != "skill" {
				return nil, Fail("invalid_template_content", 400, "技能引用不存在")
			}
		}
		for _, s := range p.Plugins {
			if refs[s.ResourceKey].Kind != "plugin" {
				return nil, Fail("invalid_template_content", 400, "插件引用无效")
			}
		}
	}
	return keys, nil
}
func RenderTemplate(c TemplateContent, input map[string]any) (string, map[string]any, error) {
	values := map[string]any{}
	text := map[string]string{}
	for k := range input {
		found := false
		for _, p := range c.Parameters {
			if k == p.Key {
				found = true
			}
		}
		if !found {
			return "", nil, Fail("invalid_template_parameters", 400, "未知岗位参数")
		}
	}
	for _, p := range c.Parameters {
		v, ok := input[p.Key]
		if !ok {
			v = p.Default
		}
		if v == nil {
			v = ""
		}
		str := ""
		if p.Type == "multi_select" {
			items, ok := v.([]any)
			if !ok {
				if a, yes := v.([]string); yes {
					for _, x := range a {
						items = append(items, x)
					}
				} else if v != "" {
					return "", nil, Fail("invalid_template_parameters", 400, "多选参数格式无效")
				}
			}
			parts := []string{}
			for _, x := range items {
				s, ok := x.(string)
				if !ok || !slices.Contains(p.Options, s) {
					return "", nil, Fail("invalid_template_parameters", 400, "多选值无效")
				}
				parts = append(parts, s)
			}
			v = parts
			str = strings.Join(parts, "、")
		} else {
			s, ok := v.(string)
			if !ok {
				return "", nil, Fail("invalid_template_parameters", 400, "岗位参数需为文本")
			}
			str = s
			if p.Type == "single_select" && s != "" && !slices.Contains(p.Options, s) {
				return "", nil, Fail("invalid_template_parameters", 400, "选项无效")
			}
		}
		limit := p.MaxLength
		if limit == 0 {
			limit = 200
			if p.Type == "long_text" {
				limit = 8000
			}
		}
		if p.Required && strings.TrimSpace(str) == "" || len([]rune(str)) > limit || strings.ContainsRune(str, 0) {
			return "", nil, Fail("invalid_template_parameters", 400, fmt.Sprintf("请检查参数 %s", p.Key))
		}
		values[p.Key] = v
		text[p.Key] = str
	}
	rendered := templatePlaceholder.ReplaceAllStringFunc(c.Instruction, func(m string) string { return text[templatePlaceholder.FindStringSubmatch(m)[1]] })
	if len([]rune(rendered)) > 8000 {
		return "", nil, Fail("invalid_template_parameters", 400, "替换后指令过长")
	}
	return rendered, values, nil
}
func (s *TemplateService) List(ctx context.Context, w, a string, q TemplateQuery) (out TemplatePage, e error) {
	if e = pageValid(q.Page, q.PageSize); e != nil {
		return
	}
	if len([]rune(q.Q)) > 100 || !slices.Contains([]string{"", "recommended", "published_at"}, q.SortBy) || !slices.Contains([]string{"", "asc", "desc"}, q.SortOrder) || !slices.Contains([]string{"", "manage"}, q.View) || !slices.Contains([]string{"", "draft", "published", "unlisted"}, q.Status) {
		e = Fail("invalid_query", 400, "筛选无效")
		return
	}
	e = s.Store.With(ctx, w, a, q.View == "manage", func(t TemplateTx) error { out, e = t.List(ctx, q); return e })
	return
}
func (s *TemplateService) Get(ctx context.Context, w, a, id string, manage bool) (out AgentTemplate, e error) {
	e = s.Store.With(ctx, w, a, manage, func(t TemplateTx) error { out, e = t.Get(ctx, id, manage); return e })
	return
}
func (s *TemplateService) Create(ctx context.Context, w, a string, c TemplateContent) (out AgentTemplate, e error) {
	c = normalizeTemplate(c)
	if e = validateTemplate(c, false); e != nil {
		return
	}
	out = AgentTemplate{ID: NewID(), Version: 1, LatestVersionID: NewID(), Scope: "workspace", OwnerWorkspaceID: w}
	if w == "" {
		out.Scope = "official"
	}
	e = s.Store.With(ctx, w, a, true, func(t TemplateTx) error {
		if e = t.CheckResources(ctx, c, false); e != nil {
			return e
		}
		if e = t.Save(ctx, &out, &c, ""); e != nil {
			return e
		}
		out, e = t.Get(ctx, out.ID, true)
		return e
	})
	return
}
func (s *TemplateService) Update(ctx context.Context, w, a, id string, r TemplatePatch) (out AgentTemplate, e error) {
	count := 0
	if r.Content != nil {
		count++
	}
	if r.RecommendedRank != nil {
		count++
	}
	if r.IsListed != nil {
		count++
	}
	if count != 1 || r.Version < 1 || r.Content == nil && r.ChangeNote != "" {
		return out, Fail("invalid_request", 400, "请分别保存内容、排序或上架状态")
	}
	if r.Content != nil {
		normalized := normalizeTemplate(*r.Content)
		r.Content = &normalized
		if e = validateTemplate(*r.Content, false); e != nil {
			return
		}
	}
	e = s.Store.With(ctx, w, a, true, func(t TemplateTx) error {
		out, e = t.Get(ctx, id, true)
		if e != nil {
			return e
		}
		if out.BuiltinKey != "" {
			return Fail("builtin_template_managed", 409, "该模板随 KIKO 版本维护，可复制到工作区后编辑")
		}
		if out.Version != r.Version {
			return Fail("template_revision_conflict", 409, "模板已更新，请刷新核对")
		}
		if r.Content != nil {
			if e = t.CheckResources(ctx, *r.Content, false); e != nil {
				return e
			}
		}
		if r.RecommendedRank != nil {
			out.RecommendedRank = *r.RecommendedRank
		}
		if r.IsListed != nil {
			if *r.IsListed {
				v, e := t.Version(ctx, id, out.PublishedVersionID, true)
				if e != nil {
					return Fail("template_unavailable", 409, "没有已发布版本")
				}
				if e = t.CheckResources(ctx, v.Content, true); e != nil {
					return e
				}
			}
			out.IsListed = *r.IsListed
		}
		if e = t.Save(ctx, &out, r.Content, r.ChangeNote); e != nil {
			return e
		}
		out, e = t.Get(ctx, id, true)
		return e
	})
	return
}
func (s *TemplateService) Publish(ctx context.Context, w, a, id string, r TemplatePublish) (out AgentTemplate, e error) {
	if r.Version < 1 || !agentUUID(r.TemplateVersionID) {
		return out, Fail("invalid_request", 400, "请提供版本前提与目标版本")
	}
	e = s.Store.With(ctx, w, a, true, func(t TemplateTx) error {
		out, e = t.Get(ctx, id, true)
		if e != nil {
			return e
		}
		if out.BuiltinKey != "" {
			return Fail("builtin_template_managed", 409, "该模板随 KIKO 版本维护，可复制到工作区后编辑")
		}
		if r.Version != out.Version || r.TemplateVersionID != out.LatestVersionID {
			return Fail("template_revision_conflict", 409, "请核对最新保存版本")
		}
		if out.PublishedVersionID == out.LatestVersionID && out.IsListed {
			return nil
		}
		if e = validateTemplate(out.Content, true); e != nil {
			return e
		}
		if e = t.CheckResources(ctx, out.Content, true); e != nil {
			return e
		}
		if e = t.Publish(ctx, &out); e != nil {
			return e
		}
		out, e = t.Get(ctx, id, true)
		return e
	})
	return
}
func (s *TemplateService) Delete(ctx context.Context, w, a, id string, version int64) error {
	if version < 1 {
		return Fail("version_required", 400, "需要当前模板版本")
	}
	return s.Store.With(ctx, w, a, true, func(t TemplateTx) error {
		v, e := t.Get(ctx, id, true)
		if e != nil {
			return e
		}
		if v.BuiltinKey != "" {
			return Fail("builtin_template_managed", 409, "该模板随 KIKO 版本维护，可复制到工作区后编辑")
		}
		if v.Version != version {
			return Fail("template_revision_conflict", 409, "模板版本已更新")
		}
		if v.PublishedVersionID != "" {
			return Fail("template_unavailable", 409, "已发布模板请下架")
		}
		return t.Delete(ctx, v)
	})
}
func (s *TemplateService) Versions(ctx context.Context, w, a, id string, q TemplateQuery) (out TemplateVersionPage, e error) {
	if e = pageValid(q.Page, q.PageSize); e != nil {
		return
	}
	e = s.Store.With(ctx, w, a, q.View == "manage", func(t TemplateTx) error {
		if _, e = t.Get(ctx, id, q.View == "manage"); e != nil {
			return e
		}
		out, e = t.Versions(ctx, id, q)
		return e
	})
	return
}
func (s *TemplateService) Version(ctx context.Context, w, a, id, version string, manage bool) (out TemplateVersion, e error) {
	e = s.Store.With(ctx, w, a, manage, func(t TemplateTx) error { out, e = t.Version(ctx, id, version, manage); return e })
	return
}
func (s *TemplateService) CreateFromTemplate(ctx context.Context, w, a string, r TemplateCreation) (Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	if !agentUUID(r.TemplateID) || !agentUUID(r.TemplateVersionID) {
		return Agent{}, Fail("invalid_request", 400, "模板版本无效")
	}
	if !ValidModelSelection(r.ModelProfileID, r.PlatformModelID) {
		return Agent{}, Fail("invalid_model_profile", 400, "请选择有效模型")
	}
	v, assets, e := s.Store.LoadCreation(ctx, w, a, r)
	if e != nil {
		return Agent{}, e
	}
	instruction, parameters, e := RenderTemplate(v.Content, r.Parameters)
	if e != nil {
		return Agent{}, e
	}
	if r.InstructionOverride != nil {
		instruction = *r.InstructionOverride
	}
	draft := Agent{Name: r.Name, ModelProfileID: r.ModelProfileID, PlatformModelID: r.PlatformModelID, Instruction: instruction}
	if e = validateAgent(&draft); e != nil {
		return Agent{}, e
	}
	if e = s.Store.Stage(ctx, w, a, assets); e != nil {
		return Agent{}, e
	}
	for _, x := range assets {
		if x.Resource.Kind == "skill" && !x.Imported && x.ImportID != "" {
			if s.Assets == nil || s.Assets.Identity() != x.Skill.StorageIdentity {
				return Agent{}, Fail("template_storage_unavailable", 503, "技能存储不可用")
			}
			if e = s.Assets.Copy(ctx, x.Skill.KeyPrefix, x.TargetPrefix, x.Skill.Manifest); e != nil {
				return Agent{}, Fail("template_storage_unavailable", 503, "技能复制失败，员工尚未创建")
			}
		}
	}
	if e = s.Store.Ready(ctx, w, a, assets); e != nil {
		return Agent{}, e
	}
	return s.Store.CommitCreation(ctx, w, a, r, v, assets, instruction, parameters)
}
func (s *TemplateService) FromAgent(ctx context.Context, w, a, id string, av, cv int64, name string) (AgentTemplate, error) {
	c, e := s.Store.FromAgent(ctx, w, a, id, av, cv, name)
	if e != nil {
		return AgentTemplate{}, e
	}
	return s.Create(ctx, w, a, c)
}
func (s *TemplateService) Setup(ctx context.Context, w, a, id string) (map[string]any, error) {
	return s.Store.Setup(ctx, w, a, id)
}

func validateTemplateResources(resources []TemplateResource) (map[string]TemplateResource, error) {
	refs := map[string]TemplateResource{}
	pins := map[string]string{}
	source := ""
	for _, r := range resources {
		if !templateKey.MatchString(r.Key) || refs[r.Key].Key != "" || (r.WorkspaceID != "" && !agentUUID(r.WorkspaceID)) || !agentUUID(r.ID) || !agentUUID(r.VersionID) || (r.Kind != "skill" && r.Kind != "plugin") {
			return nil, Fail("invalid_template_content", 400, "资源引用无效")
		}
		if r.WorkspaceID != "" && source != "" && source != r.WorkspaceID {
			return nil, Fail("invalid_template_content", 400, "模板资源需属于同一来源工作区")
		}
		if r.WorkspaceID != "" {
			source = r.WorkspaceID
		}
		resourceID := r.Kind + ":" + r.ID
		if old := pins[resourceID]; old != "" && old != r.VersionID {
			return nil, Fail("invalid_template_content", 400, "同一资源不能混用多个版本")
		}
		pins[resourceID] = r.VersionID
		refs[r.Key] = r
	}
	return refs, nil
}

func normalizeTemplate(c TemplateContent) TemplateContent {
	if c.Parameters == nil {
		c.Parameters = []TemplateParameter{}
	}
	if c.Packages == nil {
		c.Packages = []TemplatePackage{}
	}
	if c.Resources == nil {
		c.Resources = []TemplateResource{}
	}
	if c.Examples == nil {
		c.Examples = []TemplateExample{}
	}
	for i := range c.Packages {
		if c.Packages[i].Skills == nil {
			c.Packages[i].Skills = []TemplatePin{}
		}
		if c.Packages[i].Plugins == nil {
			c.Packages[i].Plugins = []TemplatePluginPin{}
		}
	}
	for i := range c.Examples {
		if c.Examples[i].PackageKeys == nil {
			c.Examples[i].PackageKeys = []string{}
		}
	}
	return c
}
