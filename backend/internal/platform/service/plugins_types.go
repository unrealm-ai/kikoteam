package service

import (
	"context"
	"encoding/json"
	"time"
)

type Plugin struct {
	SupportedAuthMethods  []string      `json:"supported_auth_methods"`
	DefaultAuthMethod     string        `json:"default_auth_method"`
	DefinitionVersion     int64         `json:"definition_version"`
	Card                  *ResourceCard `json:"card,omitempty"`
	SharedConnectionState string        `json:"shared_connection_state,omitempty"`
	OAuth                 *PluginOAuth  `json:"oauth,omitempty"`
	ClientCredential      []byte        `json:"-"`
	Scope                 string        `json:"scope"`
	Listed                bool          `json:"listed"`
	AssignedAgentCount    int           `json:"assigned_agent_count"`
	ID                    string        `json:"id"`
	Name                  string        `json:"name"`
	Description           string        `json:"description"`
	Endpoint              string        `json:"endpoint"`
	AuthType              string        `json:"auth_type"`
	HeaderName            string        `json:"header_name"`
	Enabled               bool          `json:"enabled"`
	Version               int64         `json:"version"`
	VersionID             string        `json:"version_id"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
}
type PluginCreate struct {
	OAuth       *OAuthClientInput `json:"oauth,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Endpoint    string            `json:"endpoint"`
	AuthType    string            `json:"auth_type"`
	HeaderName  string            `json:"header_name"`
}
type PluginPatch struct {
	OAuth       *OAuthClientInput `json:"oauth,omitempty"`
	Version     int64             `json:"version"`
	Name        *string           `json:"name,omitempty"`
	Description *string           `json:"description,omitempty"`
	Endpoint    *string           `json:"endpoint,omitempty"`
	AuthType    *string           `json:"auth_type,omitempty"`
	HeaderName  *string           `json:"header_name,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
}
type PluginPage struct {
	Items    []Plugin `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}
type MCPTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	SchemaDigest string          `json:"schema_digest"`
}
type PluginAccount struct {
	AuthMethod         string     `json:"auth_method"`
	Source             string     `json:"source"`
	ExternalAccount    string     `json:"external_account,omitempty"`
	GrantedScopes      string     `json:"granted_scopes,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CredentialRevision int64      `json:"-"`
	AgentCount         int        `json:"agent_count,omitempty"`
	ID                 string     `json:"id"`
	PluginID           string     `json:"plugin_id"`
	PluginVersionID    string     `json:"plugin_version_id"`
	AgentID            string     `json:"agent_id,omitempty"`
	Name               string     `json:"name"`
	Version            int64      `json:"version"`
	Enabled            bool       `json:"enabled"`
	Validated          bool       `json:"validated"`
	Tools              []MCPTool  `json:"tools"`
	ValidatedAt        time.Time  `json:"validated_at"`
	Credential         []byte     `json:"-"`
}
type PluginAccountPut struct {
	AuthMethod      string            `json:"auth_method,omitempty"`
	TenantOAuth     *OAuthClientInput `json:"tenant_oauth,omitempty"`
	PluginVersionID string            `json:"plugin_version_id"`
	Name            string            `json:"name"`
	Secret          *string           `json:"secret,omitempty"`
	Version         int64             `json:"version"`
}
type MCPProbe interface {
	Probe(context.Context, Plugin, string) ([]MCPTool, error)
}
type PluginTx interface {
	Member(context.Context, string) (Member, error)
	AgentExists(context.Context, string) error
	Get(context.Context, string) (Plugin, error)
	GetVersion(context.Context, string, string) (Plugin, error)
	List(context.Context, int, int, ResourceListFilter) ([]Plugin, int, error)
	Versions(context.Context, string, int, int) ([]Plugin, int, error)
	Save(context.Context, Plugin, bool, bool) error
	Account(context.Context, string, string, string) (PluginAccount, error)
	ConnectionTx
	SaveAccount(context.Context, PluginAccount, bool) error
}
type PluginStore interface {
	WithConnection(context.Context, string, string, func(PluginTx) error) error
	WithWorkspace(context.Context, string, func(PluginTx) error) error
}
type PluginService struct {
	OAuthApps   *OAuthAppService
	Store       PluginStore
	Probe       MCPProbe
	Secret      []byte
	RedirectURI string
}
