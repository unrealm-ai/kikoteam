package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskSpaceAgent struct {
	AgentID        string `json:"agent_id"`
	Status         string `json:"status"`
	AdmissionEpoch int64  `json:"admission_epoch,string"`
}
type TaskSpace struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspace_id"`
	BusinessKey *string          `json:"business_key"`
	CreatedBy   string           `json:"created_by"`
	Lifecycle   string           `json:"lifecycle"`
	Version     int64            `json:"version,string"`
	CreatedAt   time.Time        `json:"created_at"`
	Agents      []TaskSpaceAgent `json:"agents"`
}
type TaskSpaceCreate struct {
	BusinessKey *string `json:"business_key"`
	AgentID     string  `json:"agent_id"`
}
type TaskSpaceGrant struct {
	Version int64 `json:"version,string"`
	Enabled bool  `json:"enabled"`
}
type TaskSpaceStore interface {
	CreateTaskSpace(context.Context, string, string, string, string, TaskSpaceCreate) (TaskSpace, bool, error)
	TaskSpace(context.Context, string, string, string) (TaskSpace, error)
	GrantTaskSpaceAgent(context.Context, string, string, string, string, TaskSpaceGrant) (TaskSpace, error)
}

func (s *Service) CreateTaskSpace(ctx context.Context, w, actor, key string, q TaskSpaceCreate) (TaskSpace, bool, error) {
	if !ValidID(w) || !ValidID(q.AgentID) || !validKey(key) {
		return TaskSpace{}, false, Fail("invalid_argument", 400, "工作区、员工或幂等键无效")
	}
	if q.BusinessKey != nil && (strings.TrimSpace(*q.BusinessKey) == "" || *q.BusinessKey != strings.TrimSpace(*q.BusinessKey) || utf8.RuneCountInString(*q.BusinessKey) > 200 || strings.ContainsRune(*q.BusinessKey, 0)) {
		return TaskSpace{}, false, Fail("invalid_business_key", 400, "业务键必须为非空原文且不超过200字")
	}
	store, ok := s.Store.(TaskSpaceStore)
	if !ok {
		return TaskSpace{}, false, Fail("task_space_unavailable", 503, "任务空间不可用")
	}
	fp, err := fingerprint(q)
	if err != nil {
		return TaskSpace{}, false, err
	}
	return store.CreateTaskSpace(ctx, w, actor, key, fp, q)
}
func (s *Service) TaskSpace(ctx context.Context, w, actor, id string) (TaskSpace, error) {
	if !ValidID(w) || !ValidID(id) {
		return TaskSpace{}, Fail("invalid_argument", 400, "任务空间标识无效")
	}
	store, ok := s.Store.(TaskSpaceStore)
	if !ok {
		return TaskSpace{}, Fail("task_space_unavailable", 503, "任务空间不可用")
	}
	return store.TaskSpace(ctx, w, actor, id)
}
func (s *Service) GrantTaskSpaceAgent(ctx context.Context, w, actor, id, agent string, q TaskSpaceGrant) (TaskSpace, error) {
	if !ValidID(w) || !ValidID(id) || !ValidID(agent) || q.Version < 1 {
		return TaskSpace{}, Fail("invalid_argument", 400, "任务空间授权参数无效")
	}
	store, ok := s.Store.(TaskSpaceStore)
	if !ok {
		return TaskSpace{}, Fail("task_space_unavailable", 503, "任务空间不可用")
	}
	return store.GrantTaskSpaceAgent(ctx, w, actor, id, agent, q)
}

func (s *Service) ListTaskSpaces(ctx context.Context, w, actor, raw string, limit int) (Page[TaskSpace], error) {
	out := Page[TaskSpace]{Items: []TaskSpace{}}
	n, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	scope := "task-spaces:" + w + ":" + actor
	var before time.Time
	id := ""
	if raw != "" {
		c, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if !ValidID(c.ID) || c.Time.IsZero() {
			return out, Fail("invalid_cursor", 400, "任务空间游标无效")
		}
		before, id = c.Time, c.ID
	}
	store, ok := s.Store.(interface {
		TaskSpaces(context.Context, string, string, time.Time, string, int) ([]TaskSpace, error)
	})
	if !ok {
		return out, Fail("task_space_unavailable", 503, "任务空间不可用")
	}
	items, err := store.TaskSpaces(ctx, w, actor, before, id, n+1)
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		last := items[n-1]
		next, e := s.encodeCursor(cursor{Scope: scope, ID: last.ID, Time: last.CreatedAt})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	return out, nil
}
