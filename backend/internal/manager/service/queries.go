package service

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func pageLimit(n int) (int, error) {
	if n == 0 {
		return 50, nil
	}
	if n < 1 || n > 200 {
		return 0, Fail("invalid_argument", 400, "分页大小无效")
	}
	return n, nil
}
func (s *Service) ListSessions(ctx context.Context, w, actor, raw string, f SessionFilter) (Page[Session], error) {
	out := Page[Session]{Items: []Session{}}
	n, err := pageLimit(f.Limit)
	if err != nil {
		return out, err
	}
	if f.TaskSpace != "" && !ValidID(f.TaskSpace) || f.Conversation != "" && !ValidID(f.Conversation) || f.Agent != "" && !ValidID(f.Agent) || f.Mode != "" && f.Mode != "chat" && f.Mode != "task" || f.Lifecycle != "" && f.Lifecycle != "active" && f.Lifecycle != "archived" {
		return out, Fail("invalid_argument", 400, "筛选参数无效")
	}
	if !slices.Contains([]string{"", "planned", "started", "canceled"}, f.PlanningState) || !slices.Contains([]string{"", "idle", "queued", "running", "stopping", "blocked"}, f.ExecutionState) || !slices.Contains([]string{"", "running", "completed", "failed", "canceled", "interrupted"}, f.LatestTurnStatus) {
		return out, Fail("invalid_argument", 400, "任务状态筛选无效")
	}
	scope := "sessions:" + w + ":" + actor + ":" + f.Conversation + ":" + f.Agent + ":" + f.Mode + ":" + f.Lifecycle + ":" + f.SourceChat + ":" + f.TaskSpace + ":" + f.PlanningState + ":" + f.ExecutionState + ":" + f.LatestTurnStatus
	if raw != "" {
		v, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if !ValidID(v.ID) || v.Time.IsZero() {
			return out, Fail("invalid_cursor", 400, "分页游标无效")
		}
		f.BeforeID = v.ID
		f.BeforeTime = v.Time
	}
	f.Limit = n + 1
	items, err := s.Store.Sessions(ctx, w, actor, f)
	if err != nil {
		return out, err
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
	return out, nil
}

func (s *Service) sequenceCursor(raw, scope string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := s.decodeCursor(raw, scope)
	if err != nil {
		return 0, err
	}
	if v.Kind != "sequence" || v.Seq < 1 {
		return 0, Fail("invalid_cursor", 400, "分页游标无效")
	}
	return v.Seq, nil
}
func (s *Service) ListInputs(ctx context.Context, w, actor, id, status, raw string, limit int) (Page[Input], error) {
	out := Page[Input]{Items: []Input{}}
	n, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	if status != "" && !slices.Contains([]string{"queued", "started", "processed", "canceled", "rejected"}, status) {
		return out, Fail("invalid_argument", 400, "输入状态无效")
	}
	scope := "inputs:" + w + ":" + actor + ":" + id + ":" + status
	after, err := s.sequenceCursor(raw, scope)
	if err != nil {
		return out, err
	}
	items, err := s.Store.Inputs(ctx, w, actor, id, status, after, n+1)
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: "sequence", Seq: items[n-1].Seq})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	return out, nil
}
func (s *Service) ListTurns(ctx context.Context, w, actor, id, status, raw string, limit int) (Page[Turn], error) {
	out := Page[Turn]{Items: []Turn{}}
	n, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	if status != "" && !slices.Contains([]string{"running", "stopping", "completed", "failed", "canceled", "interrupted"}, status) {
		return out, Fail("invalid_argument", 400, "轮次状态无效")
	}
	scope := "turns:" + w + ":" + actor + ":" + id + ":" + status
	after, err := s.sequenceCursor(raw, scope)
	if err != nil {
		return out, err
	}
	items, err := s.Store.Turns(ctx, w, actor, id, status, after, n+1)
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: "sequence", Seq: items[n-1].TurnNo})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	return out, nil
}

