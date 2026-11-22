package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

type TaskSpaceHistoryIdentity struct {
	DelegationIdentity
	InputID string
}

func taskSpaceHistorySecret(secret, input string) string {
	digest := sha256.Sum256([]byte("task-space-history:" + secret + ":" + input))
	return hex.EncodeToString(digest[:])
}

func SignTaskSpaceHistory(secret string, id TaskSpaceHistoryIdentity) (string, error) {
	return SignDelegation(taskSpaceHistorySecret(secret, id.InputID), id.DelegationIdentity)
}

func VerifyTaskSpaceHistory(secret, input, token string) (TaskSpaceHistoryIdentity, error) {
	if len(secret) < 32 || !ValidID(input) {
		return TaskSpaceHistoryIdentity{}, Fail("unauthenticated", 401, "任务历史执行身份无效")
	}
	id, err := VerifyDelegation(taskSpaceHistorySecret(secret, input), token)
	return TaskSpaceHistoryIdentity{DelegationIdentity: id, InputID: input}, err
}

type TaskSpaceHistoryArgs struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type TaskSpaceHistoryDetailArgs struct {
	TaskID string `json:"task_id"`
}

type TaskSpaceHistorySummary struct {
	TaskID         string            `json:"task_id"`
	TaskSpaceID    *string           `json:"task_space_id"`
	Title          *string           `json:"title"`
	Lifecycle      string            `json:"lifecycle"`
	PlanningState  string            `json:"planning_state"`
	ExecutionState string            `json:"execution_state"`
	BlockReason    *string           `json:"block_reason"`
	LatestInput    *TaskInputSummary `json:"latest_input"`
	LatestTurn     *TaskTurnSummary  `json:"latest_turn"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (t Task) TaskSpaceHistorySummary() TaskSpaceHistorySummary {
	return TaskSpaceHistorySummary{
		TaskID: t.TaskID, TaskSpaceID: t.TaskSpaceID, Title: t.Title, Lifecycle: t.Lifecycle,
		PlanningState: t.PlanningState, ExecutionState: t.Execution.State, BlockReason: t.Execution.BlockReason,
		LatestInput: t.LatestInput, LatestTurn: t.LatestTurn, CreatedAt: t.CreatedAt,
	}
}

type TaskSpaceHistoryNote struct {
	TurnID    string    `json:"turn_id"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskSpaceHistoryDetail struct {
	Task            TaskSpaceHistorySummary `json:"task"`
	ProgressSummary *TaskSpaceHistoryNote   `json:"progress_summary"`
	ResultSummary   *TaskSpaceHistoryNote   `json:"result_summary"`
}

type TaskSpaceHistorySaveArgs struct {
	Progress string `json:"progress,omitempty"`
	Result   string `json:"result,omitempty"`
}

type TaskSpaceHistorySaveReceipt struct {
	Saved bool `json:"saved"`
}

type TaskSpaceHistoryStore interface {
	TaskSpaceHistoryTasks(context.Context, TaskSpaceHistoryIdentity, TaskFilter) ([]Task, error)
	TaskSpaceHistoryDetail(context.Context, TaskSpaceHistoryIdentity, string) (TaskSpaceHistoryDetail, error)
	SaveTaskSpaceSummary(context.Context, TaskSpaceHistoryIdentity, TaskSpaceHistorySaveArgs) error
}

type TaskSpaceHistory struct {
	Store        TaskSpaceHistoryStore
	CursorSecret []byte
}

func (h *TaskSpaceHistory) List(ctx context.Context, id TaskSpaceHistoryIdentity, args TaskSpaceHistoryArgs) (Page[TaskSpaceHistorySummary], error) {
	out := Page[TaskSpaceHistorySummary]{Items: []TaskSpaceHistorySummary{}}
	n, err := taskLimit(args.Limit)
	if err != nil {
		return out, err
	}
	s := Service{CursorSecret: h.CursorSecret}
	scope := "task-space-history:list:" + id.InputID
	f := TaskFilter{Limit: n + 1}
	if args.Cursor != "" {
		c, e := s.decodeCursor(args.Cursor, scope)
		if e != nil {
			return out, e
		}
		if !ValidID(c.ID) || c.Time.IsZero() {
			return out, Fail("invalid_cursor", 400, "任务历史游标无效")
		}
		f.BeforeID, f.BeforeTime = c.ID, c.Time
	}
	items, err := h.Store.TaskSpaceHistoryTasks(ctx, id, f)
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		last := items[n-1]
		next, e := s.encodeCursor(cursor{Scope: scope, ID: last.TaskID, Time: last.CreatedAt})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	for _, task := range items {
		out.Items = append(out.Items, task.TaskSpaceHistorySummary())
	}
	return out, nil
}

func (h *TaskSpaceHistory) Get(ctx context.Context, id TaskSpaceHistoryIdentity, args TaskSpaceHistoryDetailArgs) (TaskSpaceHistoryDetail, error) {
	var out TaskSpaceHistoryDetail
	if !ValidID(args.TaskID) {
		return out, Fail("resource_not_found", 404, "任务不存在")
	}
	return h.Store.TaskSpaceHistoryDetail(ctx, id, args.TaskID)
}

func (h *TaskSpaceHistory) Save(ctx context.Context, id TaskSpaceHistoryIdentity, args TaskSpaceHistorySaveArgs) (TaskSpaceHistorySaveReceipt, error) {
	args.Progress, args.Result = strings.TrimSpace(args.Progress), strings.TrimSpace(args.Result)
	if args == (TaskSpaceHistorySaveArgs{}) || !validSpaceSummary(args.Progress, 600) || !validSpaceSummary(args.Result, 1600) {
		return TaskSpaceHistorySaveReceipt{}, Fail("invalid_task_space_summary", 400, "请提供进度摘要或结果摘要，分别最多 600 和 1600 字")
	}
	err := h.Store.SaveTaskSpaceSummary(ctx, id, args)
	return TaskSpaceHistorySaveReceipt{Saved: err == nil}, err
}

func validSpaceSummary(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}
