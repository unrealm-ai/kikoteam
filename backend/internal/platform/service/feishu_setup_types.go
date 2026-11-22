package service

import (
	"context"
	"sync"
	"time"
)

type FeishuPermission struct {
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
}
type FeishuDiagnostics struct {
	CheckedAt        time.Time          `json:"checked_at"`
	CredentialStatus string             `json:"credential_status"`
	BotStatus        string             `json:"bot_status"`
	DirectoryStatus  string             `json:"directory_status"`
	ScopeStatus      string             `json:"scope_status"`
	Permissions      []FeishuPermission `json:"permissions"`
	Ready            bool               `json:"ready"`
	ErrorCode        string             `json:"error_code"`
	ErrorDetail      string             `json:"error_detail,omitempty"`
	TenantID         string             `json:"-"`
	BotID            string             `json:"-"`
}

func legacyFeishuPermissions() []FeishuPermission {
	required := []string{"tenant:tenant:readonly", "contact:contact.base:readonly", "contact:user.base:readonly", "contact:user.department:readonly", "contact:department.base:readonly", "contact:department.organize:readonly", "im:message.p2p_msg:readonly", "im:message.group_at_msg:readonly", "im:message:send_as_bot"}
	out := make([]FeishuPermission, 0, len(required)+3)
	for _, s := range required {
		out = append(out, FeishuPermission{Scope: s, Required: true, Status: "unknown"})
	}
	for _, s := range []string{"im:message:readonly", "im:message.group_msg", "im:chat:readonly"} {
		out = append(out, FeishuPermission{Scope: s, Status: "unknown"})
	}
	return out
}

type FeishuAutoCheck struct {
	Eligible    bool       `json:"eligible"`
	InProgress  bool       `json:"in_progress"`
	NextCheckAt *time.Time `json:"next_check_at,omitempty"`
}
type FeishuContactRange struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	ErrorCode string    `json:"error_code,omitempty"`
}
type FeishuRegistration struct {
	ReplacesRegistrationID string                   `json:"replaces_registration_id,omitempty"`
	PermissionCheckedAt    *time.Time               `json:"permission_checked_at,omitempty"`
	Profile                *FeishuPermissionProfile `json:"profile,omitempty"`
	PermissionGroups       []FeishuPermissionGroup  `json:"permission_groups,omitempty"`
	ContactRange           *FeishuContactRange      `json:"contact_range,omitempty"`
	ConnectionID           string                   `json:"connection_id,omitempty"`
	ConnectionGeneration   int64                    `json:"connection_generation,omitempty"`
	ExtensionChecking      bool                     `json:"extension_checking,omitempty"`
	CheckLeaseUntil        *time.Time               `json:"check_lease_until,omitempty"`

	NextCheckAt      *time.Time         `json:"next_check_at,omitempty"`
	CheckAttempts    int                `json:"check_attempts,omitempty"`
	AutoRetryBlocked bool               `json:"auto_retry_blocked,omitempty"`
	AutoCheck        *FeishuAutoCheck   `json:"auto_check,omitempty"`
	ID               string             `json:"id"`
	State            string             `json:"state"`
	AppID            string             `json:"app_id"`
	VerificationURL  string             `json:"verification_url,omitempty"`
	ExpiresAt        time.Time          `json:"expires_at"`
	NextPollAt       time.Time          `json:"next_poll_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	CredentialSaved  bool               `json:"credential_saved"`
	ErrorCode        string             `json:"error_code"`
	Diagnostics      *FeishuDiagnostics `json:"diagnostics,omitempty"`
	WorkspaceID      string             `json:"-"`
	AgentID          string             `json:"-"`
	ActorID          string             `json:"-"`
	OperationKey     string             `json:"-"`
	Version          int64              `json:"-"`
	Interval         int                `json:"poll_interval_seconds"`
	DeviceCipher     []byte             `json:"-"`
	SecretCipher     []byte             `json:"-"`
}
type FeishuSetupStatus struct {
	Registration *FeishuRegistration `json:"registration"`
	Permissions  []FeishuPermission  `json:"permissions"`
}
type FeishuBegin struct {
	DeviceCode, URL     string
	ExpiresIn, Interval int
}
type FeishuPoll struct{ State, AppID, Secret string }
type FeishuSetupProvider interface {
	Begin(context.Context, string, string) (FeishuBegin, error)
	Poll(context.Context, string) (FeishuPoll, error)
	Diagnose(context.Context, string, string) (FeishuDiagnostics, error)
}
type FeishuSetupTx interface {
	ChannelTx
	Registration(context.Context, string) (FeishuRegistration, error)
	LatestRegistration(context.Context, string, string) (*FeishuRegistration, error)
	RegistrationByKey(context.Context, string, string) (*FeishuRegistration, error)
	InsertRegistration(context.Context, FeishuRegistration) error
	SaveRegistration(context.Context, FeishuRegistration) error
	AgentPreset(context.Context, string) (string, string, error)
}
type FeishuSetupStore interface {
	WithSetup(context.Context, string, func(FeishuSetupTx) error) error
}
type FeishuSetupService struct {
	activeRegistrations sync.Map
	activeChecks        sync.Map
	Store               FeishuSetupStore
	Channels            *ChannelService
	Provider            FeishuSetupProvider
	Now                 func() time.Time
}