func eventScope(w, actor, id, turn, types string) string {
	return "events:" + w + ":" + actor + ":" + id + ":" + turn + ":" + types
}
func normalizeTypes(types string) (string, error) {
	if types == "" {
		return "", nil
	}
	parts := strings.Split(types, ",")
	if len(parts) > 20 {
		return "", Fail("invalid_argument", 400, "事件类型过多")
	}
	sort.Strings(parts)
	result := []string{}
	for _, p := range parts {
		if !slices.Contains([]string{"work_item.notice", "user.message", "agent.message", "agent.thinking", "agent.tool_use", "agent.tool_result", "input.updated", "turn.updated", "session.updated", "session.requires_action", "session.error"}, p) {
			return "", Fail("invalid_argument", 400, "事件类型无效")
		}
		if len(result) == 0 || result[len(result)-1] != p {
			result = append(result, p)
		}
	}
	return strings.Join(result, ","), nil
}

func (s *Service) ListEvents(ctx context.Context, w, actor, id, turn, types, raw string, limit int, direction ...string) (EventPage, error) {
	if len(direction) > 0 && direction[0] != "" && direction[0] != "forward" {
		if direction[0] == "backward" {
			return s.listEventsBackward(ctx, w, actor, id, turn, types, raw, limit)
		}
		return EventPage{}, Fail("invalid_argument", 400, "历史方向无效")
	}
	out := EventPage{Page: Page[json.RawMessage]{Items: []json.RawMessage{}}}
	n, err := pageLimit(limit)
	if err != nil {
		return out, err
	}
	types, err = normalizeTypes(types)
	if err != nil {
		return out, err
	}
	if turn != "" && !ValidID(turn) {
		return out, Fail("invalid_argument", 400, "轮次标识无效")
	}
	scope := eventScope(w, actor, id, turn, types)
	after, high := int64(0), int64(-1)
	if raw != "" {
		v, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if v.Kind != "history" || v.Seq < 0 || v.High < v.Seq {
			return out, Fail("invalid_cursor", 400, "历史游标无效")
		}
		after = v.Seq
		high = v.High
	}
	items, high, err := s.Store.Events(ctx, w, actor, id, EventFilter{Turn: turn, Type: types, After: after, High: high, Limit: n + 1})
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		var last struct {
			Seq string `json:"seq"`
		}
		if err = json.Unmarshal(items[n-1], &last); err != nil {
			return out, err
		}
		seq, e := strconv.ParseInt(last.Seq, 10, 64)
		if e != nil {
			return out, e
		}
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: "history", Seq: seq, High: high})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	if out.NextCursor == nil {
		var resume string
		resume, err = s.encodeCursor(cursor{Scope: scope, Kind: "stream", Seq: high})
		if err == nil {
			out.ResumeCursor = &resume
		}
	}
	return out, err
}

// StreamEvents returns reliable records only. The caller short-polls this same
// database history; a dropped transport never cancels the underlying Turn.
func (s *Service) StreamEvents(ctx context.Context, w, actor, id, turn, types, after string) ([]json.RawMessage, string, error) {
	types, err := normalizeTypes(types)
	if err != nil {
		return nil, "", err
	}
	return s.readEventStream(eventScope(w, actor, id, turn, types), after, func(f EventFilter) ([]json.RawMessage, int64, error) {
		f.Turn, f.Type = turn, types
		return s.Store.Events(ctx, w, actor, id, f)
	})
}

func (s *Service) readEventStream(scope, after string, load loadEvents) ([]json.RawMessage, string, error) {
	seq := int64(0)
	if after != "" {
		v, e := s.decodeCursor(after, scope)
		if e != nil {
			return nil, "", e
		}
		if v.Kind != "stream" || v.Seq < 0 {
			return nil, "", Fail("invalid_cursor", 400, "实时游标无效")
		}
		seq = v.Seq
	}
	items, high, err := load(EventFilter{After: seq, High: -1, Limit: 200})
	if err != nil {
		return nil, "", err
	}
	if len(items) == 200 {
		var last struct {
			Seq string `json:"seq"`
		}
		if err = json.Unmarshal(items[len(items)-1], &last); err != nil {
			return nil, "", err
		}
		high, err = strconv.ParseInt(last.Seq, 10, 64)
		if err != nil {
			return nil, "", err
		}
	}
	next, err := s.encodeCursor(cursor{Scope: scope, Kind: "stream", Seq: high})
	return items, next, err
}

func (s *Service) EventID(w, actor, id, turn, types string, seq int64) (string, error) {
	normalized, err := normalizeTypes(types)
	if err != nil {
		return "", err
	}
	return s.encodeCursor(cursor{Scope: eventScope(w, actor, id, turn, normalized), Kind: "stream", Seq: seq})
}
