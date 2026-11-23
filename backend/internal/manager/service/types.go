// Package service owns business conversation and execution admission use cases.
package service

import (
	"context"
	"encoding/json"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"time"
)

type Conversation struct {
	ChannelCreatedBy *string    `json:"channel_created_by,omitempty"`
	ChannelBindingID *string    `json:"channel_binding_id,omitempty"`
	WebUserID        *string    `json:"web_user_id"`
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	AgentID          string     `json:"agent_id"`
	CreatedBy        string     `json:"created_by"`
	Title            *string    `json:"title"`
	Lifecycle        string     `json:"lifecycle"`
	ChatSessionID    *string    `json:"chat_session_id"`
	CleanupPending   bool       `json:"cleanup_pending"`
	CreatedAt        time.Time  `json:"created_at"`
	ArchivedAt       *time.Time `json:"archived_at"`
}

// Configuration is immutable and contains no credential. Live credentials are
// resolved only for the authorized Engine using this snapshot's fixed target.
type Configuration struct {
	Vision          bool                            `json:"supports_images,omitempty"`
	PlatformModelID string                          `json:"platform_model_id,omitempty"`
	AgentVersion    int64                           `json:"agent_version,string"`
	ModelProfileID  string                          `json:"model_profile_id"`
	ModelVersion    int64                           `json:"model_version,string"`
	Protocol        string                          `json:"protocol,omitempty"`
	BaseURL         string                          `json:"base_url,omitempty"`
	ModelID         string                          `json:"model_id,omitempty"`
	Instruction     string                          `json:"instruction,omitempty"`
	Harness         string                          `json:"harness"`
	Skills          []string                        `json:"skills"`
	Plugins         []string                        `json:"plugins"`
	Capabilities    *platform.ExecutionCapabilities `json:"capabilities,omitempty"`
}

type RequiredAction struct {
	Type      string     `json:"type"`
	RequestID string     `json:"request_id"`
	TurnID    string     `json:"turn_id"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Confirmation struct {
	RequestID string `json:"request_id"`
	Confirmed *bool  `json:"confirmed"`
}

type Execution struct {
	RequiredActions        []RequiredAction `json:"required_actions"`
	State                  string           `json:"state"`
	ActiveTurnID           *string          `json:"active_turn_id"`
	QueuedInputCount       int              `json:"queued_input_count"`
	BackgroundProcessCount *int             `json:"background_process_count"`
	BlockReason            *string          `json:"block_reason"`
}

type Session struct {
	InputRevision    int64         `json:"input_revision,string"`
	PlanningState    string        `json:"planning_state,omitempty"`
	PlanningVersion  int64         `json:"planning_version,string,omitempty"`
	TaskSpaceID      *string       `json:"task_space_id,omitempty"`
	ChannelCreatedBy *string       `json:"channel_created_by,omitempty"`
	ID               string        `json:"id"`
	WorkspaceID      string        `json:"workspace_id"`
	AgentID          string        `json:"agent_id"`
	ConversationID   string        `json:"conversation_id,omitempty"`
	TaskID           *string       `json:"task_id"`
	CreatedBy        string        `json:"created_by"`
	Mode             string        `json:"mode"`
	Title            *string       `json:"title"`
	Lifecycle        string        `json:"lifecycle"`
	Revision         int64         `json:"revision,string"`
	InitialInputID   *string       `json:"initial_input_id"`
	Configuration    Configuration `json:"configuration"`
	Execution        Execution     `json:"execution"`
	AcceptsInput     bool          `json:"accepts_input"`
	InputBlockReason *string       `json:"input_block_reason"`
	CreatedAt        time.Time     `json:"created_at"`
	ArchivedAt       *time.Time    `json:"archived_at"`
}

type InputReceipt struct {
	Recorded      bool    `json:"recorded"`
	Delivery      string  `json:"delivery"`
	Application   string  `json:"application"`
	ContextState  *string `json:"context_state"`
	InputRevision int64   `json:"input_revision,string"`
	Duplicate     bool    `json:"duplicate"`
}
type Input struct {
	Attachments           []Attachment  `json:"attachments,omitempty"`
	ConfirmationRequestID *string       `json:"confirmation_request_id,omitempty"`
	Confirmed             *bool         `json:"confirmed,omitempty"`
	Receipt               *InputReceipt `json:"receipt,omitempty"`
	NotificationID        *string       `json:"notification_id,omitempty"`
	TriggerEventID        *string       `json:"trigger_event_id,omitempty"`
	// Nil on internal writes preserves the legacy default true; reads always return a value.
	TriggerAgentLoop   *bool      `json:"trigger_agent_loop"`
	DeliveryMode       string     `json:"delivery_mode"`
	SourceKind         string     `json:"source_kind"`
	ReportID           *string    `json:"report_id"`
	ChannelPrincipalID *string    `json:"channel_principal_id,omitempty"`
	PrincipalType      string     `json:"principal_type"`
	ID                 string     `json:"id"`
	SessionID          string     `json:"session_id"`
	SubmittedBy        string     `json:"submitted_by"`
	Seq                int64      `json:"seq,string"`
	Text               string     `json:"text"`
	Status             string     `json:"status"`
	TurnID             *string    `json:"turn_id"`
	CancelReason       *string    `json:"cancel_reason"`
	RejectionReason    *string    `json:"rejection_reason"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}

