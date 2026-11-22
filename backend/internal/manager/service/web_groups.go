package service

import (
	"context"
	"strings"
	"time"
)

type WebGroupMessage struct {
	ID              string    `json:"id"`
	GroupID         string    `json:"group_id"`
	Seq             int64     `json:"seq,string"`
	Kind            string    `json:"kind"`
	SenderID        string    `json:"sender_id,omitempty"`
	AgentID         string    `json:"agent_id,omitempty"`
	DisplayName     string    `json:"display_name"`
	Text            string    `json:"text"`
	MentionAgentIDs []string  `json:"mention_agent_ids"`
	CreatedAt       time.Time `json:"created_at"`
}
type WebGroupPost struct {
	Text            string   `json:"text"`
	MentionAgentIDs []string `json:"mention_agent_ids"`
}
type WebGroupStore interface {
	PostWebGroupMessage(context.Context, string, string, string, string, WebGroupPost) (WebGroupMessage, bool, error)
	WebGroupMessages(context.Context, string, string, string, int64, int) ([]WebGroupMessage, error)
}
type WebGroupWorker interface {
	ProcessWebGroupInbox(context.Context) error
	MaterializeWebGroupReplies(context.Context) error
}

// This scope is constructed only from a verified internal delegation, never HTTP JSON.
type WebGroupExecution struct{ ConversationID string }
type webGroupContextKey struct{}

func WithWebGroupExecution(ctx context.Context, v WebGroupExecution) context.Context {
	return context.WithValue(ctx, webGroupContextKey{}, v)
}
func WebGroupExecutionFrom(ctx context.Context) (WebGroupExecution, bool) {
	v, ok := ctx.Value(webGroupContextKey{}).(WebGroupExecution)
	return v, ok && ValidID(v.ConversationID)
}
func (s *Service) PostWebGroupMessage(ctx context.Context, w, a, g, key string, v WebGroupPost) (WebGroupMessage, bool, error) {
	if !ValidID(g) || !validKey(key) || !validText(v.Text) || len(v.MentionAgentIDs) > 32 {
		return WebGroupMessage{}, false, Fail("invalid_argument", 400, "群消息无效")
	}
	seen := map[string]bool{}
	for _, id := range v.MentionAgentIDs {
		if !ValidID(id) || seen[id] {
			return WebGroupMessage{}, false, Fail("invalid_argument", 400, "被提及员工无效或重复")
		}
		seen[id] = true
	}
	if strings.ContainsRune(v.Text, 0) {
		return WebGroupMessage{}, false, Fail("invalid_argument", 400, "群消息无效")
	}
	store, ok := s.Store.(WebGroupStore)
	if !ok {
		return WebGroupMessage{}, false, Fail("unavailable", 503, "群消息尚未接入")
	}
	return store.PostWebGroupMessage(ctx, w, a, g, key, v)
}
func (s *Service) WebGroupMessages(ctx context.Context, w, a, g string, after int64, limit int) ([]WebGroupMessage, error) {
	if !ValidID(g) || after < 0 || limit < 1 || limit > 100 {
		return nil, Fail("invalid_argument", 400, "群消息分页无效")
	}
	store, ok := s.Store.(WebGroupStore)
	if !ok {
		return nil, Fail("unavailable", 503, "群消息尚未接入")
	}
	return store.WebGroupMessages(ctx, w, a, g, after, limit)
}
