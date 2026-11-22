package service

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
)

func (s *Service) listEventsBackward(ctx context.Context, w, actor, id, turn, types, raw string, limit int) (EventPage, error) {
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
	before, high := int64(0), int64(-1)
	if raw != "" {
		v, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if v.Kind != "history_backward" || v.Seq < 1 || v.High < v.Seq {
			return out, Fail("invalid_cursor", 400, "历史游标无效")
		}
		before, high = v.Seq, v.High
	}
	items, high, err := s.Store.Events(ctx, w, actor, id, EventFilter{Turn: turn, Type: types, High: high, Before: before, Backward: true, Limit: n + 1})
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		var boundary struct {
			Seq string `json:"seq"`
		}
		if err = json.Unmarshal(items[n-1], &boundary); err != nil {
			return out, err
		}
		seq, e := strconv.ParseInt(boundary.Seq, 10, 64)
		if e != nil {
			return out, e
		}
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: "history_backward", Seq: seq, High: high})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	slices.Reverse(items)
	out.Items = items
	resume, err := s.encodeCursor(cursor{Scope: scope, Kind: "stream", Seq: high})
	if err != nil {
		return out, err
	}
	out.ResumeCursor = &resume
	return out, nil
}
