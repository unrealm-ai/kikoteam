package service

import "context"

// ConversationSearchHit includes only the fields needed for a navigation result.
type ConversationSearchHit struct {
	ID, AgentID, Title, AgentName string
}

type conversationSearchStore interface {
	SearchWebConversations(context.Context, string, string, string, int) ([]ConversationSearchHit, error)
}

func (s *Service) SearchWebConversations(ctx context.Context, workspace, actor, query string, limit int) ([]ConversationSearchHit, error) {
	store, ok := s.Store.(conversationSearchStore)
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "对话搜索暂不可用")
	}
	return store.SearchWebConversations(ctx, workspace, actor, query, limit)
}
