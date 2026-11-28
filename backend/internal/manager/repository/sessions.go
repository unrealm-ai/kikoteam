package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const sessionBeforeReason = `s.required_actions,COALESCE((SELECT planning_state FROM manager_tasks WHERE id=s.task_id),''),COALESCE((SELECT planning_version FROM manager_tasks WHERE id=s.task_id),0),s.id,s.workspace_id,s.agent_id,COALESCE(s.conversation_id::text,''),s.task_id,COALESCE(s.created_by::text,''),s.mode,s.title,s.lifecycle,s.revision,s.initial_input_id,s.configuration,s.created_at,s.archived_at,s.active_turn_id,s.background_process_count,`
const sessionAfterReason = `,COALESCE(c.lifecycle,p.lifecycle),a.enabled,a.admission_epoch,s.last_input_seq,s.stop_input_seq,s.stop_revision,s.applied_stop_revision,(SELECT count(*) FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued'),s.channel_created_by,s.task_space_id,(s.trigger_id IS NULL AND (s.task_space_id IS NULL OR COALESCE((manager_task_space_actor_allowed(s.task_space_id,p.created_by::text,s.agent_id) AND s.task_space_agent_epoch=(SELECT g.admission_epoch FROM manager_task_space_agents g WHERE g.task_space_id=s.task_space_id AND g.agent_id=s.agent_id)),false)))`
const sessionColumns = sessionBeforeReason + `s.block_reason` + sessionAfterReason
const publicSessionColumns = sessionBeforeReason + publicBlockReason + sessionAfterReason
const sessionFrom = ` FROM manager_sessions s LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id JOIN platform_agents a ON a.id=s.agent_id `
const memberRead = ` ((s.trigger_id IS NOT NULL AND manager_trigger_admin_allowed(s.trigger_id,$2::uuid::text)) OR (s.trigger_id IS NULL AND s.task_space_id IS NOT NULL AND p.created_by=$2 AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=s.workspace_id AND m.user_id=$2 AND m.status='active')) OR (s.task_space_id IS NULL AND c.web_group_id IS NULL AND c.channel_binding_id IS NULL AND (c.web_user_id IS NULL OR c.web_user_id=$2) AND EXISTS(SELECT 1 FROM platform_members m WHERE m.workspace_id=s.workspace_id AND m.user_id=$2 AND m.status='active'))) `

func scanSession(row scanner) (service.SessionState, error) {
	var st service.SessionState
	var cfg, actions []byte
	var parent string
	var spaceAllowed bool
	v := &st.Session
	err := row.Scan(&actions, &v.PlanningState, &v.PlanningVersion, &v.ID, &v.WorkspaceID, &v.AgentID, &v.ConversationID, &v.TaskID, &v.CreatedBy, &v.Mode, &v.Title, &v.Lifecycle, &v.Revision, &v.InitialInputID, &cfg, &v.CreatedAt, &v.ArchivedAt, &v.Execution.ActiveTurnID, &v.Execution.BackgroundProcessCount, &v.Execution.BlockReason, &parent, &st.AgentEnabled, &st.AdmissionEpoch, &st.LastInputSeq, &st.StopInputSeq, &st.StopRevision, &st.AppliedStopRevision, &v.Execution.QueuedInputCount, &v.ChannelCreatedBy, &v.TaskSpaceID, &spaceAllowed)
	if err != nil {
		return st, visibleError(err)
	}
	if err = json.Unmarshal(actions, &v.Execution.RequiredActions); err != nil {
		return st, err
	}
	if err = json.Unmarshal(cfg, &v.Configuration); err != nil {
		return st, err
	}
	v.InputRevision = st.LastInputSeq
	st.OwnerLifecycle = parent
	st.AgentID = v.AgentID
	st.TaskSpaceID = v.TaskSpaceID
	v.Execution.State = "idle"
	switch {
	case v.Execution.BlockReason != nil || v.Execution.BackgroundProcessCount == nil:
		v.Execution.State = "blocked"
	case st.StopRevision > st.AppliedStopRevision:
		v.Execution.State = "stopping"
	case v.Execution.ActiveTurnID != nil:
		v.Execution.State = "running"
	case v.Execution.QueuedInputCount > 0:
		v.Execution.State = "queued"
	}
	v.AcceptsInput = true
	reason := ""
	switch {
	case v.PlanningState == "planned" || v.PlanningState == "canceled":
		reason = "task_" + v.PlanningState
	case parent != "active":
		reason = "conversation_archived"
	case v.Lifecycle != "active":
		reason = "session_archived"
	case !spaceAllowed:
		reason = "task_space_access_revoked"
	case !st.AgentEnabled:
		reason = "agent_disabled"
	}
	if reason != "" {
		v.AcceptsInput = false
		v.InputBlockReason = &reason
	}
	return st, nil
}

