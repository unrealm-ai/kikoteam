package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// These DTOs are an allowlist, not the authenticated task event protocol.
type TaskDetailActivity struct {
	Kind       string    `json:"kind"`
	Text       string    `json:"text"`
	OccurredAt time.Time `json:"occurred_at"`
}

type TaskDetailArtifact struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type TaskDetailSnapshot struct {
	Title             string                  `json:"title"`
	Status            string                  `json:"status"`
	Permission        string                  `json:"permission"`
	CanAppend         bool                    `json:"can_append"`
	CanStop           bool                    `json:"can_stop"`
	CanContinue       bool                    `json:"can_continue"`
	StopVersion       string                  `json:"stop_version,omitempty"`
	Cursor            string                  `json:"cursor"`
	ActivityTruncated bool                    `json:"activity_truncated"`
	Activities        []TaskDetailActivity    `json:"activities"`
	Artifacts         []TaskDetailArtifact    `json:"artifacts"`
	InputReceipt      *TaskDetailInputReceipt `json:"input_receipt,omitempty"`
}

type TaskDetailInputReceipt struct {
	State string `json:"state"`
}

type TaskDetailMutation struct {
	Text        string `json:"text,omitempty"`
	Mode        string `json:"delivery_mode,omitempty"`
	StopVersion string `json:"stop_version,omitempty"`
}

type TaskDetailPageStore interface {
	ResolveTaskDetailGrant(context.Context, string) (TaskDetailGrant, error)
	TaskDetailProjection(context.Context, TaskDetailGrant) (TaskDetailSnapshot, error)
	TaskDetailDownload(context.Context, TaskDetailGrant, string) (Artifact, []byte, error)
}

type taskDetailTx interface {
	CheckTaskDetailGrant(string, bool) (TaskDetailGrant, error)
	TaskDetailUnknownTools() (bool, error)
	TaskDetailOperation(string, string, string, string) (bool, error)
	RecordTaskDetailInput(TaskDetailGrant, string, string, Input) error
	TaskDetailInputReceipt(string) (TaskDetailInputReceipt, error)
}

func (s *Service) TaskDetailPage(ctx context.Context, token, after string) (TaskDetailSnapshot, error) {
	store, grant, err := s.taskDetailAccess(ctx, token)
	if err != nil {
		return TaskDetailSnapshot{}, err
	}
	scope := taskDetailCursorScope(grant.ID)
	if after != "" {
		value, e := s.decodeCursor(after, scope)
		if e != nil || value.Kind != "task-detail-snapshot" {
			return TaskDetailSnapshot{}, Fail("invalid_cursor", 400, "任务详情游标无效")
		}
	}
	view, err := store.TaskDetailProjection(ctx, grant)
	if err != nil {
		return TaskDetailSnapshot{}, err
	}
	data, err := json.Marshal(view)
	if err != nil {
		return TaskDetailSnapshot{}, err
	}
	sum := sha256.Sum256(data)
	view.Cursor, err = s.encodeCursor(cursor{Scope: scope, Kind: "task-detail-snapshot", ID: hex.EncodeToString(sum[:])})
	return view, err
}

func taskDetailCursorScope(id string) string {
	sum := sha256.Sum256([]byte("task-detail-v1:" + id))
	return hex.EncodeToString(sum[:])
}

func (s *Service) PublicTaskArtifact(ctx context.Context, token, id string) (Artifact, []byte, error) {
	store, grant, err := s.taskDetailAccess(ctx, token)
	if err != nil {
		return Artifact{}, nil, err
	}
	if !ValidID(id) {
		return Artifact{}, nil, Fail("resource_not_found", 404, "产物不存在")
	}
	return store.TaskDetailDownload(ctx, grant, id)
}

func (s *Service) taskDetailAccess(ctx context.Context, token string) (TaskDetailPageStore, TaskDetailGrant, error) {
	store, ok := s.Store.(TaskDetailPageStore)
	if !ok {
		return nil, TaskDetailGrant{}, Fail("configuration_unusable", 503, "任务详情暂不可用")
	}
	grant, err := store.ResolveTaskDetailGrant(ctx, token)
	return store, grant, err
}

func TaskDetailWaiting(st SessionState) bool {
	if st.Session.Mode != "task" {
		return false
	}
	for _, action := range st.Session.Execution.RequiredActions {
		if action.Type == "human_confirmation" || action.Type == "work_item" {
			return true
		}
	}
	return false
}

