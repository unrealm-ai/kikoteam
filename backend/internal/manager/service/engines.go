package service

import (
	"context"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Engine struct {
	ID           string `json:"engine_id"`
	HostID       string `json:"host_id"`
	Endpoint     string `json:"endpoint"`
	Capacity     int    `json:"capacity"`
	HeartbeatSeq int64  `json:"heartbeat_seq,string"`
	Accepting    bool   `json:"accepting"`
}
type Assignment struct {
	SessionID    string `json:"session_id"`
	AssignmentID string `json:"assignment_id"`
	Generation   int64  `json:"generation,string"`
}
type HarnessConfig struct {
	AttachmentToken       string                    `json:"attachment_token,omitempty"`
	ReportOnly            bool                      `json:"report_only,omitempty"`
	WebhookFile           *WebhookFile              `json:"webhook_file,omitempty"`
	Employee              EmployeePrompt            `json:"-"`
	TodoStarted           bool                      `json:"-"`
	WorkRole              string                    `json:"work_role,omitempty"`
	WorkLocalToken        string                    `json:"work_local_token,omitempty"`
	WorkMCPToken          string                    `json:"work_mcp_token,omitempty"`
	WorkAssignmentID      string                    `json:"-"`
	WorkSourceAgentID     string                    `json:"-"`
	ConfirmationOnly      bool                      `json:"confirmation_only,omitempty"`
	HumanTools            bool                      `json:"-"`
	HumanToolsToken       string                    `json:"human_tools_token,omitempty"`
	HumanMCPToken         string                    `json:"human_mcp_token,omitempty"`
	GroupHistoryToken     string                    `json:"group_history_token,omitempty"`
	TaskSpaceHistoryToken string                    `json:"task_space_history_token,omitempty"`
	TriggerID             string                    `json:"-"`
	TriggerVersion        int64                     `json:"-"`
	TriggerEventID        string                    `json:"-"`
	WebGroup              bool                      `json:"-"`
	ChannelPrincipalID    *string                   `json:"-"`
	ChannelGeneration     int64                     `json:"-"`
	InputID               string                    `json:"-"`
	CapabilityToken       string                    `json:"capability_token,omitempty"`
	Resources             platform.RuntimeResources `json:"resources"`
	DelegationToken       string                    `json:"delegation_token,omitempty"`
	SessionID             string                    `json:"session_id"`
	WorkspaceID           string                    `json:"workspace_id"`
	AgentID               string                    `json:"agent_id"`
	ConversationID        string                    `json:"conversation_id,omitempty"`
	TaskSpaceID           string                    `json:"task_space_id,omitempty"`
	TaskID                string                    `json:"task_id,omitempty"`
	Mode                  string                    `json:"mode"`
	Configuration         Configuration             `json:"configuration"`
	APIKey                string                    `json:"api_key"`
}
type WebhookFile struct {
	EventID string `json:"event_id"`
	SHA256  string `json:"sha256"`
	Body    []byte `json:"body"`
}
type EngineStore interface {
	RegisterEngine(context.Context, Engine) error
	HeartbeatEngine(context.Context, string, int64, bool) error
	Dispatch(context.Context) error
	Assignments(context.Context, string) ([]Assignment, error)
	HarnessConfig(context.Context, string, string, string, int64, string) (HarnessConfig, string, error)
	Release(context.Context, string, string, string, int64) error
	Sandboxes(context.Context, string) ([]Sandbox, error)
	SandboxState(context.Context, string, string, string) error
}

// Sandbox is a limited Runner configuration; it never contains model keys.
type Sandbox struct {
	ID             string           `json:"sandbox_id"`
	WorkspaceID    string           `json:"workspace_id"`
	AgentID        string           `json:"agent_id"`
	ConversationID string           `json:"conversation_id,omitempty"`
	TaskSpaceID    string           `json:"task_space_id,omitempty"`
	TaskID         string           `json:"task_id,omitempty"`
	EngineID       string           `json:"engine_id"`
	Lifecycle      string           `json:"lifecycle"`
	State          string           `json:"state"`
	ReclaimAllowed bool             `json:"reclaim_allowed"`
	ConfigToken    string           `json:"config_token,omitempty"`
	Sessions       []SandboxSession `json:"sessions"`
}
type SandboxSession struct {
	ConversationID string  `json:"conversation_id,omitempty"`
	TaskSpaceID    string  `json:"task_space_id,omitempty"`
	TaskID         string  `json:"task_id,omitempty"`
	SessionID      string  `json:"session_id"`
	AssignmentID   *string `json:"assignment_id"`
	Generation     int64   `json:"generation,string"`
	Lifecycle      string  `json:"lifecycle"`
	EngineID       *string `json:"engine_id"`
}
