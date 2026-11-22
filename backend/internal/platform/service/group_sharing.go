package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// GroupSharingPolicy authorizes only explicit directed reads. It never creates
// message forwarding, merges conversations, or grants transitive access.
type GroupSharingPolicy struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	AgentID     string         `json:"agent_id"`
	Name        string         `json:"name"`
	Mode        string         `json:"mode"`
	Enabled     bool           `json:"enabled"`
	Version     int64          `json:"version"`
	Sources     []SelectionRef `json:"sources"`
	Targets     []SelectionRef `json:"targets"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
type GroupSharingPut struct {
	Name    string         `json:"name"`
	Mode    string         `json:"mode"`
	Enabled bool           `json:"enabled"`
	Version int64          `json:"version"`
	Sources []SelectionRef `json:"sources"`
	Targets []SelectionRef `json:"targets"`
}
type GroupSharingEdge struct {
	Source                    SelectionRef `json:"source"`
	Target                    SelectionRef `json:"target"`
	HistoryCapability         string       `json:"history_capability"`
	HistoryPermissionVerified bool         `json:"history_permission_verified"`
	UnavailableReason         string       `json:"unavailable_reason,omitempty"`
}
type GroupSharingPreview struct {
	Edges []GroupSharingEdge `json:"edges"`
}
type GroupSharingPage struct {
	Items []GroupSharingPolicy `json:"items"`
	Total int                  `json:"total"`
}
type GroupSharingStore interface {
	ListSharingPolicies(context.Context, string, string, string, string, int, int) (GroupSharingPage, error)
	GetSharingPolicy(context.Context, string, string, string, string) (GroupSharingPolicy, error)
	SaveSharingPolicy(context.Context, string, string, string, string, GroupSharingPut, []GroupSharingEdge) (GroupSharingPolicy, error)
	DeleteSharingPolicy(context.Context, string, string, string, string, int64) error
	PreviewSharingPolicy(context.Context, string, string, string, GroupSharingPut, []GroupSharingEdge) (GroupSharingPreview, error)
}
type GroupSharingService struct{ Store GroupSharingStore }

func validSharingScope(w, a string) bool { return agentUUID(w) && agentUUID(a) }
func (s *GroupSharingService) List(ctx context.Context, w, actor, a, q string, page, size int) (GroupSharingPage, error) {
	if !validSharingScope(w, a) || page < 1 || page > 100000 || size < 1 || size > 100 || utf8.RuneCountInString(q) > 100 || strings.ContainsAny(q, "\x00\r\n") {
		return GroupSharingPage{}, Fail("invalid_sharing_query", 400, "共享策略查询无效")
	}
	return s.Store.ListSharingPolicies(ctx, w, actor, a, strings.TrimSpace(q), page, size)
}
func (s *GroupSharingService) Get(ctx context.Context, w, actor, a, id string) (GroupSharingPolicy, error) {
	if !validSharingScope(w, a) || !agentUUID(id) {
		return GroupSharingPolicy{}, Fail("invalid_sharing_policy", 400, "共享策略标识无效")
	}
	return s.Store.GetSharingPolicy(ctx, w, actor, a, id)
}
func (s *GroupSharingService) Save(ctx context.Context, w, actor, a, id string, q GroupSharingPut) (GroupSharingPolicy, error) {
	if !validSharingScope(w, a) || (id != "" && !agentUUID(id)) || (id != "" && q.Version < 1) || (id == "" && q.Version != 0) {
		return GroupSharingPolicy{}, Fail("invalid_sharing_policy", 400, "共享策略标识或版本无效")
	}
	q.Name = strings.TrimSpace(q.Name)
	edges, err := SharingPolicyEdges(w, a, q)
	if err != nil {
		return GroupSharingPolicy{}, err
	}
	return s.Store.SaveSharingPolicy(ctx, w, actor, a, id, q, edges)
}
func (s *GroupSharingService) Delete(ctx context.Context, w, actor, a, id string, version int64) error {
	if !validSharingScope(w, a) || !agentUUID(id) || version < 1 {
		return Fail("invalid_sharing_policy", 400, "共享策略标识或版本无效")
	}
	return s.Store.DeleteSharingPolicy(ctx, w, actor, a, id, version)
}
func (s *GroupSharingService) Preview(ctx context.Context, w, actor, a string, q GroupSharingPut) (GroupSharingPreview, error) {
	if !validSharingScope(w, a) {
		return GroupSharingPreview{}, Fail("invalid_sharing_policy", 400, "共享策略范围无效")
	}
	q.Name = strings.TrimSpace(q.Name)
	edges, err := SharingPolicyEdges(w, a, q)
	if err != nil {
		return GroupSharingPreview{}, err
	}
	return s.Store.PreviewSharingPolicy(ctx, w, actor, a, q, edges)
}

// SharingPolicyEdges expands one policy, never follows another policy's edges.
func SharingPolicyEdges(w, a string, q GroupSharingPut) ([]GroupSharingEdge, error) {
	fail := Fail("invalid_sharing_policy", 400, "共享策略名称、方向或主体无效")
	if q.Name == "" || utf8.RuneCountInString(q.Name) > 100 || strings.ContainsAny(q.Name, "\x00\r\n") || q.Sources == nil || q.Targets == nil || len(q.Sources) < 1 || len(q.Sources) > 20 || len(q.Targets) > 20 {
		return nil, fail
	}
	if (q.Mode == "directed" && len(q.Targets) < 1) || (q.Mode == "mutual" && (len(q.Sources) < 2 || len(q.Targets) != 0)) || (q.Mode != "directed" && q.Mode != "mutual") {
		return nil, fail
	}
	seen := map[string]bool{}
	for _, r := range q.Sources {
		if r.Kind != "group" || !validSharingRef(w, a, r) || seen[sharingIdentityKey(r)] {
			return nil, fail
		}
		seen[sharingIdentityKey(r)] = true
	}
	for _, r := range q.Targets {
		if !validSharingRef(w, a, r) || seen[sharingIdentityKey(r)] {
			return nil, fail
		}
		seen[sharingIdentityKey(r)] = true
	}
	edges := []GroupSharingEdge{}
	targets := q.Targets
	if q.Mode == "mutual" {
		targets = q.Sources
	}
	for _, source := range q.Sources {
		for _, target := range targets {
			if sharingIdentityKey(source) == sharingIdentityKey(target) {
				continue
			}
			edges = append(edges, sharingEdge(source, target))
		}
	}
	return edges, nil
}
func sharingIdentityKey(r SelectionRef) string {
	return strings.Join([]string{r.Provider, r.Kind, r.BindingID, r.ExternalID}, "\x00")
}
func validSharingRef(w, a string, r SelectionRef) bool {
	if r.Kind == "group" && !agentUUID(r.ID) {
		return false
	}
	if r.WorkspaceID != w || r.AgentID != a || (r.Kind != "group" && r.Kind != "user") || r.ExternalID == "" || len(r.ExternalID) > 512 || strings.ContainsAny(r.ExternalID, "\x00\r\n") {
		return false
	}
	if r.Provider == "web" {
		return agentUUID(r.ExternalID) && r.BindingID == "" && r.IdentityEpoch == 0 && r.Generation == 0 && r.TenantID == ""
	}
	return ValidChannelProvider(r.Provider) && agentUUID(r.BindingID) && r.IdentityEpoch > 0 && r.Generation > 0 && r.TenantID != "" && r.Namespace != ""
}
func sharingEdge(source, target SelectionRef) GroupSharingEdge {
	edge := GroupSharingEdge{Source: source, Target: target, HistoryCapability: "unsupported"}
	switch source.Provider {
	case "web":
		edge.HistoryCapability = "local_history"
	case "feishu":
		edge.HistoryCapability = "provider_history"
	case "wecom":
		edge.HistoryCapability = "recent_window"
	default:
		edge.UnavailableReason = "history_not_supported"
	}
	return edge
}