type Turn struct {
	ID                    string          `json:"id"`
	SessionID             string          `json:"session_id"`
	InputID               string          `json:"input_id"`
	TurnNo                int64           `json:"turn_no,string"`
	Status                string          `json:"status"`
	ResultSummary         *string         `json:"result_summary"`
	Error                 json.RawMessage `json:"error"`
	Usage                 json.RawMessage `json:"usage"`
	HasUnknownToolResults bool            `json:"has_unknown_tool_results"`
	StartedAt             time.Time       `json:"started_at"`
	FinishedAt            *time.Time      `json:"finished_at"`
}

type CreateConversation struct {
	AgentID string  `json:"agent_id"`
	Title   *string `json:"title"`
}
type CreateSession struct {
	AttachmentIDs   []string `json:"attachment_ids,omitempty"`
	AttachmentPaths []string `json:"attachment_paths,omitempty"`
	TaskSpaceID     string   `json:"task_space_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	DeliveryMode    string   `json:"delivery_mode,omitempty"`
	ConversationID  string   `json:"conversation_id,omitempty"`
	Mode            string   `json:"mode"`
	Title           *string  `json:"title"`
	Input           *struct {
		Text string `json:"text"`
	} `json:"input"`
}
type Page[T any] struct {
	StatusCounts map[string]int `json:"status_counts,omitempty"`
	Items        []T            `json:"items"`
	NextCursor   *string        `json:"next_cursor"`
}
type EventPage struct {
	Page[json.RawMessage]
	ResumeCursor *string `json:"resume_cursor"`
}

type Platform interface {
	Snapshot(context.Context, string, string, string, string) (Configuration, error)
	ModelCredential(context.Context, string, string, Configuration) (string, error)
}

type Store interface {
	TaskStore
	InsertConversation(context.Context, Conversation) error
	ResolveWebConversation(context.Context, Conversation) (Conversation, error)
	Conversations(context.Context, string, string, ConversationFilter) ([]Conversation, error)
	Conversation(context.Context, string, string, string) (Conversation, error)
	WithSession(context.Context, Target, func(SessionTx) error) error
	WithProjectPublication(context.Context, Target, func(SessionState) error) error
	Session(context.Context, string, string, string) (Session, error)
	Inputs(context.Context, string, string, string, string, int64, int) ([]Input, error)
	Input(context.Context, string, string, string, string) (Input, error)
	Turns(context.Context, string, string, string, string, int64, int) ([]Turn, error)
	Turn(context.Context, string, string, string, string) (Turn, error)
	Sessions(context.Context, string, string, SessionFilter) ([]Session, error)
	Events(context.Context, string, string, string, EventFilter) ([]json.RawMessage, int64, error)
}

type ConversationFilter struct {
	AgentID, Lifecycle, BeforeID string
	BeforeTime                   time.Time
	Limit                        int
}

type Service struct {
	ProjectRoot  string
	Store        Store
	Platform     Platform
	CursorSecret []byte
}