func (s *Store) Session(ctx context.Context, w, actor, id string) (service.Session, error) {
	if channel, ok := service.ChannelActorFrom(ctx); ok && channel.ConfirmationOnly && channel.ID == actor {
		st, err := scanSession(s.DB.QueryRowContext(ctx, `SELECT `+publicSessionColumns+sessionFrom+` WHERE s.workspace_id=$1 AND s.id=$3
 AND s.mode='chat' AND s.channel_created_by=$2 AND EXISTS(SELECT FROM manager_channel_inbox n
 WHERE n.id=$4 AND n.conversation_id=c.id AND n.connection_generation=$5
 AND manager_human_inbox_confirmation_allowed(n.id,$2))`, w, actor, id, channel.InboxID, channel.Generation))
		return st.Session, err
	}
	st, err := scanSession(s.DB.QueryRowContext(ctx, `SELECT `+publicSessionColumns+sessionFrom+` WHERE s.workspace_id=$1 AND `+sessionRead(ctx, actor)+` AND s.id=$3`, w, actor, id))
	return st.Session, err
}

type sessionTx struct {
	delegationInput *delegatedInputAdmission
	ctx             context.Context
	tx              *sql.Tx
	target          service.Target
	state           service.SessionState
	store           *Store
}

func (s *Store) WithSession(ctx context.Context, target service.Target, fn func(service.SessionTx) error) error {
	return retrySessionTransaction(ctx, func() error { return s.withSessionAttempt(ctx, target, fn, false) })
}

// Serialize the filesystem publication with conversation archival and access checks.
// External work may run once per request, even when PostgreSQL reports a deadlock.
func (s *Store) WithProjectPublication(ctx context.Context, target service.Target, publish func(service.SessionState) error) error {
	return s.withSessionAttempt(ctx, target, func(tx service.SessionTx) error { return publish(tx.State()) }, false)
}

