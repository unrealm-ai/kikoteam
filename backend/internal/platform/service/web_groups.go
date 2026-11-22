package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type WebGroupMember struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	Name   string `json:"name,omitempty"`
}
type WebGroupAgent struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name,omitempty"`
}
type WebGroup struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspace_id"`
	Title       string           `json:"title"`
	Status      string           `json:"status"`
	Version     int64            `json:"version"`
	CreatedAt   time.Time        `json:"created_at"`
	Members     []WebGroupMember `json:"members"`
	Agents      []WebGroupAgent  `json:"agents"`
}
type WebGroupPut struct {
	Version int64            `json:"version"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Members []WebGroupMember `json:"members"`
	Agents  []WebGroupAgent  `json:"agents"`
}
type WebGroupPage struct {
	Items []WebGroup `json:"items"`
	Total int        `json:"total"`
}
type WebGroupStore interface {
	LeaveWebGroup(context.Context, string, string, string) error
	SaveWebGroup(context.Context, string, string, string, WebGroupPut) (WebGroup, error)
	GetWebGroup(context.Context, string, string, string) (WebGroup, error)
	ListWebGroups(context.Context, string, string, string, int, int) (WebGroupPage, error)
}
type WebGroupService struct{ Store WebGroupStore }

func (s *WebGroupService) Save(ctx context.Context, w, actor, id string, q WebGroupPut) (WebGroup, error) {
	if !agentUUID(w) || id != "" && !agentUUID(id) || q.Version < 0 {
		return WebGroup{}, Fail("invalid_web_group", 400, "群标识或版本无效")
	}
	q.Title = strings.TrimSpace(q.Title)
	if q.Title == "" || utf8.RuneCountInString(q.Title) > 200 || strings.ContainsRune(q.Title, 0) {
		return WebGroup{}, Fail("invalid_web_group", 400, "群名称无效")
	}
	if q.Status == "" {
		q.Status = "active"
	}
	if q.Status != "active" && q.Status != "archived" {
		return WebGroup{}, Fail("invalid_web_group", 400, "群状态无效")
	}
	if len(q.Members) > 200 || len(q.Agents) > 100 {
		return WebGroup{}, Fail("invalid_web_group", 400, "群成员过多")
	}
	if id == "" {
		found := false
		for i := range q.Members {
			if q.Members[i].UserID == actor {
				q.Members[i].Role = "owner"
				found = true
			}
		}
		if !found {
			q.Members = append(q.Members, WebGroupMember{UserID: actor, Role: "owner"})
		}
	}
	seen := map[string]bool{}
	owners := 0
	for _, m := range q.Members {
		if !agentUUID(m.UserID) || seen[m.UserID] || (m.Role != "owner" && m.Role != "admin" && m.Role != "member") {
			return WebGroup{}, Fail("invalid_web_group", 400, "群成员无效或重复")
		}
		seen[m.UserID] = true
		if m.Role == "owner" {
			owners++
		}
	}
	if owners != 1 || len(q.Members) > 200 {
		return WebGroup{}, Fail("invalid_web_group", 400, "必须保留唯一群主")
	}
	seen = map[string]bool{}
	for _, a := range q.Agents {
		if !agentUUID(a.AgentID) || seen[a.AgentID] {
			return WebGroup{}, Fail("invalid_web_group", 400, "群员工无效或重复")
		}
		seen[a.AgentID] = true
	}
	return s.Store.SaveWebGroup(ctx, w, actor, id, q)
}
func (s *WebGroupService) Get(ctx context.Context, w, actor, id string) (WebGroup, error) {
	if !agentUUID(w) || !agentUUID(id) {
		return WebGroup{}, Fail("invalid_web_group", 400, "群标识无效")
	}
	return s.Store.GetWebGroup(ctx, w, actor, id)
}
func (s *WebGroupService) List(ctx context.Context, w, actor, q string, page, size int) (WebGroupPage, error) {
	if !agentUUID(w) || len(q) > 200 || strings.ContainsRune(q, 0) {
		return WebGroupPage{}, Fail("invalid_web_group", 400, "查询无效")
	}
	if err := pageValid(page, size); err != nil {
		return WebGroupPage{}, err
	}
	return s.Store.ListWebGroups(ctx, w, actor, strings.TrimSpace(q), page, size)
}

func (s *WebGroupService) Leave(ctx context.Context, w, actor, id string) error {
	if !agentUUID(w) || !agentUUID(id) {
		return Fail("invalid_web_group", 400, "群标识无效")
	}
	return s.Store.LeaveWebGroup(ctx, w, actor, id)
}
