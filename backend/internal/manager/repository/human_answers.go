package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const humanRecipientStillConfigured = `EXISTS(SELECT 1 FROM platform_agent_human_recipients h WHERE h.agent_id=r.agent_id
 AND h.recipients @> jsonb_build_array(jsonb_build_object('id',r.question->>'recipient_id','ref',r.recipient)))`

const humanAnswerTarget = `r.workspace_id=$1 AND ($3='' OR r.agent_id::text=$3)
 AND (NOT $4 OR CASE WHEN $5::text<>'' THEN (manager_input_actor_allowed(NULLIF($5::text,'')::uuid)
 OR manager_human_chat_input_allowed(NULLIF($5::text,'')::uuid))
 ELSE manager_human_channel_inbox_current(NULLIF($6::text,'')::uuid,$2) END)
 AND manager_human_recipient_matches(r.workspace_id,r.agent_id,r.recipient,$2,$4) AND ` + humanRecipientStillConfigured

const humanRequestActive = `s.lifecycle='active' AND COALESCE(c.lifecycle,p.lifecycle)='active'
 AND s.stop_revision=r.stop_revision AND manager_input_actor_allowed(r.authority_input_id)`

type humanViewer struct {
	workspace, actor, agent, input, inbox string
	channel                               bool
}

func (v humanViewer) queryArgs() []any {
	return []any{v.workspace, v.actor, v.agent, v.channel, v.input, v.inbox}
}

const humanInboxFrom = ` FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE ` + humanAnswerTarget + ` AND r.state='pending'
 AND ` + humanRequestActive + ` AND (COALESCE(cardinality($7::text[]),0)=0 OR r.id::text=ANY($7)
 OR 'h-'||left(encode(sha256(convert_to(r.id::text||CASE WHEN r.version=1 THEN '' ELSE ':'||r.version::text END,'UTF8')),'hex'),12)=ANY($7))`

