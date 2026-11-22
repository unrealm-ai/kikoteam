package service

import "context"

type TriggerGroupCandidate struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
}
type TriggerGroupResolve struct {
	Provider    string `json:"provider"`
	CandidateID string `json:"candidate_id"`
}
type TriggerGroupCandidates interface {
	ListTriggerGroupCandidates(context.Context, string, string, string, int, string, string) ([]TriggerGroupCandidate, bool, error)
	ResolveTriggerGroupCandidate(context.Context, string, string, string, TriggerGroupResolve) (TriggerNotificationTarget, error)
}

func (s *Service) ListTriggerGroupCandidates(ctx context.Context, w, actor, agent, raw string, n int) (Page[TriggerGroupCandidate], error) {
	out := Page[TriggerGroupCandidate]{Items: []TriggerGroupCandidate{}}
	if n < 1 || n > 100 {
		return out, Fail("invalid_argument", 400, "limit无效")
	}
	scope := "trigger-group-candidates:" + w + ":" + actor + ":" + agent
	var provider, id string
	if raw != "" {
		c, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		provider = c.Kind
		id = c.ID
		if !ValidID(id) {
			return out, Fail("invalid_cursor", 400, "游标无效")
		}
	}
	st, ok := s.Store.(TriggerGroupCandidates)
	if !ok {
		return out, Fail("dependency_unavailable", 503, "群候选不可用")
	}
	items, more, e := st.ListTriggerGroupCandidates(ctx, w, actor, agent, n, provider, id)
	if e != nil {
		return out, e
	}
	out.Items = items
	if more && len(items) > 0 {
		last := items[len(items)-1]
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: last.Provider, ID: last.ID})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	return out, nil
}