func (s *Store) withSessionAttempt(ctx context.Context, target service.Target, fn func(service.SessionTx) error, dispatch bool) error {
	spaceTarget, err := s.resolveTaskSpaceTarget(ctx, target)
	if err != nil {
		return err
	}
	if spaceTarget.TaskSpaceID != "" {
		return s.withTaskSpaceSession(ctx, spaceTarget, fn, dispatch)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve the operation error; rollback after commit is harmless.
	var workspace string
	channel, isChannel := service.ChannelActorFrom(ctx)
	groupSource, hasGroupSource := service.WebGroupExecutionFrom(ctx)
	if dispatch {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR SHARE`, target.Workspace).Scan(&workspace); err != nil {
			return visibleError(err)
		}
	} else if isChannel {
		if channel.ID != target.Actor {
			return service.Fail("resource_not_found", 404, "主体不匹配")
		}
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM manager_channel_principals WHERE id=$1 AND workspace_id=$2`, target.Actor, target.Workspace).Scan(&workspace); err != nil {
			return visibleError(err)
		}
	} else {
		if err = tx.QueryRowContext(ctx, `SELECT w.id FROM platform_workspaces w JOIN platform_members m ON m.workspace_id=w.id AND m.user_id=$2 AND m.status='active' WHERE w.id=$1 FOR SHARE OF w`, target.Workspace, target.Actor).Scan(&workspace); err != nil {
			return visibleError(err)
		}
	}
	if target.DedupLock != "" {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, target.Workspace+":"+target.Actor+":"+target.DedupLock); err != nil {
			return err
		}
	}
	if target.Session != "" {
		if target.Source != nil {
			if err = s.checkDelegationTx(ctx, tx, target); err != nil {
				return err
			}
		}
		if err = tx.QueryRowContext(ctx, `SELECT conversation_id FROM manager_sessions WHERE workspace_id=$1 AND id=$2`, target.Workspace, target.Session).Scan(&target.Conversation); err != nil {
			return visibleError(err)
		}
	}
	var agent string
	if err = tx.QueryRowContext(ctx, `SELECT agent_id FROM manager_conversations WHERE workspace_id=$1 AND id=$2 AND ((web_group_id IS NULL AND channel_binding_id IS NULL AND (web_user_id IS NULL OR web_user_id=$3)) OR $4 OR $5 OR ($6 AND id=$7::uuid AND manager_web_group_actor_allowed(id,$3)))`, target.Workspace, target.Conversation, target.Actor, dispatch, isChannel, hasGroupSource, nullID(groupSource.ConversationID)).Scan(&agent); err != nil {
		return visibleError(err)
	}
	t := &sessionTx{ctx: ctx, tx: tx, target: target, store: s}
	if err = tx.QueryRowContext(ctx, `SELECT enabled,admission_epoch FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, target.Workspace, agent).Scan(&t.state.AgentEnabled, &t.state.AdmissionEpoch); err != nil {
		return visibleError(err)
	}
	if !isChannel && !dispatch && !hasGroupSource {
		var allowed bool
		if err = tx.QueryRowContext(ctx, `SELECT platform_agent_web_allowed($1,$2,$3)`, target.Workspace, agent, target.Actor).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return service.Fail("employee_access_denied", 403, "没有员工使用权限")
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_conversations WHERE id=$1 FOR UPDATE`, target.Conversation).Scan(&workspace); err != nil {
		return err
	}
	if isChannel {
		if err = tx.QueryRowContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN manager_channel_principals p ON p.binding_id=b.id WHERE p.id=$1 FOR SHARE OF b`, target.Actor).Scan(&workspace); err != nil {
			return visibleError(err)
		}
		var allowed bool
		if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN $4 THEN EXISTS(SELECT 1 FROM manager_channel_inbox n
 WHERE n.id=$5 AND n.conversation_id=$2 AND n.connection_generation=$3
 AND manager_human_inbox_confirmation_allowed(n.id,$1))
 ELSE manager_channel_actor_allowed($1,$2,$3) END`, target.Actor, target.Conversation, channel.Generation, channel.ConfirmationOnly, nullID(channel.InboxID)).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return service.Fail("access_revoked", 403, "渠道权限已撤销")
		}
	}
	if target.Source != nil {
		if err = s.lockDelegationSource(ctx, tx, target); err != nil {
			return err
		}
	}
	t.state.AgentID = agent
	t.state.Conversation, err = scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM manager_conversations c WHERE c.id=$1`, target.Conversation))
	if err != nil {
		return err
	}
	t.state.OwnerLifecycle = t.state.Conversation.Lifecycle
	if target.Session != "" {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE id=$1 FOR UPDATE`, target.Session).Scan(&workspace); err != nil {
			return err
		}
		if err = t.refresh(); err != nil {
			return err
		}
	}
	return t.commitSessionOperation(fn, dispatch)
}

func (t *sessionTx) commitSessionOperation(fn func(service.SessionTx) error, dispatch bool) error {
	if err := t.checkPlannedCreator(dispatch); err != nil {
		return err
	}
	if t.target.DelegatedFiles != nil {
		return t.commitDelegatedInput(fn)
	}
	if err := fn(t); err != nil {
		return err
	}
	return t.tx.Commit()
}