func readHumanInbox(ctx context.Context, db humanQueryer, viewer humanViewer, locators []string, offset, limit int) ([]service.HumanRequest, int, error) {
	var total int
	rows, err := db.QueryContext(ctx, `SELECT r.id,r.version,r.state,r.question,true,count(*) OVER()
`+humanInboxFrom+` ORDER BY r.created_at,r.id LIMIT $9 OFFSET $8`,
		append(viewer.queryArgs(), locators, offset, limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck // Iteration reports read failures.
	items := []service.HumanRequest{}
	for rows.Next() {
		v, err := scanHumanRequest(appendedScanner{scanner: rows, extra: []any{&total}})
		if err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	if offset > 0 && len(items) == 0 {
		err = db.QueryRowContext(ctx, "SELECT count(*)"+humanInboxFrom, append(viewer.queryArgs(), locators)...).Scan(&total)
	}
	return items, total, err
}

func (s *Store) HumanInbox(ctx context.Context, workspace, actor string) ([]service.HumanRequest, error) {
	items, _, err := s.HumanInboxPage(ctx, workspace, actor, 0, "")
	return items, err
}
func (s *Store) HumanInboxPage(ctx context.Context, workspace, actor string, offset int, id string) ([]service.HumanRequest, int, error) {
	if !service.ValidID(workspace) {
		return nil, 0, service.Fail("invalid_reference", 400, "工作区标识无效")
	}
	var allowed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_members WHERE workspace_id=$1 AND user_id=$2 AND status='active')`,
		workspace, actor).Scan(&allowed)
	if err != nil {
		return nil, 0, err
	}
	if !allowed {
		return nil, 0, service.Fail("workspace_forbidden", 403, "工作区不可访问")
	}
	viewer := humanViewer{workspace: workspace, actor: actor}
	return readHumanInbox(ctx, s.DB, viewer, humanListLocators(id), offset, 100)
}

func humanListLocators(id string) []string {
	if id == "" {
		return nil
	}
	return []string{strings.ToLower(id)}
}

func lockHumanChat(ctx context.Context, tx *sql.Tx, scope service.HumanRuntimeScope) (service.HumanRuntimeScope, humanViewer, error) {
	scope, err := lockHumanRuntime(ctx, tx, scope)
	if err != nil {
		return scope, humanViewer{}, err
	}
	if scope.Mode != "chat" || (scope.User == "" && scope.ChannelPrincipal == "") {
		return scope, humanViewer{}, service.Fail("human_tool_forbidden", 403, "请在员工主私聊处理确认")
	}
	viewer := humanViewer{workspace: scope.Workspace, actor: scope.User, agent: scope.Agent, input: scope.InputID}
	if scope.ChannelPrincipal != "" {
		viewer.actor, viewer.channel = scope.ChannelPrincipal, true
	}
	return scope, viewer, nil
}

func (s *Store) HumanChatList(ctx context.Context, scope service.HumanRuntimeScope, query service.HumanListQuery) (any, error) {
	page, size, err := query.Pagination()
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Lock is released after the read.
	scope, viewer, err := lockHumanChat(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	items, total, err := readHumanInbox(ctx, tx, viewer, humanListLocators(query.RequestID), (page-1)*size, size)
	if err != nil {
		return nil, err
	}
	evidence, err := readHumanReplyContext(ctx, tx, viewer, scope.Text)
	if err != nil {
		return nil, err
	}
	related := []service.HumanRequest{}
	if ids := evidence.referencedLocators(); len(ids) != 0 {
		related, _, err = readHumanInbox(ctx, tx, viewer, ids, 0, 20)
		if err != nil {
			return nil, err
		}
	}
	pages := (total + size - 1) / size
	hasMore := page < pages
	return map[string]any{
		"items": items, "page": page, "pageSize": size, "total": total, "totalPages": pages,
		"hasMore": hasMore, "truncated": page > 1 || hasMore, "currentMessage": evidence, "referencedRequests": related,
	}, nil
}

func (s *Store) HumanChatTool(ctx context.Context, scope service.HumanRuntimeScope, operation, requestID string, answer *service.HumanAnswer) (any, error) {
	if operation == "pending" {
		return s.HumanChatList(ctx, scope, service.HumanListQuery{RequestID: requestID})
	}
	if operation != "answer" && operation != "propose" {
		return nil, service.Fail("invalid_human_operation", 400, "确认工具操作无效")
	}
	if answer == nil {
		return nil, service.Fail("invalid_human_answer", 400, "请填写答复")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit handles success.
	scope, viewer, err := lockHumanChat(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	result, err := proposeHumanReply(ctx, tx, viewer, scope, requestID, *answer)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (s *Store) AnswerHumanRequest(ctx context.Context, workspace, actor, requestID string, answer service.HumanAnswer) (any, error) {
	if !service.ValidID(workspace) || !service.ValidID(requestID) {
		return nil, service.Fail("invalid_reference", 400, "确认请求标识无效")
	}
	if answer.Association != "" || answer.QuestionDigest != "" || len(answer.EvidenceSegmentIDs) != 0 || answer.ClarificationText != "" {
		return nil, service.Fail("invalid_human_answer", 400, "请提交当前表单的答复")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit handles success.
	result, err := recordHumanAnswer(ctx, tx, humanViewer{workspace: workspace, actor: actor}, requestID, answer)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func recordHumanAnswer(ctx context.Context, tx *sql.Tx, viewer humanViewer, id string, answer service.HumanAnswer) (any, error) {
	args := append(viewer.queryArgs(), id)
	var session string
	err := tx.QueryRowContext(ctx, `SELECT s.id FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 WHERE r.id=$7 AND `+humanAnswerTarget+` FOR UPDATE OF s`, args...).Scan(&session)
	if err != nil {
		return nil, visibleError(err)
	}
	var state string
	var version int64
	var available, scopeCurrent bool
	var previous []byte
	var kind string
	var rawQuestion []byte
	err = tx.QueryRowContext(ctx, `SELECT r.state,r.version,r.question->>'answer_type',r.question,r.answer,
 s.active_turn_id IS NULL AND s.applied_stop_revision=s.stop_revision
 AND (`+humanRequestActive+`) AND (`+humanAnswerTarget+`),
 COALESCE(r.continuation_context->'source'=manager_human_continuation_context(r,s,NULL)->'source',true)
 FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE r.id=$7`, args...).Scan(&state, &version, &kind, &rawQuestion, &previous, &available, &scopeCurrent)
	if err != nil {
		return nil, err
	}
	if version != answer.Version {
		return nil, service.Fail("human_request_changed", 409, "问题版本已变化，请重新查看")
	}
	if state == "pending" && answer.Decision != "reject" && !scopeCurrent {
		return nil, service.Fail("human_task_scope_changed", 409, "原任务的执行范围已变化，需要重新发起确认")
	}
	var question service.HumanQuestion
	if err = json.Unmarshal(rawQuestion, &question); err != nil {
		return nil, err
	}
	if len(question.Questions) > 0 {
		for index := range answer.Answers {
			answer.Answers[index].Value = strings.TrimSpace(answer.Answers[index].Value)
		}
		slices.SortFunc(answer.Answers, func(a, b service.HumanAnswerItem) int { return strings.Compare(a.QuestionID, b.QuestionID) })
		missing, err := question.CheckAnswers(answer)
		if err != nil {
			return nil, err
		}
		if len(missing) != 0 {
			if state != "pending" || !available {
				return nil, service.Fail("human_request_unavailable", 409, "该确认请求当前无法作答")
			}
			return map[string]any{"state": "needs_more", "id": id, "missingFields": missing}, nil
		}
	} else if n := len(service.NumberedHumanQuestions(question.Question)); n > 0 {
		if previous == nil && (state != "pending" || !available) {
			return nil, service.Fail("human_request_unavailable", 409, "该确认请求当前无法作答")
		}
		var receipt map[string]any
		answer, receipt, err = numberedHumanAnswer(ctx, tx, viewer, id, kind, n, answer, previous == nil)
		if err != nil || receipt != nil {
			return receipt, err
		}
	}
	if len(answer.Answers) == 0 {
		answer.Answers = nil
	}
	if kind == "decision" && answer.Decision == "information" {
		if state != "pending" || !available {
			return nil, service.Fail("human_request_unavailable", 409, "该确认请求当前无法作答")
		}
		return explicitHumanDecisionRequired(id), nil
	}
	if err = answer.Validate(kind); err != nil {
		return nil, err
	}
	if answer.Decision == "needs_more" {
		return recordHumanClarification(ctx, tx, viewer, id, answer, state == "pending" && available)
	}
	if previous != nil {
		var saved service.HumanAnswer
		if err = json.Unmarshal(previous, &saved); err != nil {
			return nil, err
		}
		if reflect.DeepEqual(saved, answer) {
			return map[string]string{"id": id, "state": state}, nil
		}
		return nil, service.Fail("human_answer_conflict", 409, "该问题已有其他答复")
	}
	if state != "pending" || !available {
		return nil, service.Fail("human_request_unavailable", 409, "该确认请求当前无法作答")
	}
	return recordFinalHumanAnswer(ctx, tx, viewer, id, session, answer)
}

func explicitHumanDecisionRequired(id string) map[string]any {
	return map[string]any{"state": "needs_more", "id": id, "reason": "explicit_decision_required",
		"message": "还需要用户明确是否批准"}
}

func recordFinalHumanAnswer(ctx context.Context, tx *sql.Tx, viewer humanViewer, id, session string, answer service.HumanAnswer) (any, error) {
	answerJSON, err := json.Marshal(answer)
	if err != nil {
		return nil, err
	}
	st := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: viewer.workspace, Session: session}}
	state := "approved"
	var resume any
	if answer.Decision == "reject" {
		state = "rejected"
		if err = rejectHumanRequest(&st); err != nil {
			return nil, err
		}
	} else {
		if answer.Decision == "revise" {
			state = "revised"
		}
		if resume, err = enqueueHumanResume(&st, id); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_human_requests r SET state=$2,answer=$3,answer_input_id=NULLIF($4,'')::uuid,
 resume_input_id=$5,answer_channel_inbox_id=NULLIF($6,'')::uuid,answered_at=now(),
 continuation_context=CASE WHEN $5::uuid IS NOT NULL THEN manager_human_continuation_context(r,s,$3) ELSE r.continuation_context END
 FROM manager_sessions s WHERE r.id=$1 AND s.id=r.session_id`, id, state, answerJSON, viewer.input, resume, viewer.inbox)
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id, "state": state}, st.SnapshotEvent()
}

