package service

import (
	"context"
	"time"
)

type ChannelTrigger struct {
	Mention  bool     `json:"mention"`
	Keywords []string `json:"keywords"`
	Regexes  []string `json:"regexes"`
}
type ChannelHistory struct {
	Enabled bool `json:"enabled"`
	Limit   int  `json:"limit"`
}
type ChannelDepartmentRule struct {
	DepartmentID       string `json:"department_id"`
	IncludeDescendants bool   `json:"include_descendants"`
}
type ChannelAccess struct {
	AllowDepartmentRules []ChannelDepartmentRule `json:"allow_department_rules"`
	DenyDepartmentRules  []ChannelDepartmentRule `json:"deny_department_rules"`
	AllowDepartmentIDs   []string                `json:"allow_department_ids"`
	DenyDepartmentIDs    []string                `json:"deny_department_ids"`
	GroupMembersEnabled  bool                    `json:"group_members_enabled"`
	Mode                 string                  `json:"mode"`
	AllowIDs             []string                `json:"allow_ids"`
	DenyIDs              []string                `json:"deny_ids"`
	AllowChatIDs         []string                `json:"allow_chat_ids"`
	DenyChatIDs          []string                `json:"deny_chat_ids"`
}
type ChannelApplied struct {
	Generation       int64  `json:"connection_generation"`
	RulesVersion     int64  `json:"rules_version"`
	Revision         int64  `json:"config_revision"`
	Status           string `json:"status"`
	ErrorCode        string `json:"error_code"`
	ObservedTenantID string `json:"observed_tenant_id,omitempty"`
	ObservedBotID    string `json:"observed_bot_open_id,omitempty"`
}
type ChannelRebindImpact struct {
	AllowEntries      int      `json:"allow_entries"`
	DenyEntries       int      `json:"deny_entries"`
	PackageNames      []string `json:"package_names"`
	PackageDenyNames  []string `json:"package_deny_names"`
	SharingNames      []string `json:"sharing_names"`
	NotificationNames []string `json:"notification_names"`
}
type ChannelBinding struct {
	IdentityConfirmed          bool                    `json:"identity_confirmed"`
	IdentityVerifiedGeneration int64                   `json:"identity_verified_generation"`
	TenantNamespace            string                  `json:"tenant_namespace"`
	UserNamespace              string                  `json:"user_namespace"`
	IdentityEpoch              int64                   `json:"identity_epoch"`
	ID                         string                  `json:"id"`
	WorkspaceID                string                  `json:"workspace_id"`
	AgentID                    string                  `json:"agent_id"`
	Provider                   string                  `json:"provider"`
	Generation                 int64                   `json:"connection_generation"`
	RulesVersion               int64                   `json:"rules_version"`
	Revision                   int64                   `json:"config_revision"`
	Enabled                    bool                    `json:"enabled"`
	AppID                      string                  `json:"app_id"`
	TenantID                   string                  `json:"external_tenant_id"`
	BotID                      string                  `json:"bot_open_id"`
	HasSecret                  bool                    `json:"has_secret"`
	Ciphertext                 []byte                  `json:"-"`
	Trigger                    ChannelTrigger          `json:"trigger"`
	History                    ChannelHistory          `json:"history"`
	Access                     ChannelAccess           `json:"access"`
	Applied                    ChannelApplied          `json:"applied"`
	PermissionDiagnostics      *FeishuDiagnostics      `json:"permission_diagnostics,omitempty"`
	PermissionGroups           []FeishuPermissionGroup `json:"permission_groups,omitempty"`
	DirectoryStatus            *DirectoryStatus        `json:"directory_status,omitempty"`
	RebindImpact               *ChannelRebindImpact    `json:"rebind_impact,omitempty"`
}
type ChannelPut struct {
	Version   int64          `json:"version"`
	AppID     string         `json:"app_id"`
	AppSecret *string        `json:"app_secret,omitempty"`
	Enabled   bool           `json:"enabled"`
	Trigger   ChannelTrigger `json:"trigger"`
	History   ChannelHistory `json:"history"`
	Access    ChannelAccess  `json:"access"`
}
type ChannelCredentials struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}
type HubChannelBinding struct {
	ChannelBinding
	Credentials ChannelCredentials `json:"credentials"`
}
type HubChannelSnapshot struct {
	Revision int64               `json:"snapshot_revision"`
	Items    []HubChannelBinding `json:"items"`
}
type ChannelTx interface {
	Member(context.Context, string) (Member, error)
	AgentExists(context.Context, string) error
	Get(context.Context, string) (ChannelBinding, error)
	Save(context.Context, ChannelBinding, bool) error
}
type ChannelStore interface {
	WithWorkspace(context.Context, string, func(ChannelTx) error) error
	Snapshot(context.Context) (int64, []ChannelBinding, error)
	Applied(context.Context, string, ChannelApplied) error
	PendingNotification(context.Context) (int64, error)
	MarkNotified(context.Context, int64) error
}
type ChannelService struct {
	CredentialProbe ChannelCredentialProbe
	CredentialNow   func() time.Time
	DirectoryWake   chan struct{}
	Store           ChannelStore
	Secret          []byte
}