// checkPlannedCreator applies the read boundary to all public mutations while
// the Session lock serializes authorization with start/cancel.
func (t *sessionTx) checkPlannedCreator(dispatch bool) error {
	if dispatch || (t.state.Session.PlanningState != "planned" && t.state.Session.PlanningState != "canceled") {
		return nil
	}
	var creator bool
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(COALESCE(created_by,channel_created_by)::text=$2,false) FROM manager_sessions WHERE id=$1`, t.target.Session, t.target.Actor).Scan(&creator); err != nil {
		return err
	}
	if !creator {
		return service.Fail("resource_not_found", 404, "会话不存在")
	}
	return nil
}

func (t *sessionTx) State() service.SessionState { return t.state }
func (t *sessionTx) refresh() error {
	c := t.state.Conversation
	st, err := scanSession(t.tx.QueryRowContext(t.ctx, `SELECT `+sessionColumns+sessionFrom+` WHERE s.id=$1`, t.target.Session))
	if err != nil {
		return err
	}
	st.Conversation = c
	t.state = st
	return nil
}

func (t *sessionTx) modelConfiguration(mode string) (service.Configuration, error) {
	v := service.Configuration{Harness: "pi", Skills: []string{}, Plugins: []string{}}
	var model, official string
	var assistant, follow bool
	if err := t.tx.QueryRowContext(t.ctx, `SELECT a.config_version,a.instruction,coalesce(COALESCE(a.model_profile_id,d.model_profile_id)::text,''),
 coalesce(COALESCE(a.platform_model_id,d.platform_model_id)::text,''),w.assistant_agent_id IS NOT DISTINCT FROM a.id,a.follows_default_model
 FROM platform_agents a JOIN platform_workspaces w ON w.id=a.workspace_id
 LEFT JOIN LATERAL platform_assistant_default_model(a.workspace_id) d ON a.follows_default_model AND w.assistant_agent_id=a.id
 WHERE a.id=$1`, t.state.AgentID).Scan(&v.AgentVersion, &v.Instruction, &model, &official, &assistant, &follow); err != nil {
		return v, err
	}
	query := `SELECT id,version,protocol,base_url,model_id FROM platform_model_profiles WHERE workspace_id=$1 AND enabled AND validated AND id=NULLIF($2,'')::uuid`
	if mode == "chat" {
		v.Instruction = service.PlatformChatInstruction
	}
	var err error
	if official == "" {
		err = t.tx.QueryRowContext(t.ctx, query, t.target.Workspace, model).Scan(&v.ModelProfileID, &v.ModelVersion, &v.Protocol, &v.BaseURL, &v.ModelID)
	} else {
		v, err = t.officialConfiguration(v, official, assistant && follow)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if assistant && follow && mode == "chat" {
			return v, assistantModelRequired()
		}
		return v, service.Fail("configuration_unusable", 422, "模型未配置、未验证或已停用")
	}
	if err != nil {
		return v, err
	}
	return v, nil
}

func (t *sessionTx) Configuration(mode string) (service.Configuration, error) {
	v, err := t.modelConfiguration(mode)
	if err != nil {
		return v, err
	}
	if a, ok := service.ChannelActorFrom(t.ctx); ok {
		t.ctx = platform.WithChannelExecution(t.ctx, platform.ChannelExecution{PrincipalID: a.ID, Generation: a.Generation, ConversationID: t.target.Conversation})
	}
	if g, ok := service.WebGroupExecutionFrom(t.ctx); ok {
		t.ctx = platform.WithWebGroupExecution(t.ctx, platform.WebGroupExecution{ConversationID: g.ConversationID})
	}
	if t.store.SnapshotCapabilities != nil && (mode != "chat" || t.target.Actor != "") {
		caps, e := t.store.SnapshotCapabilities(t.ctx, t.tx, t.target.Workspace, t.target.Actor, t.state.AgentID)
		if e != nil {
			return v, e
		}
		v.Capabilities = &caps
		// Chat uses the platform harness. Delegated tasks take their own fresh snapshot.
		if mode == "chat" {
			return v, nil
		}
		for _, p := range caps.Packages {
			for _, r := range p.Skills {
				if !slices.Contains(v.Skills, r.SkillID) {
					v.Skills = append(v.Skills, r.SkillID)
				}
			}
			for _, r := range p.Plugins {
				if !slices.Contains(v.Plugins, r.PluginID) {
					v.Plugins = append(v.Plugins, r.PluginID)
				}
			}
		}
	}
	return v, nil
}
func (t *sessionTx) officialConfiguration(v service.Configuration, id string, autoBind bool) (service.Configuration, error) {
	q := `SELECT id,version,protocol FROM platform_proxy_models WHERE enabled AND validated AND id=NULLIF($1,'')::uuid FOR SHARE`
	if e := t.tx.QueryRowContext(t.ctx, q, id).Scan(&v.PlatformModelID, &v.ModelVersion, &v.Protocol); e != nil {
		return v, e
	}
	var bound bool
	if e := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM platform_agent_model_keys WHERE agent_id=$1)`, t.state.AgentID).Scan(&bound); e != nil {
		return v, e
	}
	if !bound && autoBind && t.store.EnsureAssistantModelKey != nil {
		if e := t.store.EnsureAssistantModelKey(t.ctx, t.tx, t.target.Workspace, t.state.AgentID); e != nil {
			return v, e
		}
		bound = true
	}
	if !bound {
		return v, service.Fail("platform_billing_unconfigured", 422, "请在员工模型设置中确认官方模型扣费账户")
	}
	v.ModelID = v.PlatformModelID
	return v, nil
}
func assistantModelRequired() error {
	return service.Fail("assistant_model_required", 422, "KIKO 助手尚未配置可用模型，请先配置模型")
}

