package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

type DelegationIdentity struct {
	SessionID    string `json:"session_id"`
	EngineID     string `json:"engine_id"`
	AssignmentID string `json:"assignment_id"`
	Generation   int64  `json:"generation,string"`
}
type DelegationScope struct {
	SourceFilesUnused                                         bool
	WebGroup                                                  *WebGroupExecution
	Group                                                     bool
	ReportOnly                                                bool
	ReportTaskID                                              string
	Channel                                                   *ChannelActor
	InputID                                                   string
	Identity                                                  DelegationIdentity
	WorkspaceID, ActorID, ConversationID, AgentID, Name, Role string
}
type DelegationStore interface {
	DelegationSource(context.Context, DelegationIdentity) (DelegationScope, error)
	DelegatedSession(context.Context, DelegationScope, string) (Session, error)
	LatestTurn(context.Context, string, string, string) (*Turn, error)
	DelegatedTurnSupplements(context.Context, DelegationScope, string, string) (TurnSupplements, error)
	ChatTriggerTaskResults(context.Context, DelegationScope, TriggerTaskResultFilter) ([]TriggerTaskResult, error)
	ChatWorkItem(context.Context, DelegationScope, string) (*ChatWorkItemView, error)
	AmendChatWorkItem(context.Context, DelegationScope, string, string, string, string, int64) (any, error)
}

func SignDelegation(secret string, v DelegationIdentity) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err = mac.Write(append([]byte("task-delegation:"), data...)); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func VerifyDelegation(secret, token string) (DelegationIdentity, error) {
	var out DelegationIdentity
	bad := Fail("unauthenticated", 401, "委派运行身份无效")
	p := strings.Split(token, ".")
	if len(secret) < 32 || len(p) != 2 || len(token) > 2048 {
		return out, bad
	}
	data, err := base64.RawURLEncoding.DecodeString(p[0])
	if err != nil || json.Unmarshal(data, &out) != nil || !ValidID(out.SessionID) || !ValidID(out.EngineID) || !ValidID(out.AssignmentID) || out.Generation < 1 {
		return out, bad
	}
	expected, err := SignDelegation(secret, out)
	if err != nil {
		return out, err
	}
	if !hmac.Equal([]byte(expected), []byte(token)) {
		return out, bad
	}
	return out, nil
}