func enqueueHumanResume(st *sessionTx, request string) (string, error) {
	id := service.NewID()
	var seq int64
	err := st.tx.QueryRowContext(st.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1,
 required_actions=COALESCE((SELECT jsonb_agg(CASE WHEN q->>'request_id'=$2 THEN jsonb_set(q,'{message}',to_jsonb('答复已记录，等待继续'::text)) ELSE q END ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY x(q,ord)),'[]') WHERE id=$1 RETURNING last_input_seq`, st.target.Session, request).Scan(&seq)
	if err != nil {
		return "", err
	}
	_, err = st.tx.ExecContext(st.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,seq,text,submitted_by,admission_epoch,
 channel_principal_id,channel_generation,trigger_event_id,status,delivery_mode,source_kind,human_request_id,work_assignment_id)
 SELECT $1,i.workspace_id,i.session_id,$2,'已收到人工答复',i.submitted_by,i.admission_epoch,i.channel_principal_id,i.channel_generation,
 i.trigger_event_id,'queued','queue','human_confirmation',r.id,i.work_assignment_id FROM manager_human_requests r JOIN manager_inputs i ON i.id=r.authority_input_id WHERE r.id=$3`,
		id, seq, request)
	if err != nil {
		return "", err
	}
	input, err := st.Input(id)
	if err != nil {
		return "", err
	}
	return id, st.Event("input.updated", &id, nil, service.InputFields(input))
}

func rejectHumanRequest(st *sessionTx) error {
	items, err := st.CancelQueued("human_rejected")
	if err != nil {
		return err
	}
	for _, input := range items {
		if err := st.Event("input.updated", &input.ID, nil, service.InputFields(input)); err != nil {
			return err
		}
	}
	return st.Stop(false)
}
