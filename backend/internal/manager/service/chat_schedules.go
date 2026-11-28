package service

import (
	"context"
	"errors"
	"time"
)

type ChatSchedule struct {
	ID          string                   `json:"schedule_id"`
	Status      string                   `json:"status"`
	Instruction string                   `json:"instruction"`
	Title       *string                  `json:"title,omitempty"`
	Rule        ChatScheduleRule         `json:"schedule"`
	NextDue     *time.Time               `json:"next_due,omitempty"`
	Preview     []time.Time              `json:"next_occurrences,omitempty"`
	Runs        []ChatScheduleOccurrence `json:"recent_runs,omitempty"`
}

type ChatScheduleOccurrence struct {
	Due             time.Time `json:"due"`
	Status          string    `json:"status"`
	TaskID          *string   `json:"task_id,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	ExecutionStatus string    `json:"execution_status,omitempty"`
}

type ChatScheduleMutation struct {
	Enabled                        *bool
	ID, Action, Key                string
	Instruction                    string
	Title                          *string
	Rule                           *ChatScheduleRule
	AttachmentIDs, AttachmentPaths []string
	SourceFilesUnused              bool
	ValidatePaths                  func(context.Context, Conversation, string, []string) ([]Attachment, error) `json:"-"`
}

type ChatScheduleRun struct {
	ID   string
	Tick TriggerScheduleTick
	Next time.Time
}

type ChatScheduleDispatch struct {
	Now                            time.Time
	Scope                          DelegationScope
	Instruction                    string
	Title                          *string
	Rule                           ChatScheduleRule
	AttachmentIDs, AttachmentPaths []string
}

type ChatScheduleStore interface {
	MutateChatSchedule(context.Context, DelegationScope, ChatScheduleMutation) (ChatSchedule, error)
	ListChatSchedules(context.Context, DelegationScope, string) ([]ChatSchedule, error)
	PollChatSchedules(context.Context, int) ([]TriggerScheduleItem, error)
	LoadChatScheduleDispatch(context.Context, string, TriggerScheduleTick) (*ChatScheduleDispatch, error)
	SkipChatSchedule(context.Context, ChatScheduleRun, string) (bool, error)
}

type chatScheduleTransaction interface {
	FinishChatSchedule(*ChatScheduleRun, string) error
}

func (d *Delegation) chatSchedule(ctx context.Context, scope DelegationScope, op string, args DelegationArgs) (any, error) {
	store, ok := d.Store.(ChatScheduleStore)
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "定时任务暂不可用")
	}
	if !ValidID(scope.InputID) || args.ScheduleID != "" && !ValidID(args.ScheduleID) {
		return nil, Fail("invalid_argument", 400, "定时计划标识无效")
	}
	if op == "list_scheduled_tasks" {
		return store.ListChatSchedules(ctx, scope, args.ScheduleID)
	}
	input := ChatScheduleMutation{ID: args.ScheduleID, Action: op, Instruction: args.Instruction, Title: args.Title, Rule: args.Schedule, AttachmentIDs: args.AttachmentIDs, AttachmentPaths: args.AttachmentPaths, SourceFilesUnused: args.SourceFilesUnused, Enabled: args.Enabled}
	if err := validateChatScheduleMutation(input, args.Cancel); err != nil {
		return nil, err
	}
	if args.Cancel {
		input.Action = "cancel_scheduled_task"
	}
	key, err := fingerprint([]any{scope.InputID, input})
	if err != nil {
		return nil, err
	}
	input.Key = key
	input.ValidatePaths = d.Manager.resolveInputAttachments
	result, err := store.MutateChatSchedule(ctx, scope, input)
	if err == nil {
		result.Preview = schedulePreview(result)
	}
	return result, err
}

func validateChatScheduleMutation(q ChatScheduleMutation, cancel bool) error {
	bad := Fail("invalid_argument", 400, "定时任务参数无效")
	if !validAttachmentSelection(q.AttachmentPaths, q.AttachmentIDs, true) || !validTitle(q.Title) || q.Instruction != "" && !validText(q.Instruction) {
		return bad
	}
	if q.Action == "create_scheduled_task" {
		if q.ID != "" || q.Rule == nil || q.Instruction == "" || cancel || q.Enabled != nil || q.SourceFilesUnused && len(q.AttachmentPaths)+len(q.AttachmentIDs) != 0 {
			return bad
		}
		return nil
	}
	changed := q.Rule != nil || q.Instruction != "" || q.Title != nil || q.Enabled != nil
	if q.ID == "" || len(q.AttachmentPaths)+len(q.AttachmentIDs) != 0 || q.SourceFilesUnused || cancel == changed {
		return bad
	}
	return nil
}

func schedulePreview(p ChatSchedule) []time.Time {
	if p.NextDue == nil {
		return nil
	}
	out := []time.Time{*p.NextDue}
	for len(out) < 3 {
		next, err := p.Rule.Next(out[len(out)-1])
		if err != nil || next.IsZero() {
			break
		}
		out = append(out, next)
	}
	return out
}

func (s *Service) TickChatSchedule(ctx context.Context, id string, tick TriggerScheduleTick) (TriggerScheduleResult, error) {
	out := TriggerScheduleResult{Status: "stale"}
	store, ok := s.Store.(ChatScheduleStore)
	if !ok || !ValidID(id) || tick.Version < 1 || tick.ExpectedDue.IsZero() || tick.RecoveryStartedAt.IsZero() {
		return out, Fail("invalid_argument", 400, "调度身份无效")
	}
	plan, err := store.LoadChatScheduleDispatch(ctx, id, tick)
	if err != nil || plan == nil {
		return out, err
	}
	run := ChatScheduleRun{ID: id, Tick: tick}
	now := plan.Now
	if tick.RecoveryStartedAt.After(now) {
		return out, Fail("invalid_argument", 400, "恢复时刻不能晚于服务器时间")
	}
	if tick.ExpectedDue.After(now) {
		out.Status = "not_due"
		return out, nil
	}
	if run.Next, err = plan.Rule.Next(now); err != nil {
		return out, err
	}
	if tick.ExpectedDue.Before(tick.RecoveryStartedAt) {
		return skipChatSchedule(ctx, store, run, "missed_while_offline")
	}
	v, err := s.submitChatSchedule(ctx, plan, &run)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "chat_schedule_stale" {
			return out, nil
		}
		if reason := ScheduleAdmissionSkip(err); reason != "" {
			return skipChatSchedule(ctx, store, run, reason)
		}
		return out, err
	}
	out.Status = "accepted"
	out.Receipt = &TriggerReceipt{EventID: id, Status: "accepted", TaskID: *v.TaskID, SessionID: v.ID, InputID: *v.InitialInputID}
	return out, nil
}

func skipChatSchedule(ctx context.Context, store ChatScheduleStore, run ChatScheduleRun, reason string) (TriggerScheduleResult, error) {
	ok, err := store.SkipChatSchedule(ctx, run, reason)
	status := "stale"
	if ok {
		status = "skipped"
	}
	return TriggerScheduleResult{Status: status}, err
}

func (s *Service) submitChatSchedule(ctx context.Context, plan *ChatScheduleDispatch, run *ChatScheduleRun) (Session, error) {
	scope := &plan.Scope
	if scope.Channel != nil {
		ctx = WithChannelActor(ctx, *scope.Channel)
	}
	if scope.WebGroup != nil {
		ctx = WithWebGroupExecution(ctx, *scope.WebGroup)
	}
	req := CreateSession{ConversationID: scope.ConversationID, Mode: "task", DeliveryMode: "queue", Title: plan.Title, AttachmentIDs: plan.AttachmentIDs, AttachmentPaths: plan.AttachmentPaths}
	req.Input = &struct {
		Text string `json:"text"`
	}{Text: plan.Instruction}
	v, _, err := s.createSessionWithSchedule(ctx, scope.WorkspaceID, scope.ActorID, "", req, scope, run)
	return v, err
}

// Management configuration excludes the conversation transcript and cannot
// change the originating actor, files, business instruction, or destination.
type ManagedChatSchedule struct {
	ChatSchedule
	Version     int64  `json:"version,string"`
	Channel     string `json:"channel"`
	ChatType    string `json:"chat_type"`
	SourceLabel string `json:"source_label,omitempty"`
}
type ManagedChatSchedulePatch struct {
	Version int64             `json:"version,string"`
	Rule    *ChatScheduleRule `json:"schedule,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}
type ChatScheduleManagementStore interface {
	ManagedChatSchedules(context.Context, string, string, string) ([]ManagedChatSchedule, error)
	UpdateManagedChatSchedule(context.Context, string, string, string, string, ManagedChatSchedulePatch) (ManagedChatSchedule, error)
}

func (s *Service) ManagedChatSchedules(ctx context.Context, w, actor, agent string) ([]ManagedChatSchedule, error) {
	st, ok := s.Store.(ChatScheduleManagementStore)
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "对话定时暂不可用")
	}
	if !ValidID(w) || !ValidID(actor) || !ValidID(agent) {
		return nil, Fail("invalid_argument", 400, "工作区或员工无效")
	}
	return st.ManagedChatSchedules(ctx, w, actor, agent)
}
func (s *Service) UpdateManagedChatSchedule(ctx context.Context, w, actor, agent, id string, q ManagedChatSchedulePatch) (ManagedChatSchedule, error) {
	st, ok := s.Store.(ChatScheduleManagementStore)
	if !ok {
		return ManagedChatSchedule{}, Fail("dependency_unavailable", 503, "对话定时暂不可用")
	}
	if !ValidID(w) || !ValidID(actor) || !ValidID(agent) || !ValidID(id) || q.Version < 1 || q.Rule == nil && q.Enabled == nil {
		return ManagedChatSchedule{}, Fail("invalid_argument", 400, "对话定时配置无效")
	}
	return st.UpdateManagedChatSchedule(ctx, w, actor, agent, id, q)
}