func guardTaskDetailControl(st SessionState, detail bool) error {
	if detail && TaskDetailWaiting(st) {
		return Fail("task_waiting_for_action", 409, "任务正在等待人工确认或事项交接，不能从详情页停止或归档")
	}
	return nil
}

func TaskDetailControls(st SessionState, writable, unknown bool) TaskDetailSnapshot {
	view := TaskDetailSnapshot{Title: "任务详情", Permission: "read", Activities: []TaskDetailActivity{}, Artifacts: []TaskDetailArtifact{}}
	if st.Session.Title != nil && strings.TrimSpace(*st.Session.Title) != "" {
		view.Title = *st.Session.Title
	}
	view.Status, _ = delegatedTaskStatus(st.Session.PlanningState, st.Session.Lifecycle, st.Session.Execution)
	if writable {
		view.Permission = "write"
	}
	exec := st.Session.Execution
	switch {
	case view.Status == "archived" || view.Status == "planned" || view.Status == "todo_canceled":
		return view
	case TaskDetailWaiting(st) && st.StopRevision == st.AppliedStopRevision:
		view.Status = "waiting_for_action"
		view.CanAppend = writable && st.Session.AcceptsInput
	case exec.State == "blocked" || st.StopRevision != st.AppliedStopRevision:
		// Blocked and stopping have distinct UI states; neither can continue.
	case st.StopRevision > 0 && st.StopInputSeq == st.LastInputSeq:
		if exec.ActiveTurnID == nil && exec.BackgroundProcessCount != nil && *exec.BackgroundProcessCount == 0 && !unknown {
			view.Status = "stopped"
			view.CanContinue = writable && st.Session.AcceptsInput
			if view.CanContinue {
				view.StopVersion = strconv.FormatInt(st.StopRevision, 10)
			}
		} else {
			view.Status = "blocked"
		}
	default:
		view.CanAppend = writable && st.Session.AcceptsInput
		view.CanStop = writable && st.Session.AcceptsInput && view.Status != "idle"
	}
	return view
}

func (s *Service) MutateTaskDetail(ctx context.Context, token, action, key string, req TaskDetailMutation) (TaskDetailSnapshot, error) {
	_, grant, err := s.taskDetailAccess(ctx, token)
	if err != nil {
		return TaskDetailSnapshot{}, err
	}
	receipt, err := s.mutateTaskDetail(ctx, grant, action, key, req)
	if err != nil {
		return TaskDetailSnapshot{}, err
	}
	view, err := s.TaskDetailPage(ctx, token, "")
	view.InputReceipt = receipt
	return view, err
}

func (s *Service) mutateTaskDetail(ctx context.Context, g TaskDetailGrant, action, key string, req TaskDetailMutation) (*TaskDetailInputReceipt, error) {
	if !g.CanWrite() || g.SubjectID == "" || g.Channel == nil {
		return nil, Fail("task_detail_link_read_only", 403, "此链接无权修改任务")
	}
	d, err := taskDetailDedup(g, action, key, &req)
	if err != nil {
		return nil, err
	}
	var out *TaskDetailInputReceipt
	actor := ChannelActor{ID: g.SubjectID, Generation: g.SubjectGeneration}
	ctx = WithChannelActor(ctx, actor)
	err = s.Store.WithSession(ctx, Target{Workspace: g.WorkspaceID, Actor: g.SubjectID, Session: g.SessionID, Control: action == "stop", DedupLock: d.Kind + ":" + key}, func(tx SessionTx) error {
		out = nil
		extra, ok := tx.(taskDetailTx)
		if !ok {
			return Fail("configuration_unusable", 503, "任务详情暂不可用")
		}
		current, e := extra.CheckTaskDetailGrant(g.ID, true)
		if e != nil {
			return e
		}
		if current.SubjectID != g.SubjectID || current.SubjectGeneration != g.SubjectGeneration || current.SessionID != g.SessionID {
			return Fail("access_revoked", 403, "任务操作授权已变化")
		}
		if action == "stop" {
			return stopTaskDetail(tx, extra, current, key)
		}
		found, e := tx.FindDedup(d)
		if e != nil {
			return e
		}
		if found != nil {
			if found.Fingerprint != d.Fingerprint {
				return Fail("idempotency_conflict", 409, "此提交标识已用于其他内容")
			}
			v, e := extra.TaskDetailInputReceipt(found.InputID)
			if e == nil {
				out = &v
			}
			return e
		}
		v, e := s.appendTaskDetail(ctx, tx, extra, current, action, key, req, d)
		if e == nil {
			out = &v
		}
		return e
	})
	return out, err
}