// An idle assistant chat can adopt a new model without a new conversation.
// A running or queued turn retains its prior configuration.
func (t *sessionTx) CheckAssistantChatModel() error {
	var assistant bool
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(assistant_agent_id=$2,false) FROM platform_workspaces WHERE id=$1`, t.target.Workspace, t.state.AgentID).Scan(&assistant); err != nil || !assistant {
		return err
	}
	cfg, err := t.modelConfiguration("chat")
	if err != nil {
		return err
	}
	old := t.state.Session.Configuration
	if old.ModelProfileID == cfg.ModelProfileID && old.PlatformModelID == cfg.PlatformModelID && old.ModelVersion == cfg.ModelVersion {
		return nil
	}
	if t.state.Session.Execution.State != "idle" {
		return service.Fail("assistant_model_change_pending", 409, "请等当前轮处理结束后重试")
	}
	old.ModelProfileID, old.PlatformModelID, old.ModelVersion = cfg.ModelProfileID, cfg.PlatformModelID, cfg.ModelVersion
	old.Protocol, old.BaseURL, old.ModelID = cfg.Protocol, cfg.BaseURL, cfg.ModelID
	raw, err := json.Marshal(old)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET configuration=$2 WHERE id=$1`, t.target.Session, raw)
	return err
}

