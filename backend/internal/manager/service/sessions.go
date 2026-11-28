package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func validText(v string) bool {
	return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 65536 && !strings.ContainsRune(v, '\x00')
}
func validMessageText(text string, paths, ids []string) bool {
	return validText(text) || text == "" && len(paths)+len(ids) > 0
}
func validKey(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
func fingerprint(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func admitted(st SessionState) error {
	if st.Session.PlanningState == "planned" || st.Session.PlanningState == "canceled" {
		return Fail("task_not_started", 409, "待办必须显式启动，已取消待办不能执行")
	}
	if ownerLifecycle(st) != "active" {
		return Fail("conversation_archived", 409, "业务会话已归档")
	}
	if st.Session.ID != "" && st.Session.Lifecycle != "active" {
		return Fail("session_archived", 409, "执行会话已归档")
	}
	if !st.AgentEnabled {
		return Fail("agent_disabled", 409, "员工已停用")
	}
	return nil
}

func (s *Service) CreateSession(ctx context.Context, w, actor, key string, r CreateSession) (Session, bool, error) {
	return s.createSession(ctx, w, actor, key, r, nil)
}
func (s *Service) createSession(ctx context.Context, w, actor, key string, r CreateSession, source *DelegationScope) (Session, bool, error) {
	mode, modeErr := DeliveryMode(r.DeliveryMode)
	if modeErr != nil {
		return Session{}, false, modeErr
	}
	requestedMode := r.DeliveryMode
	legacyFingerprint, legacyErr := fingerprint(r)
	if legacyErr != nil {
		return Session{}, false, legacyErr
	}
	r.DeliveryMode = mode
	target := Target{Workspace: w, Actor: actor, Conversation: r.ConversationID, TaskSpaceID: r.TaskSpaceID, AgentID: r.AgentID}
	if source != nil {
		target.Source = &source.Identity
		target.SourceInputID = source.InputID
		target.DelegatedFiles = &DelegatedFiles{IDs: r.AttachmentIDs, Paths: r.AttachmentPaths, SourceFilesUnused: source.SourceFilesUnused}
	}
	if !validSessionOwner(r, target.Source) || (r.Mode != "chat" && r.Mode != "task") || !validAttachmentSelection(r.AttachmentPaths, r.AttachmentIDs, source != nil) || !validTitle(r.Title) || r.Input != nil && !validMessageText(r.Input.Text, r.AttachmentPaths, r.AttachmentIDs) {
		return Session{}, false, Fail("invalid_argument", 400, "会话参数无效")
	}
	d := Dedup{Kind: "initial_input", Target: "sessions", Key: key}
	if r.Input != nil {
		var err error
		d.Fingerprint, err = fingerprint(r)
		if err != nil {
			return Session{}, false, err
		}
		if source != nil {
			sum := sha256.Sum256([]byte("task_delegation_create:" + source.Identity.SessionID + ":" + source.InputID + ":" + d.Fingerprint))
			d.Kind = "delegated_initial_input"
			d.Key = hex.EncodeToString(sum[:])
		}
		if !validKey(d.Key) {
			return Session{}, false, Fail("idempotency_key_required", 400, "提交首条输入需要有效去重键")
		}
		target.DedupLock = d.Kind + ":" + d.Target + ":" + d.Key
	}
	initialID, initialDedup := NewID(), d
	id := initialID
	replayed := false
	err := s.Store.WithSession(ctx, target, func(tx SessionTx) error {
		id, d, replayed = initialID, initialDedup, false
		if r.Input != nil {
			found, err := tx.FindDedup(d)
			if err != nil {
				return err
			}
			if found != nil {
				if found.Fingerprint != d.Fingerprint && (requestedMode != "" || found.Fingerprint != legacyFingerprint) {
					return Fail("idempotency_conflict", 409, "去重键已用于其他内容")
				}
				id = found.SessionID
				replayed = true
				return nil
			}
		}
		if err := tx.CheckDelegatedInput(); err != nil {
			return err
		}
		st := tx.State()
		if err := admitted(st); err != nil {
			return err
		}
		cfg, err := tx.Configuration(r.Mode)
		if err != nil {
			return err
		}
		v := Session{ID: id, WorkspaceID: w, AgentID: st.AgentID, ConversationID: r.ConversationID, TaskSpaceID: st.TaskSpaceID, CreatedBy: actor, Mode: r.Mode, Title: r.Title, Lifecycle: "active", Revision: 1, Configuration: cfg, CreatedAt: time.Now().UTC()}
		if r.Mode == "task" {
			task := NewID()
			v.TaskID = &task
			var chat *string
			if target.Source != nil {
				chat = &target.Source.SessionID
			}
			if err := tx.InsertTask(task, chat); err != nil {
				return err
			}
		}
		if err := tx.InsertSession(v); err != nil {
			return err
		}
		if r.Input != nil {
			i, err := s.appendInputMode(ctx, tx, actor, r.Input.Text, mode, r.AttachmentPaths, r.AttachmentIDs, nil)
			if err != nil {
				return err
			}
			if err = tx.SetInitialInput(i.ID); err != nil {
				return err
			}
			d.SessionID = id
			d.InputID = i.ID
			if err = tx.RecordDedup(d); err != nil {
				return err
			}
		}
		return tx.SnapshotEvent()
	})
	if err != nil {
		return Session{}, false, err
	}
	v, err := s.Store.Session(ctx, w, actor, id)
	return PublicSession(v), replayed, err
}

func (s *Service) appendInputMode(ctx context.Context, tx SessionTx, actor, text, mode string, paths, ids []string, trusted []Attachment) (Input, error) {
	st := tx.State()
	if st.Session.Mode == "chat" {
		if check, ok := tx.(interface{ CheckAssistantChatModel() error }); ok {
			if err := check.CheckAssistantChatModel(); err != nil {
				return Input{}, err
			}
		}
	}
	if st.Session.Execution.QueuedInputCount >= 100 {
		return Input{}, Fail("session_queue_full", 429, "待执行输入已达到上限")
	}
	attachments, err := s.resolveInputAttachments(ctx, st.Conversation, actor, paths)
	if err == nil && len(ids) > 0 {
		var refs []Attachment
		refs, err = resolveTaskAttachmentIDs(tx, ids)
		attachments = append(attachments, refs...)
	}
	if err != nil {
		return Input{}, err
	}
	if len(trusted) > 0 && st.Session.Mode != "chat" {
		return Input{}, Fail("permission_denied", 403, "渠道文件只可随原消息入队")
	}
	attachments = append(attachments, trusted...)
	for n := range attachments {
		if attachments[n].ID != "" {
			attachments[n].Path = ""
		}
	}
	i, err := tx.InsertInput(Input{Attachments: attachments, ID: NewID(), SessionID: st.Session.ID, SubmittedBy: actor, Text: text, DeliveryMode: mode, SourceKind: "user", Status: "queued", CreatedAt: time.Now().UTC()})
	if err != nil {
		return i, err
	}
	if err = tx.Event("user.message", &i.ID, nil, inputFields(i)); err != nil {
		return i, err
	}
	return i, emitInput(tx, i)
}
func decimal(v int64) string { return strconv.FormatInt(v, 10) }
func emitInput(tx SessionTx, i Input) error {
	return tx.Event("input.updated", &i.ID, i.TurnID, inputFields(i))
}

// ReconcileQueuedInputs closes admissions invalidated before assignment. It
// does not need an Engine or Sandbox and never changes a started Input.
func ReconcileQueuedInputs(tx SessionTx) error {
	items, err := tx.RejectUnavailableInputs()
	if err != nil {
		return err
	}
	for _, input := range items {
		if err = emitInput(tx, input); err != nil {
			return err
		}
	}
	if len(items) > 0 {
		return tx.SnapshotEvent()
	}
	return nil
}
func inputFields(i Input) map[string]any {
	return InputFields(i)
}

// InputFields is the shared reliable projection for user and internal inputs.
func InputFields(i Input) map[string]any {
	if i.Attachments == nil {
		i.Attachments = []Attachment{}
	}
	var rejection any
	if i.RejectionReason != nil {
		rejection = map[string]string{"code": *i.RejectionReason, "message": "输入未执行"}
	}
	return map[string]any{"attachments": i.Attachments, "confirmation_request_id": i.ConfirmationRequestID, "confirmed": i.Confirmed, "trigger_agent_loop": i.TriggerAgentLoop, "delivery_mode": i.DeliveryMode, "source_kind": i.SourceKind, "report_id": i.ReportID, "message_id": i.ID, "input_seq": decimal(i.Seq), "submitted_by": nullableWebActor(i), "channel_principal_id": i.ChannelPrincipalID, "principal_type": inputPrincipalType(i), "content": []any{map[string]string{"type": "text", "text": i.Text}}, "status": i.Status, "cancel_reason": i.CancelReason, "rejection": rejection, "received_at": i.CreatedAt, "started_at": i.StartedAt, "finished_at": i.FinishedAt}
}

func (s *Service) SubmitInput(ctx context.Context, w, actor, id, key, text string) (Input, bool, error) {
	return s.SubmitInputMode(ctx, w, actor, id, key, text, "")
}
func (s *Service) SubmitInputMode(ctx context.Context, w, actor, id, key, text, mode string, paths ...string) (Input, bool, error) {
	return s.submitInputVersion(ctx, w, actor, id, key, text, mode, nil, nil, nil, paths...)
}
func (s *Service) SubmitInputModeWithAttachments(ctx context.Context, w, actor, id, key, text, mode string, ids []string, paths ...string) (Input, bool, error) {
	return s.submitInputVersion(ctx, w, actor, id, key, text, mode, nil, nil, ids, paths...)
}
func DeliveryMode(mode string) (string, error) {
	if mode == "" {
		return "steer", nil
	}
	if mode != "steer" && mode != "queue" {
		return "", Fail("invalid_argument", 400, "delivery_mode 必须为 steer 或 queue")
	}
	return mode, nil
}
func (s *Service) submitInputVersion(ctx context.Context, w, actor, id, key, text, requestedMode string, source *DelegationScope, expected *int64, ids []string, paths ...string) (Input, bool, error) {
	mode, modeErr := DeliveryMode(requestedMode)
	if modeErr != nil {
		return Input{}, false, modeErr
	}
	var trusted []Attachment
	if ch, ok := ChannelActorFrom(ctx); ok && source == nil && ch.ID == actor && ValidID(ch.InboxID) && key == "channel_"+strings.ReplaceAll(ch.InboxID, "-", "") && !ch.ConfirmationOnly {
		trusted = ch.Attachments
	}
	if (!validMessageText(text, paths, ids) && (text != "" || len(trusted) == 0)) || !validAttachmentSelection(paths, ids, source != nil) {
		return Input{}, false, Fail("invalid_argument", 400, "输入文本无效")
	}
	if !validKey(key) {
		return Input{}, false, Fail("idempotency_key_required", 400, "提交输入需要有效去重键")
	}
	fp, err := fingerprint([]string{text, mode})
	if err != nil {
		return Input{}, false, err
	}
	d := Dedup{Kind: "session_input", Target: id, Key: key, Fingerprint: fp}
	if len(ids) > 0 || len(paths) > 0 {
		payload := []any{text, mode, nil, paths}
		if len(ids) > 0 {
			payload = append(payload, ids)
		}
		fp, err = fingerprint(payload)
		if err != nil {
			return Input{}, false, err
		}
		d.Fingerprint = fp
	}
	initialDedup := d
	var out Input
	replayed := false
	target := Target{Workspace: w, Actor: actor, Session: id, DedupLock: d.Kind + ":" + id + ":" + key}
	if source != nil {
		target.Source = &source.Identity
		target.SourceInputID = source.InputID
		target.DelegatedFiles = &DelegatedFiles{IDs: ids, Paths: paths, SourceFilesUnused: source.SourceFilesUnused}
	}
	err = s.Store.WithSession(ctx, target, func(tx SessionTx) error {
		out, d, replayed = Input{}, initialDedup, false
		found, err := tx.FindDedup(d)
		if err != nil {
			return err
		}
		if found != nil {
			legacy, e := fingerprint(text)
			if e != nil {
				return e
			}
			if found.Fingerprint != fp && (len(paths) > 0 || len(ids) > 0 || expected != nil || requestedMode != "" || found.Fingerprint != legacy) {
				return Fail("idempotency_conflict", 409, "去重键已用于其他内容")
			}
			out, err = tx.Input(found.InputID)
			replayed = true
			return err
		}
		if err := tx.CheckDelegatedInput(); err != nil {
			return err
		}
		if expected != nil && tx.State().LastInputSeq != *expected {
			return Fail("version_conflict", 409, "任务输入已变更，请重新查询")
		}
		if err = admitted(tx.State()); err != nil {
			return err
		}
		out, err = s.appendInputMode(ctx, tx, actor, text, mode, paths, ids, trusted)
		if err != nil {
			return err
		}
		d.SessionID = id
		d.InputID = out.ID
		if err = tx.RecordDedup(d); err != nil {
			return err
		}
		return tx.SnapshotEvent()
	})
	if err != nil {
		return Input{}, false, err
	}
	return out, replayed, nil
}

func (s *Service) WithdrawInput(ctx context.Context, w, actor, id, input string) (Input, error) {
	var out Input
	err := s.Store.WithSession(ctx, Target{Workspace: w, Actor: actor, Session: id}, func(tx SessionTx) error {
		out = Input{}
		v, err := tx.Input(input)
		if err != nil {
			return err
		}
		out = v
		if v.Status == "canceled" {
			return nil
		}
		if v.Status != "queued" || v.ConfirmationRequestID != nil {
			return Fail("input_not_queued", 409, "输入已开始或不可撤回")
		}
		out, err = tx.CancelInput(input, "withdrawn")
		if err != nil {
			return err
		}
		if err = emitInput(tx, out); err != nil {
			return err
		}
		return tx.SnapshotEvent()
	})
	if err != nil {
		return Input{}, err
	}
	return out, nil
}

func (s *Service) ControlSession(ctx context.Context, w, actor, id string, archive bool) (Session, error) {
	return s.controlSession(ctx, w, actor, id, archive, nil)
}

// ControlTaskFromWeb is the authenticated direct Session control entry. A
// pending workflow cannot be canceled through either stop or archive here.
// Main-chat delegation and broader orchestration retain their own semantics.
func (s *Service) ControlTaskFromWeb(ctx context.Context, w, actor, id string, archive bool) (Session, error) {
	return s.controlSessionMode(ctx, w, actor, id, archive, nil, true)
}

func (s *Service) controlSession(ctx context.Context, w, actor, id string, archive bool, source *DelegationIdentity) (Session, error) {
	return s.controlSessionMode(ctx, w, actor, id, archive, source, false)
}

func (s *Service) controlSessionMode(ctx context.Context, w, actor, id string, archive bool, source *DelegationIdentity, web bool) (Session, error) {
	err := s.Store.WithSession(ctx, Target{Workspace: w, Actor: actor, Session: id, Source: source, Control: true}, func(tx SessionTx) error {
		st := tx.State()
		if source != nil && !archive && st.Session.PlanningState == "canceled" {
			return CanceledTodoError{}
		}
		if !archive && (st.Session.PlanningState == "planned" || st.Session.PlanningState == "canceled") {
			return Fail("task_not_started", 409, "未启动待办请使用取消待办，而不是停止执行")
		}
		if archive && st.Session.Lifecycle == "archived" {
			return nil
		}
		if err := guardTaskDetailControl(st, web); err != nil {
			return err
		}
		if archive && ownerLifecycle(st) != "archived" && (st.Session.Execution.ActiveTurnID != nil || st.Session.Execution.QueuedInputCount > 0 || st.Session.Execution.BackgroundProcessCount == nil || *st.Session.Execution.BackgroundProcessCount != 0 || (st.Session.Execution.BlockReason != nil && *st.Session.Execution.BlockReason != "remote_tool_outcome_unknown") || st.StopRevision > st.AppliedStopRevision) {
			return Fail("session_busy", 409, "执行会话仍在运行、排队或等待停止确认，请先停止并等待收尾；存在未知活动时须先核对处理后再归档")
		}
		if !archive {
			if st.Session.Lifecycle == "archived" {
				return Fail("session_archived", 409, "执行会话已归档")
			}
			if st.StopRevision > st.AppliedStopRevision && st.StopInputSeq == st.LastInputSeq {
				return nil
			}
			if len(st.Session.Execution.RequiredActions) == 0 && st.Session.Execution.ActiveTurnID == nil && st.Session.Execution.QueuedInputCount == 0 && st.Session.Execution.BackgroundProcessCount != nil && *st.Session.Execution.BackgroundProcessCount == 0 && st.Session.Execution.BlockReason == nil {
				return nil
			}
		}
		reason := "session_stopped"
		if archive {
			reason = "session_archived"
		}
		items, err := tx.CancelQueued(reason)
		if err != nil {
			return err
		}
		for _, i := range items {
			if err = emitInput(tx, i); err != nil {
				return err
			}
		}
		if err = tx.Stop(archive); err != nil {
			return err
		}
		return tx.SnapshotEvent()
	})
	if err != nil {
		return Session{}, err
	}
	v, err := s.Store.Session(ctx, w, actor, id)
	return PublicSession(v), err
}

func (s *Service) RenameSession(ctx context.Context, w, actor, id string, title *string, revision int64) (Session, error) {
	if revision < 1 || !validTitle(title) {
		return Session{}, Fail("invalid_argument", 400, "标题或修订号无效")
	}
	err := s.Store.WithSession(ctx, Target{Workspace: w, Actor: actor, Session: id}, func(tx SessionTx) error {
		st := tx.State()
		if st.Session.Lifecycle != "active" {
			return Fail("session_archived", 409, "执行会话已归档")
		}
		if st.Session.Revision != revision {
			return Fail("session_revision_conflict", 409, "修订号已变化")
		}
		if st.Session.Title == nil && title == nil || st.Session.Title != nil && title != nil && *st.Session.Title == *title {
			return nil
		}
		if err := tx.UpdateTitle(title, revision); err != nil {
			return err
		}
		return tx.SnapshotEvent()
	})
	if err != nil {
		return Session{}, err
	}
	v, err := s.Store.Session(ctx, w, actor, id)
	return PublicSession(v), err
}

func (s *Service) ArchiveConversation(ctx context.Context, w, actor, id string) (Conversation, error) {
	var sessions []string
	err := s.Store.WithSession(ctx, Target{Workspace: w, Actor: actor, Conversation: id}, func(tx SessionTx) error {
		sessions = nil
		var err error
		sessions, err = tx.ArchiveConversation()
		return err
	})
	if err != nil {
		return Conversation{}, err
	}
	for _, session := range sessions {
		if _, err = s.ControlSession(ctx, w, actor, session, true); err != nil {
			return Conversation{}, err
		}
	}
	return s.Store.Conversation(ctx, w, actor, id)
}

func nullableWebActor(i Input) any {
	if i.ChannelPrincipalID != nil {
		return nil
	}
	return i.SubmittedBy
}
func inputPrincipalType(i Input) string {
	if i.PrincipalType == "work_item" {
		return i.PrincipalType
	}
	if i.ChannelPrincipalID != nil {
		return "channel"
	}
	return "web"
}

func ownerLifecycle(st SessionState) string {
	if st.OwnerLifecycle != "" {
		return st.OwnerLifecycle
	}
	return st.Conversation.Lifecycle
}
func validSessionOwner(r CreateSession, source *DelegationIdentity) bool {
	if r.TaskSpaceID != "" {
		return r.ConversationID == "" && ValidID(r.TaskSpaceID) && ValidID(r.AgentID) && r.Mode == "task" && r.Input != nil && source == nil
	}
	return ValidID(r.ConversationID) && r.AgentID == ""
}
