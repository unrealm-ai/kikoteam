package service

import (
	"context"
	"slices"
	"time"

	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type WorkOverview struct {
	LinkedTaskIDs []string  `json:"linked_task_ids"`
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Goal          string    `json:"goal"`
	Criteria      string    `json:"criteria"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason"`
	Origin        string    `json:"origin_employee"`
	Owner         string    `json:"current_employee"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type WorkDelivery struct {
	ConversationID string `json:"conversation_id"`
	Destination    string `json:"destination"`
	Status         string `json:"status"`
}
type WorkHistory struct {
	ID         int64          `json:"id,string"`
	Kind       string         `json:"kind"`
	Employee   string         `json:"employee"`
	Target     string         `json:"target_employee"`
	Summary    string         `json:"summary"`
	CreatedAt  time.Time      `json:"created_at"`
	Deliveries []WorkDelivery `json:"deliveries"`
}
type WorkDetail struct {
	WorkOverview
	Events []WorkHistory `json:"events"`
	More   bool          `json:"has_more"`
}
type ReceiverPolicy struct {
	Version int64              `json:"version,string"`
	Scope   platform.WorkScope `json:"scope"`
}
type WorkBoardStore interface {
	WorkCounts(context.Context, string, string, string, string) (map[string]int, error)
	WorkList(context.Context, string, string, string, string, time.Time, string, int) ([]WorkOverview, error)
	WorkDetail(context.Context, string, string, string, int64, int) (WorkDetail, error)
	ReceiverPolicy(context.Context, string, string, string, *ReceiverPolicy) (ReceiverPolicy, error)
}

func (s *Service) ListWork(ctx context.Context, w, actor, status, employee, raw string, n int, counts bool) (Page[WorkOverview], error) {
	out := Page[WorkOverview]{Items: []WorkOverview{}}
	if !ValidID(w) || employee != "" && !ValidID(employee) || !slices.Contains([]string{"", "working", "blocked", "awaiting_acceptance", "cancelled", "completed", "failed"}, status) {
		return out, Fail("invalid_argument", 400, "事项筛选无效")
	}
	n, err := pageLimit(n)
	if err != nil {
		return out, err
	}
	scope := "work:" + w + ":" + actor + ":" + status + ":" + employee
	var before time.Time
	var id string
	if raw != "" {
		c, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		before, id = c.Time, c.ID
		if !ValidID(id) || before.IsZero() {
			return out, Fail("invalid_cursor", 400, "事项游标无效")
		}
	}
	st, ok := s.Store.(WorkBoardStore)
	if !ok {
		return out, Fail("dependency_unavailable", 503, "事项服务不可用")
	}
	items, e := st.WorkList(ctx, w, actor, status, employee, before, id, n+1)
	if e != nil {
		return out, e
	}
	if len(items) > n {
		items = items[:n]
		last := items[n-1]
		next, e := s.encodeCursor(cursor{Scope: scope, Time: last.CreatedAt, ID: last.ID})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	if counts {
		out.StatusCounts, e = st.WorkCounts(ctx, w, actor, status, employee)
	}
	return out, e
}