func (t *sessionTx) InsertTask(id string, source *string) error {
	if t.target.TaskSpaceID != "" {
		_, err := t.tx.ExecContext(t.ctx, `INSERT INTO manager_tasks(id,workspace_id,task_space_id,agent_id)VALUES($1,$2,$3,$4)`, id, t.target.Workspace, t.target.TaskSpaceID, t.state.AgentID)
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO manager_tasks(id,workspace_id,conversation_id,source_chat_session_id,source_chat_input_id,agent_id) VALUES($1,$2,$3,$4,(SELECT COALESCE((SELECT u.id FROM manager_inputs u WHERE u.turn_id=t.id AND u.source_kind='user' AND u.trigger_agent_loop AND u.context_state='committed' ORDER BY u.seq DESC LIMIT 1),t.input_id) FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id WHERE s.id=$4),$5)`, id, t.target.Workspace, t.target.Conversation, source, t.state.AgentID)
	return err
}
func (t *sessionTx) InsertSession(v service.Session) error {
	cfg, err := json.Marshal(v.Configuration)
	if err != nil {
		return err
	}
	var webActor any = v.CreatedBy
	var channelActor any
	if a, ok := service.ChannelActorFrom(t.ctx); ok {
		webActor = nil
		channelActor = a.ID
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,task_id,created_by,mode,title,configuration,created_at,channel_created_by,task_space_id,task_space_agent_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,(SELECT admission_epoch FROM manager_task_space_agents WHERE task_space_id=$12 AND agent_id=$3))`, v.ID, v.WorkspaceID, v.AgentID, nullID(v.ConversationID), v.TaskID, webActor, v.Mode, v.Title, cfg, v.CreatedAt, channelActor, v.TaskSpaceID)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.ConstraintName == "manager_chat_unique" {
		return service.Fail("chat_session_exists", 409, "业务会话已有主对话")
	}
	if err != nil {
		return err
	}
	t.target.Session = v.ID
	return t.refresh()
}
func (t *sessionTx) SetInitialInput(id string) error {
	_, err := t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET initial_input_id=$2 WHERE id=$1`, t.target.Session, id)
	return err
}
func (t *sessionTx) InsertInput(v service.Input) (service.Input, error) {
	if v.TriggerAgentLoop == nil {
		enabled := true
		v.TriggerAgentLoop = &enabled
	}
	if *v.TriggerAgentLoop && v.DeliveryMode == "steer" && t.state.Session.Execution.ActiveTurnID != nil {
		var compatible bool
		// An active or queued report already forces this input to wait for a
		// later Turn. Keep its steer mode for subsequent ordinary chat inputs.
		if err := t.tx.QueryRowContext(t.ctx, `SELECT i.source_kind='task_report'
 OR COALESCE(i.submitted_by::text,i.channel_principal_id::text)=$2
 OR EXISTS(SELECT FROM manager_inputs r WHERE r.session_id=t.session_id AND r.status='queued' AND r.trigger_agent_loop AND r.source_kind='task_report')
 FROM manager_turns t JOIN manager_inputs i ON i.id=t.input_id WHERE t.id=$1`, *t.state.Session.Execution.ActiveTurnID, t.target.Actor).Scan(&compatible); err != nil {
			return v, err
		}
		if !compatible {
			return v, service.Fail("steer_actor_conflict", 409, "当前轮属于其他主体，请使用 queue")
		}
	}
	if _, ok := service.ChannelActorFrom(t.ctx); ok && t.state.Session.Mode == "chat" {
		if err := flushHumanDMContext(t.ctx, t.tx, t.target.Workspace, t.target.Session, t.target.Conversation); err != nil {
			return v, err
		}
	}
	var err error
	v.Text, err = t.channelInputText(v)
	if err != nil {
		return v, err
	}
	historyCount, err := t.insertChannelHistory(v)
	if err != nil {
		return v, err
	}
	if err := t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, t.target.Session).Scan(&v.Seq); err != nil {
		return v, err
	}
	var webActor any = v.SubmittedBy
	var channelActor any
	var generation any
	var receipt any = "pending"
	if v.DeliveryMode == "" {
		v.DeliveryMode = "steer"
	}
	if v.SourceKind == "" {
		v.SourceKind = "user"
	}
	v.PrincipalType = "web"
	if a, ok := service.ChannelActorFrom(t.ctx); ok {
		webActor = nil
		channelActor = a.ID
		generation = a.Generation
		if a.NeedsContextReceipt {
			receipt = "pending"
		}
		v.SubmittedBy = ""
		v.ChannelPrincipalID = &a.ID
		v.PrincipalType = "channel"

	}
	if v.Attachments == nil {
		v.Attachments = []service.Attachment{}
	}
	attachments, err := json.Marshal(v.Attachments)
	if err != nil {
		return v, err
	}
	if err = t.checkAttachmentSource(v.Attachments); err != nil {
		return v, err
	}
	var delegatedFrom any
	if t.delegationInput != nil {
		delegatedFrom = t.target.SourceInputID
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,created_at,channel_principal_id,channel_generation,context_state,channel_history_count,delivery_mode,source_kind,report_id,trigger_agent_loop,confirmation_request_id,confirmed,attachments,delegated_from_input_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, v.ID, t.target.Workspace, v.SessionID, webActor, t.state.AdmissionEpoch, v.Seq, v.Text, v.CreatedAt, channelActor, generation, receipt, historyCount, v.DeliveryMode, v.SourceKind, v.ReportID, *v.TriggerAgentLoop, v.ConfirmationRequestID, v.Confirmed, attachments, delegatedFrom)
	if err == nil {
		err = t.stampWebGroupInput(v.ID)
	}
	if err == nil {
		if a, ok := service.ChannelActorFrom(t.ctx); ok && a.InboxID != "" {
			err = acceptChannelInput(t.ctx, t.tx, a.InboxID, v, !a.ConfirmationOnly && v.SourceKind == "user" && *v.TriggerAgentLoop)
			if err == nil && a.ConfirmationOnly {
				_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_human_chat_inputs(input_id,inbox_id) VALUES($1,$2)`, v.ID, a.InboxID)
			}
		}
	}
	return v, err
}
func (t *sessionTx) FindDedup(v service.Dedup) (*service.Dedup, error) {
	var found service.Dedup
	err := t.tx.QueryRowContext(t.ctx, `SELECT fingerprint,session_id,input_id FROM manager_input_dedup WHERE workspace_id=$1 AND actor_id=$2 AND operation_kind=$3 AND target=$4 AND request_key=$5`, t.target.Workspace, t.target.Actor, v.Kind, v.Target, v.Key).Scan(&found.Fingerprint, &found.SessionID, &found.InputID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &found, err
}
func (t *sessionTx) RecordDedup(v service.Dedup) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO manager_input_dedup(workspace_id,actor_id,operation_kind,target,request_key,fingerprint,session_id,input_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, t.target.Workspace, t.target.Actor, v.Kind, v.Target, v.Key, v.Fingerprint, v.SessionID, v.InputID)
	return err
}