type Delegation struct {
	Manager *Service
	Store   DelegationStore
	Secret  string
}
type DelegationArgs struct {
	Enabled           *bool             `json:"enabled,omitempty"`
	Schedule          *ChatScheduleRule `json:"schedule,omitempty"`
	ScheduleID        string            `json:"schedule_id,omitempty"`
	Cancel            bool              `json:"cancel,omitempty"`
	SourceFilesUnused bool              `json:"source_files_unused,omitempty"`

	AmendmentBasis   string   `json:"amendment_basis,omitempty"`
	ArtifactIDs      []string `json:"artifact_ids,omitempty"`
	TaskID           string   `json:"task_id,omitempty"`
	Content          string   `json:"content,omitempty"`
	AttachmentIDs    []string `json:"attachment_ids,omitempty"`
	AttachmentPaths  []string `json:"attachment_paths,omitempty"`
	PlanningState    string   `json:"planning_state,omitempty"`
	ExecutionState   string   `json:"execution_state,omitempty"`
	LatestTurnStatus string   `json:"latest_turn_status,omitempty"`
	InputID          string   `json:"input_id,omitempty"`
	NotificationID   string   `json:"notification_id,omitempty"`
	ExpectedVersion  int64    `json:"expected_version,string,omitempty"`
	Instruction      string   `json:"instruction,omitempty"`
	RequestKey       string   `json:"request_key,omitempty"`
	EmployeeID       string   `json:"employee_id,omitempty"`
	Title            *string  `json:"title,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	Text             string   `json:"text,omitempty"`
	TurnID           string   `json:"turn_id,omitempty"`
	Cursor           string   `json:"cursor,omitempty"`
	Limit            int      `json:"limit,omitempty"`
}

func (d *Delegation) Authorize(ctx context.Context, raw string) (DelegationScope, error) {
	v, err := VerifyDelegation(d.Secret, raw)
	if err != nil {
		return DelegationScope{}, err
	}
	return d.Store.DelegationSource(ctx, v)
}
func (d *Delegation) Call(ctx context.Context, scope DelegationScope, name string, args DelegationArgs) (any, error) {
	current, err := d.Store.DelegationSource(ctx, scope.Identity)
	if err != nil {
		return nil, err
	}
	if current.ActorID != scope.ActorID {
		return nil, Fail("access_revoked", 403, "当前输入主体已改变")
	}
	if current.InputID != scope.InputID && pinsDelegationInput(name, args) {
		return nil, Fail("delegation_input_changed", 409, "本次委派的来源输入已改变")
	}
	scope = current
	scope.SourceFilesUnused = args.SourceFilesUnused
	if scope.ReportOnly && name != "artifact_select" && name != "send_task_detail_link" {
		return nil, Fail("user_authorization_required", 403, "任务回报只能说明结果或询问用户，业务操作需要用户输入")
	}
	if scope.WebGroup != nil {
		ctx = WithWebGroupExecution(ctx, *scope.WebGroup)
	}
	if scope.Channel != nil {
		ctx = WithChannelActor(ctx, *scope.Channel)
	}
	switch name {
	case "send_task_detail_link":
		return d.taskDetailDispatch(ctx, scope, args.TaskID, args.Text)
	case "trigger_task_get_result":
		return d.triggerTaskResults(ctx, scope, args)
	case "artifact_select":
		artifacts, ok := d.Store.(ArtifactStore)
		if !ok {
			return nil, Fail("artifacts_unavailable", 503, "产物存储不可用")
		}
		return artifacts.SelectArtifacts(ctx, scope, args.ArtifactIDs)
	case "work_item_get_current", "work_item_amend_requirements":
		return d.callWorkItem(ctx, scope, name, args)
	case "create_scheduled_task", "list_scheduled_tasks", "update_scheduled_task":
		return d.chatSchedule(ctx, scope, name, args)
	case "create_todo":
		return d.planned(ctx, scope, name, args)
	case "start_todo", "cancel_todo", "supplement_todo":
		v, e := d.delegatedTaskTarget(ctx, scope, args)
		if e != nil {
			return nil, e
		}
		args.SessionID = v.ID
		return d.planned(ctx, scope, name, args)
	case "create_session":
		return d.createTask(ctx, scope, args)
	case "list_sessions":
		return d.Manager.ListSessions(ctx, scope.WorkspaceID, scope.ActorID, args.Cursor, SessionFilter{Conversation: scope.ConversationID, Mode: "task", SourceChat: scope.Identity.SessionID, Limit: args.Limit, PlanningState: args.PlanningState, ExecutionState: args.ExecutionState, LatestTurnStatus: args.LatestTurnStatus})
	}
	v, err := d.delegatedTaskTarget(ctx, scope, args)
	if err != nil {
		return nil, err
	}
	switch name {
	case "get_session":
		if v.PlanningState == "planned" || v.PlanningState == "canceled" {
			if st, ok := d.Store.(PlannedTaskStore); ok {
				planned, err := st.PlannedTaskRead(ctx, scope, v.ID)
				if err != nil {
					return nil, err
				}
				summary := summarizeDelegatedTask(planned.State, planned.Lifecycle, v.Execution, nil)
				planned.Summary = &summary
				return planned, nil
			}
		}
		return d.result(ctx, scope, v, args)
	case "send_message":
		return d.sendTaskMessage(ctx, scope, v, args)
	case "stop_session":
		return d.Manager.controlSession(ctx, scope.WorkspaceID, scope.ActorID, v.ID, false, &scope.Identity)
	default:
		return nil, Fail("invalid_argument", 400, "未知委派操作")
	}
}

type delegatedTaskResolver interface {
	DelegatedTaskSession(context.Context, DelegationScope, string) (Session, error)
}

func (d *Delegation) delegatedTaskTarget(ctx context.Context, scope DelegationScope, args DelegationArgs) (Session, error) {
	if args.TaskID != "" {
		if args.SessionID != "" || !ValidID(args.TaskID) {
			return Session{}, Fail("invalid_argument", 400, "task_id 无效或不能同时指定多个任务标识")
		}
		resolver, ok := d.Store.(delegatedTaskResolver)
		if !ok {
			return Session{}, Fail("dependency_unavailable", 503, "暂时无法查找任务")
		}
		return resolver.DelegatedTaskSession(ctx, scope, args.TaskID)
	}
	// Existing internal clients can still resolve their known execution session.
	if ValidID(args.SessionID) {
		return d.Store.DelegatedSession(ctx, scope, args.SessionID)
	}
	return Session{}, Fail("invalid_argument", 400, "task_id 无效")
}

func pinsDelegationInput(name string, args DelegationArgs) bool {
	return len(args.AttachmentIDs) > 0 || (strings.HasSuffix(name, "_todo") || strings.HasSuffix(name, "_scheduled_task")) || name == "create_session" || name == "send_message" || name == "artifact_select" || name == "trigger_task_get_result" || name == "send_task_detail_link"
}

func (d *Delegation) sendTaskMessage(ctx context.Context, scope DelegationScope, v Session, args DelegationArgs) (any, error) {
	if v.PlanningState == "canceled" {
		return nil, CanceledTodoError{}
	}
	if v.PlanningState == "planned" {
		return nil, Fail("task_not_started", 409, "未启动的待办请使用 supplement_todo")
	}
	if len(args.AttachmentPaths) == 0 {
		args.AttachmentPaths = nil
	}
	if (len(args.AttachmentPaths) > 0 || len(args.AttachmentIDs) > 0) && strings.TrimSpace(args.Text) == "" {
		args.Text = ""
	}
	payload := []any{"delegated_append", scope.Identity.SessionID, scope.InputID, v.ID, args.Text, args.AttachmentPaths}
	if len(args.AttachmentIDs) > 0 {
		payload = append(payload, args.AttachmentIDs)
	}
	key, e := fingerprint(payload)
	if e != nil {
		return nil, e
	}
	input, duplicate, e := d.Manager.submitInputVersion(ctx, scope.WorkspaceID, scope.ActorID, v.ID, key, args.Text, "", &scope, &v.InputRevision, args.AttachmentIDs, args.AttachmentPaths...)
	if e != nil {
		return nil, e
	}
	if r, ok := d.Store.(DelegationReceiptStore); ok {
		input, e = r.DelegatedInputReceipt(ctx, scope, v.ID, input.ID)
		if input.Receipt != nil {
			input.Receipt.Duplicate = duplicate
		}
	}
	return input, e
}

func (d *Delegation) callWorkItem(ctx context.Context, scope DelegationScope, name string, args DelegationArgs) (any, error) {
	if name == "work_item_get_current" {
		if !ValidID(args.TaskID) {
			return nil, Fail("invalid_argument", 400, "task_id 无效")
		}
		return d.readChatWorkItem(ctx, scope, args.TaskID)
	}
	content := strings.TrimSpace(args.Content)
	if !workText(content, 4000) || strings.ContainsRune(content, 0) {
		return nil, Fail("invalid_work_arguments", 400, "事项补充内容无效")
	}
	return d.writeChatWorkItem(ctx, scope, args.AmendmentBasis, content)
}

func (d *Delegation) createTask(ctx context.Context, scope DelegationScope, args DelegationArgs) (any, error) {
	if args.EmployeeID != "" && args.EmployeeID != scope.AgentID {
		return nil, Fail("delegation_target_not_allowed", 403, "首版仅支持委派给当前员工")
	}
	if !ValidID(scope.InputID) {
		return nil, Fail("delegation_input_changed", 409, "无法核实本次委派的来源输入")
	}
	req := CreateSession{AttachmentIDs: args.AttachmentIDs, AttachmentPaths: args.AttachmentPaths, ConversationID: scope.ConversationID, Mode: "task", Title: args.Title}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: args.Instruction}
	v, _, err := d.Manager.createSession(ctx, scope.WorkspaceID, scope.ActorID, "", req, &scope)
	return map[string]any{"task_id": v.TaskID, "session_id": v.ID, "input_id": v.InitialInputID, "execution": v.Execution}, err
}

func (d *Delegation) result(ctx context.Context, scope DelegationScope, v Session, args DelegationArgs) (any, error) {
	cursorScope := "delegated-result:" + scope.WorkspaceID + ":" + scope.ActorID + ":" + scope.Identity.SessionID + ":" + v.ID
	turn, inner := args.TurnID, ""
	if args.Cursor != "" {
		c, err := d.Manager.decodeCursor(args.Cursor, cursorScope)
		if err != nil {
			return nil, err
		}
		if c.Kind != "delegated_result" || args.TurnID != "" && args.TurnID != c.ID {
			return nil, Fail("invalid_cursor", 400, "结果游标范围不匹配")
		}
		turn = c.ID
		inner = c.Inner
	}
	var selected *Turn
	var err error
	if turn == "" {
		selected, err = d.Store.LatestTurn(ctx, scope.WorkspaceID, scope.ActorID, v.ID)
	} else if ValidID(turn) {
		var t Turn
		t, err = d.Manager.Store.Turn(ctx, scope.WorkspaceID, scope.ActorID, v.ID, turn)
		selected = &t
	} else {
		return nil, Fail("invalid_argument", 400, "turn_id 无效")
	}
	if err != nil {
		return nil, err
	}
	page := Page[json.RawMessage]{Items: []json.RawMessage{}}
	if selected != nil {
		events, e := d.Manager.ListEvents(ctx, scope.WorkspaceID, scope.ActorID, v.ID, selected.ID, "agent.message", inner, args.Limit)
		if e != nil {
			return nil, e
		}
		page = events.Page
		if page.NextCursor != nil {
			next, e := d.Manager.encodeCursor(cursor{Scope: cursorScope, Kind: "delegated_result", ID: selected.ID, Inner: *page.NextCursor})
			if e != nil {
				return nil, e
			}
			page.NextCursor = &next
		}
	}
	supplements := TurnSupplements{Receipts: []SupplementReceipt{}}
	if selected != nil {
		supplements, err = d.Store.DelegatedTurnSupplements(ctx, scope, v.ID, selected.ID)
		if err != nil {
			return nil, err
		}
	}
	var latestInput *Input
	if r, ok := d.Store.(DelegationReceiptStore); ok {
		i, e := r.DelegatedInputReceipt(ctx, scope, v.ID, args.InputID)
		if e != nil {
			return nil, e
		}
		if i.ID != "" {
			latestInput = &i
		}
	}
	out := map[string]any{"input_revision": decimal(v.InputRevision), "input": latestInput, "supplement_receipts": supplements.Receipts, "supplement_receipts_truncated": supplements.Truncated, "task_id": v.TaskID, "session_id": v.ID, "planning_state": v.PlanningState, "planning_version": decimal(v.PlanningVersion), "lifecycle": v.Lifecycle, "execution": v.Execution, "turn": selected, "messages": page.Items, "next_cursor": page.NextCursor}
	if selected != nil && scope.Channel != nil {
		if assets, ok := d.Store.(ArtifactStore); ok {
			candidates, e := assets.OfferArtifacts(ctx, scope, selected.ID)
			if e != nil {
				return nil, e
			}
			out["artifact_candidates"] = candidates
		}
	}
	out["summary"] = summarizeDelegatedTask(v.PlanningState, v.Lifecycle, v.Execution, selected)
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(data) > 512<<10 {
		return nil, Fail("result_too_large", 422, "结果超过 512 KiB，请减小 limit；单条过大时使用会话事件 API 读取")
	}
	return out, nil
}
