package service

import "context"

// PlannedTask retains a stable Task and inert Session, but no execution Input,
// native runtime, model snapshot or sandbox exists until explicit start.
type PlannedTask struct {
	Attachments      []Attachment          `json:"attachments,omitempty"`
	Lifecycle        string                `json:"lifecycle"`
	Application      string                `json:"application"`
	AvailableActions map[string]bool       `json:"available_actions"`
	TaskID           string                `json:"task_id"`
	SessionID        string                `json:"session_id"`
	State            string                `json:"planning_state"`
	Version          int64                 `json:"planning_version,string"`
	Instruction      string                `json:"instruction"`
	InitialInputID   *string               `json:"initial_input_id"`
	Duplicate        bool                  `json:"duplicate"`
	Summary          *DelegatedTaskSummary `json:"summary,omitempty"`
}
type PlannedTaskRequest struct {
	AttachmentIDs      []string                                                   `json:"attachment_ids,omitempty"`
	AttachmentPaths    []string                                                   `json:"attachment_paths,omitempty"`
	ResolveAttachments func(Conversation, string, []string) ([]Attachment, error) `json:"-"`
	Action             string
	RequestKey         string
	Fingerprint        string
	SessionID          string
	ExpectedVersion    int64
	Instruction        string
	Title              *string
}
type PlannedTaskStore interface {
	PlannedTaskCommand(context.Context, DelegationScope, PlannedTaskRequest) (PlannedTask, error)
	PlannedTaskRead(context.Context, DelegationScope, string) (PlannedTask, error)
}

func (d *Delegation) planned(ctx context.Context, scope DelegationScope, name string, args DelegationArgs) (any, error) {
	if name == "create_todo" && (args.TaskID != "" || args.SessionID != "" || args.ExpectedVersion != 0) {
		return nil, Fail("invalid_argument", 400, "新待办不能指定已有会话或版本")
	}
	if !validKey(args.RequestKey) || name != "create_todo" && (!ValidID(args.SessionID) || args.ExpectedVersion < 1) || (name == "create_todo" || name == "supplement_todo") && !validText(args.Instruction) || !validAttachmentSelection(args.AttachmentPaths, args.AttachmentIDs, true) || !validTitle(args.Title) {
		return nil, Fail("invalid_argument", 400, "待办参数无效")
	}
	st, ok := d.Store.(PlannedTaskStore)
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "待办不可用")
	}
	if len(args.AttachmentIDs) > 0 && name != "create_todo" && name != "supplement_todo" {
		return nil, Fail("invalid_argument", 400, "仅新建和补充待办可添加附件")
	}
	q := PlannedTaskRequest{AttachmentIDs: args.AttachmentIDs, AttachmentPaths: args.AttachmentPaths, ResolveAttachments: func(c Conversation, actor string, paths []string) ([]Attachment, error) {
		return d.Manager.resolveInputAttachments(ctx, c, actor, paths)
	}, Action: name, RequestKey: args.RequestKey, SessionID: args.SessionID, ExpectedVersion: args.ExpectedVersion, Instruction: args.Instruction, Title: args.Title}
	hash, err := fingerprint(q)
	if err != nil {
		return nil, err
	}
	q.Fingerprint = hash
	return st.PlannedTaskCommand(ctx, scope, q)
}

// CanceledTodoError distinguishes an inert canceled todo without changing the
// task_not_started error code surfaced by callers.
type CanceledTodoError struct{}

func (CanceledTodoError) Error() string   { return "待办已取消，未启动任何执行" }
func (e CanceledTodoError) Unwrap() error { return Fail("task_not_started", 409, e.Error()) }

type DelegationReceiptStore interface {
	DelegatedInputReceipt(context.Context, DelegationScope, string, string) (Input, error)
}
