package service

import (
	"context"
	"time"
)

// ResolveWebConversation keeps one stable private conversation for this user
// and employee. It never revives or replaces archived legacy conversations.
func (s *Service) ResolveWebConversation(ctx context.Context, w, actor, agent string) (Conversation, error) {
	if !ValidID(agent) {
		return Conversation{}, Fail("invalid_argument", 400, "员工标识无效")
	}
	return s.Store.ResolveWebConversation(ctx, Conversation{ID: NewID(), WorkspaceID: w, AgentID: agent, CreatedBy: actor, WebUserID: &actor, Lifecycle: "active", CreatedAt: time.Now().UTC()})
}
