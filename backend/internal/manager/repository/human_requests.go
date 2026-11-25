package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Store) HumanRecipients(ctx context.Context, workspace, actor, agent string, update *service.HumanRecipients) (service.HumanRecipients, error) {
	var out service.HumanRecipients
	if !service.ValidID(workspace) || !service.ValidID(agent) {
		return out, service.Fail("invalid_reference", 400, "员工标识无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit handles success.
	// Serialize recipient edits with WeCom re-authentication, which refreshes
	// saved recipient proofs under the binding lock. Lock before the agent.
	if update != nil {
		if _, err = tx.ExecContext(ctx, `SELECT b.id FROM platform_channel_bindings b JOIN platform_members m ON m.workspace_id=b.workspace_id
 WHERE b.workspace_id=$1 AND b.agent_id=$2 AND b.provider='wecom' AND m.user_id=$3 AND m.status='active' AND m.role IN ('owner','admin') FOR SHARE OF b`, workspace, agent, actor); err != nil {
			return out, err
		}
	}
	var authorized string
	err = tx.QueryRowContext(ctx, `SELECT a.id FROM platform_agents a JOIN platform_members m ON m.workspace_id=a.workspace_id
 WHERE a.workspace_id=$1 AND m.user_id=$2 AND a.id=$3 AND m.status='active' AND m.role IN ('owner','admin') FOR UPDATE OF a`, workspace, actor, agent).Scan(&authorized)
	if err != nil {
		return out, visibleError(err)
	}
	out, err = readHumanRecipients(ctx, tx, agent)
	if err != nil || update == nil {
		return out, err
	}
	if update.Version != out.Version {
		return out, service.Fail("human_recipients_changed", 409, "确认人配置已更新，请刷新后编辑")
	}
	if err = validateHumanRecipients(ctx, tx, workspace, agent, out.Recipients, update.Recipients); err != nil {
		return out, err
	}
	raw, err := json.Marshal(update.Recipients)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_agent_human_recipients(agent_id,version,recipients) VALUES($1,$2,$3)
 ON CONFLICT(agent_id) DO UPDATE SET version=EXCLUDED.version,recipients=EXCLUDED.recipients`, agent, out.Version+1, raw)
	if err != nil {
		return out, err
	}
	return service.HumanRecipients{Version: out.Version + 1, Recipients: update.Recipients}, tx.Commit()
}

func readHumanRecipients(ctx context.Context, tx *sql.Tx, agent string) (service.HumanRecipients, error) {
	return scanHumanRecipients(tx.QueryRowContext(ctx, `SELECT version,recipients FROM platform_agent_human_recipients WHERE agent_id=$1`, agent))
}

func eligibleHumanRecipients(ctx context.Context, tx *sql.Tx, workspace, agent string) (service.HumanRecipients, error) {
	return scanHumanRecipients(tx.QueryRowContext(ctx, `SELECT h.version,COALESCE((
 SELECT jsonb_agg(r.item || jsonb_build_object('name',eligible.name) ORDER BY r.ord)
 FROM jsonb_array_elements(h.recipients) WITH ORDINALITY r(item,ord)
 CROSS JOIN LATERAL (SELECT manager_human_recipient_name($1,h.agent_id,r.item->'ref') AS name) eligible
 WHERE eligible.name IS NOT NULL
 ),'[]'::jsonb) FROM platform_agent_human_recipients h WHERE h.agent_id=$2`, workspace, agent))
}

func scanHumanRecipients(row scanner) (service.HumanRecipients, error) {
	out := service.HumanRecipients{Recipients: []service.HumanRecipient{}}
	var data []byte
	err := row.Scan(&out.Version, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out.Recipients)
	return out, err
}

func validateHumanRecipients(ctx context.Context, tx *sql.Tx, workspace, agent string, previous, next []service.HumanRecipient) error {
	if next == nil || len(next) > 40 {
		return service.Fail("invalid_human_recipients", 400, "请提供确认人列表，最多 40 人")
	}
	previousRules := make(map[platform.SelectionRef]service.HumanRecipient, len(previous))
	for _, candidate := range previous {
		previousRules[humanRecipientKey(candidate.Ref)] = candidate
	}
	seen := make(map[platform.SelectionRef]bool, len(next))
	defaultSet := false
	for i := range next {
		candidate := &next[i]
		candidate.Scope = strings.TrimSpace(candidate.Scope)
		ref, valid := canonicalHumanRecipient(candidate.Ref, workspace, agent)
		key := humanRecipientKey(ref)
		if !valid || seen[key] || (!candidate.IsDefault && candidate.Scope == "") || utf8.RuneCountInString(candidate.Scope) > 1000 || strings.ContainsRune(candidate.Scope, 0) {
			return service.Fail("invalid_human_recipient", 400, "请选择可核验的真人，普通确认人须填写负责范围")
		}
		if candidate.IsDefault && defaultSet {
			return service.Fail("invalid_human_recipients", 400, "每位员工只能设置一名默认确认人")
		}
		defaultSet = defaultSet || candidate.IsDefault
		seen[key] = true
		candidate.Ref = ref
		if err := resolveHumanRecipient(ctx, tx, candidate, previousRules[key]); err != nil {
			return err
		}
	}
	return nil
}

func canonicalHumanRecipient(ref platform.SelectionRef, workspace, agent string) (platform.SelectionRef, bool) {
	if ref.Kind != "user" || ref.WorkspaceID != workspace || ref.AgentID != agent ||
		ref.ExternalID == "" || len(ref.ExternalID) > 512 || strings.ContainsAny(ref.ExternalID, "\x00\r\n") {
		return ref, false
	}
	clean := platform.SelectionRef{Kind: "user", Provider: ref.Provider, WorkspaceID: workspace, AgentID: agent, ExternalID: ref.ExternalID}
	switch ref.Provider {
	case "web":
		return clean, service.ValidID(ref.ExternalID) && ref.BindingID == "" && ref.TenantID == "" && ref.IdentityEpoch == 0 && ref.Generation == 0
	case "feishu", "dingtalk", "wecom":
		clean.BindingID, clean.TenantID, clean.Namespace = ref.BindingID, ref.TenantID, ref.Namespace
		clean.IdentityEpoch, clean.Generation = ref.IdentityEpoch, ref.Generation
		return clean, service.ValidID(ref.BindingID) && ref.TenantID != "" && ref.IdentityEpoch > 0 && ref.Generation > 0
	default:
		return ref, false
	}
}

func humanRecipientKey(ref platform.SelectionRef) platform.SelectionRef {
	// Selecting the same identity after a credential refresh updates its proof.
	ref.Generation = 0
	return ref
}

func resolveHumanRecipient(ctx context.Context, tx *sql.Tx, candidate *service.HumanRecipient, previous service.HumanRecipient) error {
	ref := candidate.Ref
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	var name sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT manager_human_recipient_name($1,$2,$3)`, ref.WorkspaceID, ref.AgentID, raw).Scan(&name); err != nil {
		return err
	}
	if !name.Valid {
		if previous.ID == "" || previous.Ref != ref || previous.Scope != candidate.Scope || candidate.IsDefault && !previous.IsDefault {
			return service.Fail("human_recipient_forbidden", 403, "该确认人当前无权使用员工")
		}
		candidate.Name = previous.Name
	} else {
		candidate.Name = name.String
	}
	candidate.ID = previous.ID
	if candidate.ID == "" {
		candidate.ID = service.NewID()
	}
	return nil
}

const privateHumanInput = `(i.source_kind='user' AND (c.web_user_id=i.submitted_by OR
 (c.channel_chat_type='direct' AND c.channel_thread_id='' AND c.channel_created_by=i.channel_principal_id)))`

const webHumanRecipientConfigured = `EXISTS(SELECT FROM platform_agent_human_recipients h
 WHERE h.agent_id=s.agent_id AND h.recipients @> jsonb_build_array(jsonb_build_object('ref',jsonb_build_object(
 'kind','user','provider','web','external_id',i.submitted_by))))`

// Keep confirmation tools off ordinary channel chats that have no matching rule.
const channelHumanRecipientConfigured = `EXISTS(SELECT 1 FROM manager_channel_principals p
 JOIN platform_agent_human_recipients h ON h.agent_id=s.agent_id WHERE p.id=i.channel_principal_id
 AND h.recipients @> jsonb_build_array(jsonb_build_object('ref',jsonb_build_object(
  'provider',p.provider,'binding_id',p.binding_id,'identity_epoch',p.identity_epoch,
  'tenant_id',p.external_tenant_id,'external_id',p.external_user_id))))`

const humanActorQuery = ` FROM manager_sessions s
 JOIN manager_turns t ON t.id=s.active_turn_id JOIN manager_inputs i ON i.id=t.input_id
 JOIN platform_agents a ON a.id=s.agent_id LEFT JOIN manager_conversations c ON c.id=s.conversation_id
 LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND i.id=$5
 AND s.lifecycle='active' AND COALESCE(c.lifecycle,p.lifecycle)='active' AND a.enabled
 AND s.stop_revision=s.applied_stop_revision AND i.status='started' AND i.admission_epoch=a.admission_epoch
 AND (manager_input_actor_allowed(i.id) OR manager_human_chat_input_allowed(i.id))
 AND (s.mode='task' OR ` + privateHumanInput + `)`

func lockHumanRuntime(ctx context.Context, tx *sql.Tx, v service.HumanRuntimeScope) (service.HumanRuntimeScope, error) {
	id := v.Identity
	err := tx.QueryRowContext(ctx, `SELECT s.workspace_id,s.agent_id,t.id,s.mode,COALESCE(i.submitted_by::text,''),COALESCE(i.channel_principal_id::text,''),i.text,
 COALESCE((SELECT r.id::text FROM manager_human_requests r WHERE r.id=i.human_request_id AND r.resume_input_id=i.id
 AND r.state='revised' AND r.answer->>'decision'='revise'),'')`+humanActorQuery+
		` FOR UPDATE OF s FOR SHARE OF a`, id.SessionID, id.EngineID, id.AssignmentID, id.Generation, v.InputID).
		Scan(&v.Workspace, &v.Agent, &v.Turn, &v.Mode, &v.User, &v.ChannelPrincipal, &v.Text, &v.CorrectionRequestID)
	return v, visibleError(err)
}

func (s *Store) HumanTaskTool(ctx context.Context, scope service.HumanRuntimeScope, operation, requestID string, question *service.HumanQuestion) (any, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit handles success.
	scope, err = lockHumanRuntime(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	if scope.Mode != "task" {
		return nil, service.Fail("human_tool_forbidden", 403, "此操作仅供后台任务执行")
	}
	switch operation {
	case "recipients":
		rules, err := eligibleHumanRecipients(ctx, tx, scope.Workspace, scope.Agent)
		if err != nil {
			return nil, err
		}
		return publicHumanRecipients(rules), nil
	case "request":
		request, err := createHumanRequest(ctx, tx, scope, question)
		if err != nil {
			return nil, err
		}
		return request, tx.Commit()
	case "fenced":
		return scanHumanRequest(tx.QueryRowContext(ctx, `SELECT id,version,state,question,fenced_at IS NOT NULL FROM manager_human_requests
 WHERE id=$1 AND session_id=$2 AND turn_id=$3`, requestID, scope.Identity.SessionID, scope.Turn))
	default:
		return nil, service.Fail("invalid_human_operation", 400, "确认工具操作无效")
	}
}

func publicHumanRecipients(rules service.HumanRecipients) map[string]any {
	items := make([]map[string]string, 0, len(rules.Recipients))
	var fallback map[string]string
	for _, candidate := range rules.Recipients {
		if candidate.Scope != "" {
			items = append(items, map[string]string{"candidateId": candidate.ID, "questionScope": candidate.Scope})
		}
		if candidate.IsDefault {
			fallback = map[string]string{"candidateId": candidate.ID, "questionScope": candidate.Scope}
		}
	}
	return map[string]any{"version": strconv.FormatInt(rules.Version, 10), "items": items, "default_recipient": fallback}
}

func scanHumanRequest(row scanner) (service.HumanRequest, error) {
	var v service.HumanRequest
	var q []byte
	err := row.Scan(&v.ID, &v.Version, &v.State, &q, &v.Fenced)
	if err != nil {
		return v, visibleError(err)
	}
	v.Code = service.HumanShortCode(v.ID, v.Version)
	digest := sha256.Sum256(q)
	v.QuestionDigest = hex.EncodeToString(digest[:])
	err = json.Unmarshal(q, &v.Question)
	return v, err
}

func createHumanRequest(ctx context.Context, tx *sql.Tx, scope service.HumanRuntimeScope, question *service.HumanQuestion) (service.HumanRequest, error) {
	var workPending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_work_boundaries WHERE turn_id=$1)`, scope.Turn).Scan(&workPending); err != nil {
		return service.HumanRequest{}, err
	}
	if workPending {
		return service.HumanRequest{}, service.Fail("work_boundary_conflict", 409, "本轮已提交事项结束动作")
	}
	var out service.HumanRequest
	if question == nil {
		return out, service.Fail("invalid_human_question", 400, "确认问题不能为空")
	}
	if err := question.Validate(); err != nil {
		return out, err
	}
	if scope.CorrectionRequestID != "" && question.AnswerType != "decision" {
		return out, service.Fail("human_reapproval_required", 409, "请为修改后的方案提交需要明确批准的问题")
	}
	existing, err := scanHumanRequest(tx.QueryRowContext(ctx, `SELECT id,version,state,question,fenced_at IS NOT NULL FROM manager_human_requests WHERE turn_id=$1`, scope.Turn))
	if err == nil {
		comparison := *question
		// The new tool omits scope. For an exact preparing replay, reuse the
		// scope already captured by that candidate/version, not today's rules.
		if comparison.QuestionScope == "" && comparison.CandidateID != "" &&
			comparison.CandidateID == existing.Question.CandidateID && comparison.RulesVersion == existing.Question.RulesVersion {
			comparison.QuestionScope = existing.Question.QuestionScope
		}
		if reflect.DeepEqual(existing.Question, comparison) && existing.State == "preparing" {
			return existing, nil
		}
		return out, service.Fail("human_request_conflict", 409, "本轮已有确认请求")
	}
	if !isMissing(err) {
		return out, err
	}
	if err = question.ValidateNewMode(); err != nil {
		return out, err
	}
	rules, err := eligibleHumanRecipients(ctx, tx, scope.Workspace, scope.Agent)
	if err != nil {
		return out, err
	}
	candidate, err := selectHumanRecipient(rules, *question)
	if err != nil {
		return out, err
	}
	if question.CandidateID != "" && question.QuestionScope == "" {
		question.QuestionScope = candidate.Scope
	}
	recipient, err := json.Marshal(candidate.Ref)
	if err != nil {
		return out, err
	}
	encodedQuestion, err := json.Marshal(question)
	if err != nil {
		return out, err
	}
	out = service.HumanRequest{ID: service.NewID(), Version: 1, State: "preparing", Question: *question}
	session := scope.Identity.SessionID
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,authority_input_id,recipient,rules_version,question,stop_revision)
 SELECT $1,s.workspace_id,s.agent_id,s.id,$3,i.id,COALESCE(prior.authority_input_id,i.id),$5,$6,$7,s.stop_revision
 FROM manager_sessions s JOIN manager_inputs i ON i.id=$4 LEFT JOIN manager_human_requests prior ON prior.id=i.human_request_id WHERE s.id=$2`,
		out.ID, session, scope.Turn, scope.InputID, recipient, rules.Version, encodedQuestion)
	if err != nil {
		return out, err
	}
	// Transfer only the gate belonging to this revision. Both the replacement
	// and the new request commit together so queued work never sees an open gate.
	var updated string
	err = tx.QueryRowContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((
 SELECT jsonb_agg(q ORDER BY ord) FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)
 WHERE q->>'type' IS DISTINCT FROM 'human_confirmation' OR q->>'request_id' IS DISTINCT FROM $4
 ),'[]') || jsonb_build_array(jsonb_build_object('type','human_confirmation','request_id',$2::text,'turn_id',$3::text,'title','找人确认','message','正在停稳'))
 WHERE id=$1 AND ($4='' OR required_actions @> jsonb_build_array(jsonb_build_object('type','human_confirmation','request_id',$4::text)))
 RETURNING id`, session, out.ID, scope.Turn, scope.CorrectionRequestID).Scan(&updated)
	if isMissing(err) {
		return out, service.Fail("human_reapproval_changed", 409, "原整改的确认门禁已变化")
	}
	if err != nil {
		return out, err
	}
	st := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: scope.Workspace, Session: session}}
	return out, st.SnapshotEvent()
}

func selectHumanRecipient(rules service.HumanRecipients, q service.HumanQuestion) (service.HumanRecipient, error) {
	if rules.Version == q.RulesVersion {
		for _, candidate := range rules.Recipients {
			if candidate.ID == q.RecipientID {
				return candidate, nil
			}
		}
	}
	return service.HumanRecipient{}, service.Fail("human_recipient_changed", 409, "确认人配置或权限已变化，请重新查询候选")
}
