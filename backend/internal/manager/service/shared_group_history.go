package service

import (
	"context"
	"encoding/json"
)

// SharedHistoryScope is supplied only by the authenticated runtime authority.
// A model's selection of a source is not a grant.
type SharedHistoryScope struct {
	WorkspaceID, AgentID, SessionID, InputID, PolicyID string
	PolicyVersion                                      int64
	EdgeOrdinal                                        int
	SourceRef, TargetRef                               json.RawMessage
}
type SharedHistoryRequest struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
	Limit   int   `json:"limit"`
}
type SharedHistoryItem struct {
	ID          string `json:"record_id"`
	MessageID   string `json:"provider_message_id,omitempty"`
	SenderID    string `json:"sender_id,omitempty"`
	SenderName  string `json:"sender_name,omitempty"`
	SenderType  string `json:"sender_type"`
	CreatedAtMS int64  `json:"created_at_ms"`
	Text        string `json:"text"`
	MessageType string `json:"message_type"`
}
type SharedHistoryPage struct {
	PendingItems       []SharedHistoryItem `json:"pending_items,omitempty"`
	Items              []SharedHistoryItem `json:"items"`
	NextProviderCursor string              `json:"next_provider_cursor,omitempty"`
	Coverage           string              `json:"coverage"`
}
type SharedHistoryResult struct {
	ID        string              `json:"query_id"`
	Status    string              `json:"status"`
	SourceRef json.RawMessage     `json:"source"`
	StartMS   int64               `json:"start_ms"`
	EndMS     int64               `json:"end_ms"`
	Items     []SharedHistoryItem `json:"items"`
	HasMore   bool                `json:"has_more"`
	Coverage  string              `json:"coverage"`
	ErrorCode string              `json:"error_code,omitempty"`
}
type SharedHistoryJob struct {
	PendingItems   []SharedHistoryItem `json:"pending_items,omitempty"`
	ID             string              `json:"id"`
	LeaseToken     string              `json:"lease_token"`
	SourceRef      json.RawMessage     `json:"source"`
	StartMS        int64               `json:"start_ms"`
	EndMS          int64               `json:"end_ms"`
	Limit          int                 `json:"limit"`
	ProviderCursor string              `json:"provider_cursor"`
}
type SharedHistoryCompletion struct {
	LeaseToken string            `json:"lease_token"`
	Status     string            `json:"status"`
	Page       SharedHistoryPage `json:"page"`
}
type SharedHistoryWorkerStore interface {
	ClaimSharedHistory(context.Context) ([]SharedHistoryJob, error)
	AuthorizeSharedHistory(context.Context, string, string) (SharedHistoryJob, error)
	CompleteSharedHistory(context.Context, string, SharedHistoryCompletion) error
}
