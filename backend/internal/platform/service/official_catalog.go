package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Repository keys are permanent. Database IDs are independent of insertion order
// so a retry after writing immutable objects addresses the same files.
func CatalogID(kind, key string) string {
	h := sha256.Sum256([]byte("kikoteam:official:" + kind + ":" + key))
	h[6] = (h[6] & 0x0f) | 0x80
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

type CatalogSkill struct{ Key, Name, File, Source, License, SHA256 string }

func (s CatalogSkill) Ref() TemplateResource {
	return TemplateResource{Kind: "skill", Key: strings.ReplaceAll(s.Key, "-", "_"), ID: CatalogID("skill", s.Key), VersionID: CatalogID("skill-version", s.Key+":"+s.SHA256)}
}
func (s CatalogSkill) Prepare(ctx context.Context, assets SkillAssets) (SkillBundle, error) {
	data, err := catalogFiles.ReadFile("catalog/skills/" + s.File)
	if err != nil {
		return SkillBundle{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != s.SHA256 {
		return SkillBundle{}, fmt.Errorf("catalog skill %s: checksum mismatch", s.Key)
	}
	req := SkillImportRequest{Source: "markdown", Markdown: string(data)}
	if strings.HasSuffix(s.File, ".zip") {
		req = SkillImportRequest{Source: "zip", ZIPBase64: base64.StdEncoding.EncodeToString(data)}
	}
	b, err := assets.Prepare(ctx, req)
	if err == nil && b.TechnicalName != s.Key {
		return b, fmt.Errorf("catalog skill %s: unexpected technical name %s", s.Key, b.TechnicalName)
	}
	b.Source = SkillSource{Type: "official", RepositoryURL: s.Source}
	return b, err
}

type CatalogPlugin struct {
	Key                   string    `json:"key"`
	Name                  string    `json:"name"`
	Description           string    `json:"description"`
	Endpoint              string    `json:"endpoint"`
	Auth                  string    `json:"auth"`
	Issuer                string    `json:"issuer,omitempty"`
	Scopes                string    `json:"scopes,omitempty"`
	Header                string    `json:"header,omitempty"`
	Tools                 []string  `json:"tools"`
	PublicTools           []MCPTool `json:"public_tools,omitempty"`
	ExecutionKind         string    `json:"execution_kind,omitempty"`
	Distribution          string    `json:"distribution,omitempty"`
	HandlerKey            string    `json:"handler_key,omitempty"`
	DefaultScenes         []string  `json:"default_scenes,omitempty"`
	RequiresConfiguration bool      `json:"requires_configuration,omitempty"`
}

const githubReadonlyEndpoint = "https://api.githubcopilot.com/mcp/readonly"
const githubIssuer = "https://github.com/login/oauth"

// The official catalog's connection options apply to pinned versions too.
// Keep them outside the immutable tool definition and its content hash.
func (p Plugin) officialGitHub() bool {
	return p.ID == CatalogID("plugin", "github") && p.Scope == "official" && p.Endpoint == githubReadonlyEndpoint &&
		(p.AuthType == "bearer" || p.AuthType == "oauth")
}

func (p *Plugin) SetAuthMethods() {
	p.SupportedAuthMethods = []string{p.AuthType}
	if p.officialGitHub() {
		p.SupportedAuthMethods = []string{"bearer", "oauth"}
	}
	p.DefaultAuthMethod = p.SupportedAuthMethods[0]
}

// Resolve the effective wire credentials without mutating the stored definition.
func (p Plugin) forAuthMethod(method string) (Plugin, error) {
	if p.officialGitHub() && (method == "bearer" || method == "oauth") {
		p.AuthType = method
		p.OAuth = nil
		if method == "oauth" {
			p.OAuth = &PluginOAuth{Issuer: githubIssuer, Resource: githubReadonlyEndpoint, Scopes: "offline_access"}
		}
	} else if method == "" || p.AuthType != method {
		return Plugin{}, Fail("unsupported_auth_method", 400, "此插件版本不支持所选认证方式")
	}
	return p, nil
}

func (p CatalogPlugin) Ref() TemplateResource {
	data, err := json.Marshal(p)
	if err != nil {
		return TemplateResource{}
	}
	sum := sha256.Sum256(data)
	return TemplateResource{Kind: "plugin", Key: strings.ReplaceAll(p.Key, "-", "_"), ID: CatalogID("plugin", p.Key), VersionID: CatalogID("plugin-version", fmt.Sprintf("%s:%x", p.Key, sum))}
}
func (p CatalogPlugin) Definition() Plugin {
	r := p.Ref()
	d := Plugin{ID: r.ID, VersionID: r.VersionID, Scope: "official", Name: p.Name, Description: p.Description, Endpoint: p.Endpoint, AuthType: p.Auth, HeaderName: p.Header, Enabled: true, Version: 1}
	if p.Issuer != "" {
		d.OAuth = &PluginOAuth{Issuer: p.Issuer, Resource: p.Endpoint, Scopes: p.Scopes}
	}
	return d
}

func loadResources(c *TemplateCatalog) (map[string]TemplateResource, error) {
	if err := readCatalogJSON("skills/manifest", &c.Skills); err != nil {
		return nil, err
	}
	if err := readCatalogJSON("plugins/manifest", &c.Plugins); err != nil {
		return nil, err
	}
	refs := map[string]TemplateResource{}
	for _, s := range c.Skills {
		r := s.Ref()
		if refs[r.Key].ID != "" || !templateKey.MatchString(r.Key) || s.Name == "" || s.License == "" || s.Source == "" {
			return nil, fmt.Errorf("invalid catalog skill %q", s.Key)
		}
		refs[r.Key] = r
	}
	for _, p := range c.Plugins {
		r := p.Ref()
		d := p.Definition()
		if refs[r.Key].ID != "" || !templateKey.MatchString(r.Key) || len(p.Tools) == 0 {
			return nil, fmt.Errorf("invalid catalog plugin %q", p.Key)
		}
		if p.Auth == "none" && len(p.PublicTools) != len(p.Tools) {
			return nil, fmt.Errorf("public schema count for %s", p.Key)
		}
		baseline := map[string]string{}
		for _, tool := range p.PublicTools {
			var schema map[string]any
			if !slices.Contains(p.Tools, tool.Name) || baseline[tool.Name] != "" || json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
				return nil, fmt.Errorf("invalid public schema %s/%s", p.Key, tool.Name)
			}
			canonical, err := json.Marshal(schema)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256(canonical)
			if tool.SchemaDigest != hex.EncodeToString(hash[:]) {
				return nil, fmt.Errorf("public schema digest mismatch %s/%s", p.Key, tool.Name)
			}
			baseline[tool.Name] = tool.SchemaDigest
		}
		if p.ExecutionKind == "platform_managed" {
			if p.Distribution != "all_agents" || !validManagedDefinition(p) || p.Endpoint != "/mcp/platform-plugins/"+p.Key || len(p.DefaultScenes) == 0 {
				return nil, fmt.Errorf("invalid managed plugin %q", p.Key)
			}
		} else if err := ValidatePlugin(&d); err != nil {
			return nil, err
		}
		refs[r.Key] = r
	}
	return refs, nil
}

// Each shipped handler explicitly declares its supported contract before catalog activation.
func validManagedDefinition(p CatalogPlugin) bool {
	for _, scene := range p.DefaultScenes {
		if !slices.Contains([]string{"direct", "group", "trigger", "work"}, scene) {
			return false
		}
	}
	return p.Key == "image-generation" && p.HandlerKey == "image_generation" && p.Auth == "none" && p.RequiresConfiguration && slices.Equal(p.Tools, []string{"generate_image"})
}
func resolveCatalogTemplate(c *TemplateContent, refs map[string]TemplateResource) error {
	if len(c.Resources) != 0 {
		return fmt.Errorf("catalog templates must use portable resource keys")
	}
	seen := map[string]bool{}
	bind := func(key string) error {
		r, ok := refs[key]
		if !ok {
			return fmt.Errorf("unknown catalog resource %q", key)
		}
		if !seen[key] {
			seen[key] = true
			c.Resources = append(c.Resources, r)
		}
		return nil
	}
	for i := range c.Packages {
		p := &c.Packages[i]
		for _, s := range p.Skills {
			if err := bind(s.ResourceKey); err != nil {
				return err
			}
		}
		for j := range p.Plugins {
			pin := &p.Plugins[j]
			if err := bind(pin.ResourceKey); err != nil {
				return err
			}
		}
	}
	return nil
}
