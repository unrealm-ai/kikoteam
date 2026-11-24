package service

import (
	"context"
	"time"
)

type TriggerTaskResult struct {
	NotificationID string     `json:"notification_id"`
	TriggerKind    string     `json:"trigger_kind"`
	Outcome        string     `json:"outcome"`
	PublishedText  string     `json:"published_text"`
	PublishedAt    time.Time  `json:"published_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	FailureReason  string     `json:"failure_reason,omitempty"`
	CreatedAt      time.Time  `json:"-"`
}

type TriggerTaskResultFilter struct {
	NotificationID string
	BeforeID       string
	BeforeTime     time.Time
	Limit          int
}

func (d *Delegation) triggerTaskResults(ctx context.Context, scope DelegationScope, args DelegationArgs) (Page[TriggerTaskResult], error) {
	const pageSize = 5
	out := Page[TriggerTaskResult]{Items: []TriggerTaskResult{}}
	if !scope.Group {
		return out, Fail("trigger_task_group_required", 403, "请在收到该任务结果的群聊中查询")
	}
	exact := args.NotificationID != ""
	if exact && (!ValidID(args.NotificationID) || args.Cursor != "") {
		return out, Fail("invalid_argument", 400, "通知编号无效，或与分页游标同时提供")
	}
	filter := TriggerTaskResultFilter{NotificationID: args.NotificationID, Limit: pageSize + 1}
	cursorScope := "chat-trigger-task-results:" + scope.ConversationID + ":" + scope.ActorID
	if args.Cursor != "" {
		c, err := d.Manager.decodeCursor(args.Cursor, cursorScope)
		if err != nil {
			return out, err
		}
		filter.BeforeID, filter.BeforeTime = c.ID, c.Time
	}
	items, err := d.Store.ChatTriggerTaskResults(ctx, scope, filter)
	if err != nil {
		return out, err
	}
	if exact && len(items) == 0 {
		return out, Fail("resource_not_found", 404, "本群没有可查询的对应任务结果")
	}
	if len(items) > pageSize {
		items = items[:pageSize]
		last := items[len(items)-1]
		next, err := d.Manager.encodeCursor(cursor{Scope: cursorScope, ID: last.NotificationID, Time: last.CreatedAt})
		if err != nil {
			return out, err
		}
		out.NextCursor = &next
	}
	out.Items = append(out.Items, items...)
	return out, nil
}
