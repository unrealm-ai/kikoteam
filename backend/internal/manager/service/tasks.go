package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Tasks are read projections of existing Task/Session facts. All commands still
// enter the Session use cases; this projection owns no execution state.
type Task struct {
	ReportedProgress *ReportedProgress     `json:"reported_progress"`
	WorkItemID       *string               `json:"work_item_id,omitempty"`
	PlanningState    string                `json:"planning_state"`
	PlanningVersion  int64                 `json:"planning_version,string"`
	TaskSpaceID      *string               `json:"task_space_id,omitempty"`
	TaskID           string                `json:"task_id"`
	SessionID        string                `json:"session_id"`
	ConversationID   string                `json:"conversation_id,omitempty"`
	Title            *string               `json:"title"`
	Agent            TaskAgent             `json:"agent"`
	Source           TaskSource            `json:"source"`
	Lifecycle        string                `json:"lifecycle"`
	Execution        Execution             `json:"execution"`
	LatestInput      *TaskInputSummary     `json:"latest_input"`
	LatestTurn       *TaskTurnSummary      `json:"latest_turn"`
	CreatedAt        time.Time             `json:"created_at"`
	Revision         int64                 `json:"revision,string"`
	AcceptsInput     bool                  `json:"accepts_input"`
	InputBlockReason *string               `json:"input_block_reason"`
	Actions          map[string]TaskAction `json:"actions"`
}
type TaskInputSummary struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	DeliveryMode string  `json:"delivery_mode"`
	TurnID       *string `json:"turn_id"`
}
type TaskTurnSummary struct {
	ID                    string     `json:"id"`
	TurnNo                int64      `json:"turn_no,string"`
	Status                string     `json:"status"`
	StartedAt             time.Time  `json:"started_at"`
	FinishedAt            *time.Time `json:"finished_at"`
	HasUnknownToolResults bool       `json:"has_unknown_tool_results"`
}
type TaskAgent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}
type TaskSource struct {
	Kind          string  `json:"kind"`
	ChatSessionID *string `json:"chat_session_id"`
	ChatInputID   *string `json:"chat_input_id"`
}
type TaskAction struct {
	Allowed bool    `json:"allowed"`
	Reason  *string `json:"reason"`
}
type TaskDetail struct {
	Task
	Progress     *TaskProgress        `json:"progress,omitempty"`
	Artifacts    []TaskDetailArtifact `json:"artifacts,omitempty"`
	InitialInput *Input               `json:"initial_input"`
	LatestResult *TaskResultPreview   `json:"latest_result"`
}
type TaskResultPreview struct {
	TurnID    string `json:"turn_id"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}
type TaskFilter struct {
	WorkItem                                                                      string
	IncludeCounts                                                                 bool
	TaskSpace                                                                     string
	Q, Agent, Conversation, ExecutionState, LatestTurnStatus, Lifecycle, BeforeID string
	BeforeTime                                                                    time.Time
	Limit                                                                         int
}

// SupplementReceipt identifies a user supplement that joined this Turn.
// Native context evidence does not establish semantic application.
type SupplementReceipt struct {
	InputID      string  `json:"input_id"`
	InputSeq     int64   `json:"input_seq,string"`
	ContextState *string `json:"context_state"`
}

type TurnSupplements struct {
	Receipts  []SupplementReceipt `json:"supplement_receipts"`
	Truncated bool                `json:"supplement_receipts_truncated"`
}

type TaskReportEntry struct {
	TurnSupplements
	ReportID       *string         `json:"report_id"`
	SourceInputID  string          `json:"source_input_id"`
	SourceInputSeq int64           `json:"source_input_seq,string"`
	SourceTurnID   *string         `json:"source_turn_id"`
	Outcome        string          `json:"outcome"`
	Status         string          `json:"status"`
	Reason         *string         `json:"reason"`
	Target         json.RawMessage `json:"target"`
	CreatedAt      *time.Time      `json:"created_at"`
}
type ReportFilter struct {
	Turn, Input string
	Before      int64
	Limit       int
}
type TaskStore interface {
	TaskCounts(context.Context, string, string, TaskFilter) (map[string]int, error)
	Tasks(context.Context, string, string, TaskFilter) ([]Task, error)
	Task(context.Context, string, string, string) (TaskDetail, error)
	TaskReports(context.Context, string, string, string, ReportFilter) ([]TaskReportEntry, error)
}

type TaskArtifactStore interface {
	TaskArtifactDownload(ctx context.Context, workspace, actor, taskID, artifactID string) (Artifact, []byte, error)
}

func taskLimit(n int) (int, error) {
	if n == 0 {
		return 20, nil
	}
	if n < 1 || n > 100 {
		return 0, Fail("invalid_argument", 400, "分页大小须为 1–100")
	}
	return n, nil
}
func (s *Service) ListTasks(ctx context.Context, w, actor, raw string, f TaskFilter) (Page[Task], error) {
	out := Page[Task]{Items: []Task{}}
	n, err := taskLimit(f.Limit)
	if err != nil {
		return out, err
	}
	f.Q = strings.TrimSpace(f.Q)
	if f.WorkItem != "" && !ValidID(f.WorkItem) || f.TaskSpace != "" && !ValidID(f.TaskSpace) || utf8.RuneCountInString(f.Q) > 200 || (f.Agent != "" && !ValidID(f.Agent)) || (f.Conversation != "" && !ValidID(f.Conversation)) || !slices.Contains([]string{"", "idle", "queued", "running", "stopping", "blocked"}, f.ExecutionState) || !slices.Contains([]string{"", "running", "stopping", "completed", "failed", "canceled", "interrupted"}, f.LatestTurnStatus) || !slices.Contains([]string{"", "active", "archived"}, f.Lifecycle) {
		return out, Fail("invalid_argument", 400, "任务筛选参数无效")
	}
	parts := []string{w, actor, f.Q, f.Agent, f.Conversation, f.ExecutionState, f.LatestTurnStatus, f.Lifecycle, f.TaskSpace}
	if f.WorkItem != "" {
		parts = append(parts, f.WorkItem)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return out, err
	}
	scope := "tasks:" + string(encoded)
	if raw != "" {
		c, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if !ValidID(c.ID) || c.Time.IsZero() {
			return out, Fail("invalid_cursor", 400, "分页游标无效")
		}
		f.BeforeID = c.ID
		f.BeforeTime = c.Time
	}
	f.Limit = n + 1
	items, err := s.Store.Tasks(ctx, w, actor, f)
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
	out.Items = items
	if f.IncludeCounts {
		out.StatusCounts, err = s.Store.TaskCounts(ctx, w, actor, f)
	}
	return out, err
}
func (s *Service) GetTask(ctx context.Context, w, actor, id string) (TaskDetail, error) {
	if !ValidID(id) {
		return TaskDetail{}, Fail("resource_not_found", 404, "任务不存在")
	}
	return s.Store.Task(ctx, w, actor, id)
}

func (s *Service) TaskArtifact(ctx context.Context, w, actor, taskID, artifactID string) (Artifact, []byte, error) {
	if !ValidID(taskID) || !ValidID(artifactID) {
		return Artifact{}, nil, Fail("resource_not_found", 404, "产物不存在")
	}
	store, ok := s.Store.(TaskArtifactStore)
	if !ok {
		return Artifact{}, nil, Fail("artifacts_unavailable", 503, "产物暂不可读")
	}
	return store.TaskArtifactDownload(ctx, w, actor, taskID, artifactID)
}
func (s *Service) ListTaskReports(ctx context.Context, w, actor, id, raw string, f ReportFilter) (Page[TaskReportEntry], error) {
	out := Page[TaskReportEntry]{Items: []TaskReportEntry{}}
	if !ValidID(id) {
		return out, Fail("resource_not_found", 404, "任务不存在")
	}
	if (f.Turn != "" && !ValidID(f.Turn)) || (f.Input != "" && !ValidID(f.Input)) || (f.Turn != "" && f.Input != "") {
		return out, Fail("invalid_argument", 400, "轮次与输入筛选无效")
	}
	n, err := taskLimit(f.Limit)
	if err != nil {
		return out, err
	}
	scope := "task-reports:" + w + ":" + actor + ":" + id + ":" + f.Turn + ":" + f.Input
	if raw != "" {
		c, e := s.decodeCursor(raw, scope)
		if e != nil {
			return out, e
		}
		if c.Kind != "sequence" || c.Seq < 1 {
			return out, Fail("invalid_cursor", 400, "回报游标无效")
		}
		f.Before = c.Seq
	}
	f.Limit = n + 1
	items, err := s.Store.TaskReports(ctx, w, actor, id, f)
	if err != nil {
		return out, err
	}
	if len(items) > n {
		items = items[:n]
		next, e := s.encodeCursor(cursor{Scope: scope, Kind: "sequence", Seq: items[n-1].SourceInputSeq})
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	out.Items = items
	return out, nil
}