func taskDetailDedup(g TaskDetailGrant, action, key string, req *TaskDetailMutation) (Dedup, error) {
	bad := Fail("invalid_argument", 400, "任务操作参数无效")
	if !validKey(key) {
		return Dedup{}, Fail("idempotency_key_required", 400, "任务操作需要有效去重键")
	}
	switch action {
	case "stop":
		if *req != (TaskDetailMutation{}) {
			return Dedup{}, bad
		}
	case "append", "continue":
		if !validMessageText(req.Text, nil, nil) || len(req.Text) > 10000 || (action == "append" && req.StopVersion != "") {
			return Dedup{}, bad
		}
		if action == "continue" {
			version, err := strconv.ParseInt(req.StopVersion, 10, 64)
			if err != nil || version < 1 || req.Mode != "" {
				return Dedup{}, bad
			}
		}
		mode, err := DeliveryMode(req.Mode)
		if err != nil {
			return Dedup{}, err
		}
		req.Mode = mode
	default:
		return Dedup{}, bad
	}
	fp, err := fingerprint(*req)
	return Dedup{Kind: "task_detail_" + action + ":" + g.ID, Target: g.SessionID, Key: key, Fingerprint: fp, SessionID: g.SessionID}, err
}

func stopTaskDetail(tx SessionTx, extra taskDetailTx, g TaskDetailGrant, key string) error {
	// The audit marker and stop effects share the transaction. A replay is
	// recognized even after another legitimate input opened a later round.
	inserted, err := extra.TaskDetailOperation(g.ID, "stop", key, "")
	if err != nil || !inserted {
		return err
	}
	st := tx.State()
	if err = guardTaskDetailControl(st, true); err != nil {
		return err
	}
	if !st.Session.AcceptsInput || st.Session.Execution.State == "blocked" {
		return Fail("task_stop_unavailable", 409, "当前任务状态不能从详情页停止或归档")
	}
	if st.StopRevision > st.AppliedStopRevision || st.Session.Execution.State == "idle" && (st.Session.Execution.BackgroundProcessCount == nil || *st.Session.Execution.BackgroundProcessCount == 0) {
		return nil
	}
	items, err := tx.CancelQueued("session_stopped")
	if err != nil {
		return err
	}
	for _, item := range items {
		if err = emitInput(tx, item); err != nil {
			return err
		}
	}
	if err = tx.Stop(false); err != nil {
		return err
	}
	return tx.SnapshotEvent()
}

func (s *Service) appendTaskDetail(ctx context.Context, tx SessionTx, extra taskDetailTx, g TaskDetailGrant, action, key string, req TaskDetailMutation, d Dedup) (TaskDetailInputReceipt, error) {
	st := tx.State()
	unknown, err := extra.TaskDetailUnknownTools()
	if err != nil {
		return TaskDetailInputReceipt{}, err
	}
	controls := TaskDetailControls(st, true, unknown)
	if action == "continue" && (!controls.CanContinue || req.StopVersion != controls.StopVersion) {
		return TaskDetailInputReceipt{}, Fail("task_continue_unavailable", 409, "任务尚未停稳或已有新一轮，请刷新")
	}
	if action == "append" && !controls.CanAppend {
		return TaskDetailInputReceipt{}, Fail("task_append_unavailable", 409, "当前任务状态不能补充，请刷新任务详情")
	}
	mode := req.Mode
	if TaskDetailWaiting(st) || action == "continue" {
		mode = "queue"
	}
	input, err := s.appendInputMode(ctx, tx, g.SubjectID, req.Text, mode, nil, nil, nil)
	if err != nil {
		return TaskDetailInputReceipt{}, err
	}
	d.InputID = input.ID
	if err = tx.RecordDedup(d); err != nil {
		return TaskDetailInputReceipt{}, err
	}
	if err = extra.RecordTaskDetailInput(g, action, key, input); err != nil {
		return TaskDetailInputReceipt{}, err
	}
	if err = tx.SnapshotEvent(); err != nil {
		return TaskDetailInputReceipt{}, err
	}
	return extra.TaskDetailInputReceipt(input.ID)
}
