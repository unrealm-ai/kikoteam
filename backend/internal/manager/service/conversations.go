package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Error struct {
	Code    string
	Status  int
	Message string
}

func (e *Error) Error() string                           { return e.Message }
func Fail(code string, status int, message string) error { return &Error{code, status, message} }
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func ValidID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return false
	}
	return true
}
func validTitle(v *string) bool {
	return v == nil || strings.TrimSpace(*v) != "" && utf8.RuneCountInString(*v) <= 200 && !strings.ContainsRune(*v, '\x00')
}

func (s *Service) CreateConversation(ctx context.Context, workspace, actor string, r CreateConversation) (Conversation, error) {
	if !ValidID(r.AgentID) || !validTitle(r.Title) {
		return Conversation{}, Fail("invalid_argument", 400, "员工标识或标题无效")
	}
	c := Conversation{ID: NewID(), WorkspaceID: workspace, AgentID: r.AgentID, CreatedBy: actor, Title: r.Title, Lifecycle: "active", CreatedAt: time.Now().UTC()}
	if err := s.Store.InsertConversation(ctx, c); err != nil {
		return Conversation{}, err
	}
	return c, nil
}

func (s *Service) ListConversations(ctx context.Context, workspace, actor, agent, lifecycle, rawCursor string, limit int) (Page[Conversation], error) {
	page := Page[Conversation]{Items: []Conversation{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || agent != "" && !ValidID(agent) || lifecycle != "" && lifecycle != "active" && lifecycle != "archived" {
		return page, Fail("invalid_argument", 400, "分页或筛选参数无效")
	}
	f := ConversationFilter{AgentID: agent, Lifecycle: lifecycle, Limit: limit + 1}
	scope := "conversations:recent:" + workspace + ":" + actor + ":" + agent + ":" + lifecycle
	if rawCursor != "" {
		v, err := s.decodeCursor(rawCursor, scope)
		if err != nil {
			return page, err
		}
		if !ValidID(v.ID) || v.Time.IsZero() {
			return page, Fail("invalid_cursor", 400, "分页游标无效")
		}
		f.BeforeID = v.ID
		f.BeforeTime = v.Time
	}
	items, err := s.Store.Conversations(ctx, workspace, actor, f)
	if err != nil {
		return page, err
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		at := last.CreatedAt
		if last.LastMessageAt != nil {
			at = *last.LastMessageAt
		}
		next, err := s.encodeCursor(cursor{Scope: scope, Time: at, ID: last.ID})
		if err != nil {
			return page, err
		}
		page.NextCursor = &next
	}
	page.Items = items
	return page, nil
}
