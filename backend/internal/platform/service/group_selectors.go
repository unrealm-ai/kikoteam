package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// GroupSelection is configuration metadata, not permission to read messages or send.
type GroupSelection struct {
	Ref            SelectionRef `json:"ref"`
	Provider       string       `json:"provider"`
	Name           string       `json:"name"`
	Evidence       string       `json:"evidence"`
	Coverage       string       `json:"coverage"`
	Status         string       `json:"status"`
	Selectable     bool         `json:"selectable"`
	DisabledReason string       `json:"disabled_reason,omitempty"`
}
type GroupSelectionPage struct {
	Items      []GroupSelection `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}
type GroupSelectionQuery struct {
	Provider, Q, Cursor string
	Limit               int
}
type GroupSelectionBoundary struct{ Provider, ID string }
type GroupSelectorStore interface {
	SearchGroups(context.Context, string, string, string, GroupSelectionQuery, GroupSelectionBoundary) ([]GroupSelection, bool, error)
	ResolveGroups(context.Context, string, string, string, []SelectionRef) ([]GroupSelection, error)
}
type GroupSelectorService struct {
	Store  GroupSelectorStore
	Secret []byte
}
type groupSelectionCursor struct {
	Scope    string
	Boundary GroupSelectionBoundary
}

func (s *GroupSelectorService) Search(ctx context.Context, w, actor, agent string, q GroupSelectionQuery) (GroupSelectionPage, error) {
	out := GroupSelectionPage{Items: []GroupSelection{}}
	q.Q = strings.TrimSpace(q.Q)
	if !agentUUID(w) || !agentUUID(agent) || q.Limit < 1 || q.Limit > 100 || utf8.RuneCountInString(q.Q) > 100 || strings.ContainsAny(q.Q, "\x00\r\n") || (q.Provider != "" && q.Provider != "web" && !ValidChannelProvider(q.Provider)) || len(q.Cursor) > 4096 {
		return out, Fail("invalid_group_query", 400, "群查询参数无效")
	}
	if s == nil || s.Store == nil || len(s.Secret) < 32 {
		return out, Fail("dependency_unavailable", 503, "群选择不可用")
	}
	scopeBytes, err := json.Marshal([]string{w, actor, agent, q.Provider, q.Q})
	if err != nil {
		return out, err
	}
	scope := string(scopeBytes)
	boundary, err := s.decodeGroupCursor(q.Cursor, scope)
	if err != nil {
		return out, err
	}
	return s.groupPage(ctx, w, actor, agent, q, boundary, scope)
}
func (s *GroupSelectorService) groupPage(ctx context.Context, w, actor, agent string, q GroupSelectionQuery, b GroupSelectionBoundary, scope string) (GroupSelectionPage, error) {
	items, more, err := s.Store.SearchGroups(ctx, w, actor, agent, q, b)
	out := GroupSelectionPage{Items: items}
	if err != nil {
		return out, err
	}
	if more && len(items) > 0 {
		last := items[len(items)-1]
		raw, err := json.Marshal(groupSelectionCursor{Scope: scope, Boundary: GroupSelectionBoundary{Provider: last.Provider, ID: last.Ref.ID}})
		if err != nil {
			return out, err
		}
		mac := hmac.New(sha256.New, s.Secret)
		_, _ = mac.Write(raw)
		token := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		out.NextCursor = &token
	}
	return out, nil
}
func (s *GroupSelectorService) decodeGroupCursor(token, scope string) (GroupSelectionBoundary, error) {
	var c groupSelectionCursor
	if token == "" {
		return c.Boundary, nil
	}
	invalid := Fail("invalid_cursor", 400, "群分页游标无效")
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c.Boundary, invalid
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return c.Boundary, invalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return c.Boundary, invalid
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(raw, &c) != nil || c.Scope != scope || !agentUUID(c.Boundary.ID) {
		return GroupSelectionBoundary{}, invalid
	}
	return c.Boundary, nil
}
func (s *GroupSelectorService) Resolve(ctx context.Context, w, actor, agent string, refs []SelectionRef) (GroupSelectionPage, error) {
	out := GroupSelectionPage{Items: []GroupSelection{}}
	if !agentUUID(w) || !agentUUID(agent) || len(refs) < 1 || len(refs) > 100 {
		return out, Fail("invalid_selection", 400, "群选择无效")
	}
	for _, ref := range refs {
		if ref.Kind != "group" || ref.WorkspaceID != w || ref.AgentID != agent || !agentUUID(ref.ID) || (ref.Provider != "web" && !ValidChannelProvider(ref.Provider)) {
			return out, Fail("invalid_selection", 400, "群选择来源无效")
		}
	}
	if s == nil || s.Store == nil {
		return out, Fail("dependency_unavailable", 503, "群选择不可用")
	}
	items, err := s.Store.ResolveGroups(ctx, w, actor, agent, refs)
	out.Items = items
	return out, err
}

// SavedGroupSelectorStore resolves display metadata, including unavailable saved
// references. It must not replace an old source with a current same-name group.
type SavedGroupSelectorStore interface {
	ResolveSavedGroups(context.Context, string, string, string, []UsageSelector) ([]GroupSelection, error)
}

func (s *GroupSelectorService) ResolveSaved(ctx context.Context, w, actor, agent string, selectors []UsageSelector) (GroupSelectionPage, error) {
	out := GroupSelectionPage{Items: []GroupSelection{}}
	if !agentUUID(w) || !agentUUID(agent) || selectors == nil || len(selectors) > 200 {
		return out, Fail("invalid_selection", 400, "群选择无效")
	}
	for _, v := range selectors {
		if !validUsageSelector(v) || v.Kind != "group" {
			return out, Fail("invalid_selection", 400, "群选择来源无效")
		}
	}
	if s == nil {
		return out, Fail("dependency_unavailable", 503, "群选择不可用")
	}
	store, ok := s.Store.(SavedGroupSelectorStore)
	if !ok {
		return out, Fail("dependency_unavailable", 503, "群选择不可用")
	}
	items, err := store.ResolveSavedGroups(ctx, w, actor, agent, selectors)
	out.Items = items
	return out, err
}