const inputColumns = `id,session_id,COALESCE(submitted_by::text,'') AS submitted_by,seq,text,status,turn_id,cancel_reason,rejection_reason,created_at,started_at,finished_at,channel_principal_id,delivery_mode,source_kind,report_id,trigger_agent_loop,trigger_event_id,notification_id,confirmation_request_id,confirmed,attachments`

func scanInput(row scanner) (service.Input, error) {
	var v service.Input
	var attachments []byte
	err := row.Scan(&v.ID, &v.SessionID, &v.SubmittedBy, &v.Seq, &v.Text, &v.Status, &v.TurnID, &v.CancelReason, &v.RejectionReason, &v.CreatedAt, &v.StartedAt, &v.FinishedAt, &v.ChannelPrincipalID, &v.DeliveryMode, &v.SourceKind, &v.ReportID, &v.TriggerAgentLoop, &v.TriggerEventID, &v.NotificationID, &v.ConfirmationRequestID, &v.Confirmed, &attachments)
	if err == nil {
		err = json.Unmarshal(attachments, &v.Attachments)
	}
	if v.SourceKind == "work_item_assignment" || v.SourceKind == "work_item_amendment" {
		v.PrincipalType = "work_item"
	} else if v.NotificationID != nil {
		v.PrincipalType = "notification"
	} else if v.TriggerEventID != nil {
		v.PrincipalType = "trigger"
	} else if v.ChannelPrincipalID != nil {
		v.PrincipalType = "channel"

	} else {
		v.PrincipalType = "web"
	}
	return v, visibleError(err)
}
func (t *sessionTx) Input(id string) (service.Input, error) {
	return scanInput(t.tx.QueryRowContext(t.ctx, `SELECT `+inputColumns+` FROM manager_inputs WHERE session_id=$1 AND id=$2`, t.target.Session, id))
}
func (t *sessionTx) CancelInput(id, reason string) (service.Input, error) {
	return scanInput(t.tx.QueryRowContext(t.ctx, `UPDATE manager_inputs SET status='canceled',cancel_reason=$3,finished_at=now() WHERE session_id=$1 AND id=$2 AND status='queued' RETURNING `+inputColumns, t.target.Session, id, reason))
}
func (t *sessionTx) CancelQueued(reason string) ([]service.Input, error) {
	rows, err := t.tx.QueryContext(t.ctx, `UPDATE manager_inputs SET status='canceled',cancel_reason=$2,finished_at=now() WHERE session_id=$1 AND status='queued' RETURNING `+inputColumns, t.target.Session, reason)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []service.Input{}
	for rows.Next() {
		v, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (t *sessionTx) RejectUnavailableInputs() ([]service.Input, error) {
	rows, err := t.tx.QueryContext(t.ctx, `WITH rejected AS (UPDATE manager_inputs i SET status='rejected',rejection_reason=CASE WHEN NOT $2 OR i.admission_epoch<>$3 THEN 'agent_disabled' ELSE 'access_revoked' END,finished_at=now() WHERE i.session_id=$1 AND i.status='queued' AND (NOT $2 OR i.admission_epoch<>$3 OR NOT (manager_input_admission_allowed(i.id) OR manager_human_chat_input_allowed(i.id))) RETURNING `+inputColumns+`) SELECT `+inputColumns+` FROM rejected ORDER BY seq`, t.target.Session, t.state.AgentEnabled, t.state.AdmissionEpoch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned below.
	items := []service.Input{}
	for rows.Next() {
		v, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (t *sessionTx) UpdateTitle(title *string, revision int64) error {
	_, err := t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET title=$2,revision=revision+1 WHERE id=$1 AND revision=$3`, t.target.Session, title, revision)
	return err
}
func (t *sessionTx) Stop(archive bool) error {
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE manager_work_items SET status='cancelled',version=version+1,updated_at=now() WHERE root_session_id=$1 AND status IN ('working','awaiting_acceptance','blocked')`, t.target.Session); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE manager_work_boundaries SET state='cancelled' WHERE session_id=$1 AND state IN ('preparing','safe','blocked')`, t.target.Session); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `UPDATE manager_sessions SET required_actions='[]',stop_input_seq=last_input_seq,stop_revision=stop_revision+1,applied_stop_revision=CASE WHEN engine_id IS NULL AND active_turn_id IS NULL AND COALESCE(background_process_count,1)=0 THEN stop_revision+1 ELSE applied_stop_revision END,lifecycle=CASE WHEN $2 THEN 'archived' ELSE lifecycle END,archived_at=CASE WHEN $2 THEN now() ELSE archived_at END,revision=revision+CASE WHEN $2 THEN 1 ELSE 0 END WHERE id=$1`, t.target.Session, archive)
	if err != nil {
		return err
	}

	if _, err = t.tx.ExecContext(t.ctx, `UPDATE manager_human_requests SET state='cancelled' WHERE session_id=$1 AND state IN ('preparing','pending','blocked')`, t.target.Session); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE manager_turns SET status='stopping' WHERE session_id=$1 AND status='running'`, t.target.Session)
	return err
}
func (t *sessionTx) ArchiveConversation() ([]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 ORDER BY id FOR UPDATE`, t.target.Conversation)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	// The caller holds the parent FOR UPDATE; Claim holds it FOR SHARE.
	// Lock children before reading activity so an in-flight Engine write is visible.
	if t.state.Conversation.Lifecycle != "archived" {
		var busy bool
		err = t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM manager_sessions s WHERE conversation_id=$1 AND (active_turn_id IS NOT NULL OR COALESCE(background_process_count,1)<>0 OR (block_reason IS NOT NULL AND block_reason<>'remote_tool_outcome_unknown') OR stop_revision>applied_stop_revision OR EXISTS(SELECT 1 FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued')))`, t.target.Conversation).Scan(&busy)
		if err != nil {
			return nil, err
		}
		if busy {
			return nil, service.Fail("conversation_busy", 409, "业务会话或关联任务仍在运行、排队或等待停止确认，请先停止并等待收尾；存在未知活动时须先核对处理后再归档")
		}
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE manager_conversations SET lifecycle='archived',archived_at=COALESCE(archived_at,now()) WHERE id=$1`, t.target.Conversation)
	return ids, err
}
func (t *sessionTx) Event(kind string, input, turn *string, fields map[string]any) error {
	var seq int64
	if err := t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq`, t.target.Session).Scan(&seq); err != nil {
		return err
	}
	fields["type"] = kind
	fields["schema_version"] = 1
	fields["session_id"] = t.target.Session
	fields["input_id"] = input
	fields["turn_id"] = turn
	fields["seq"] = strconv.FormatInt(seq, 10)
	fields["occurred_at"] = time.Now().UTC()
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event) VALUES($1,$2,$3,$4)`, t.target.Workspace, t.target.Session, seq, data)
	return err
}
func (t *sessionTx) SnapshotEvent() error {
	if err := t.refresh(); err != nil {
		return err
	}
	v := t.state.Session
	return t.Event("session.updated", nil, nil, map[string]any{"planning_state": v.PlanningState, "planning_version": strconv.FormatInt(v.PlanningVersion, 10), "accepts_input": v.AcceptsInput, "input_block_reason": v.InputBlockReason, "title": v.Title, "lifecycle": v.Lifecycle, "revision": strconv.FormatInt(v.Revision, 10), "archived_at": v.ArchivedAt, "execution": v.Execution})
}
