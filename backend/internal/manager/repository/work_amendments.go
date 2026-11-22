package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type chatWorkItem struct {
	workItem
	employee workEmployee
	session  string
}

// A chat may see the item through its own delegated task and original actor.
// The assignee's session is used internally and never exposed as a chat target.
func findChatWorkItem(ctx context.Context, tx *sql.Tx, scope service.DelegationScope, task, item string, lock bool) (chatWorkItem, error) {
	var v chatWorkItem
	query := `SELECT ` + workColumns + `,COALESCE(a.session_id::text,''),COALESCE(e.id::text,''),COALESCE(e.name,'')
 FROM manager_work_items w JOIN manager_sessions root ON root.id=w.root_session_id
 JOIN manager_tasks k ON k.id=root.task_id
 JOIN manager_inputs original ON original.id=w.authority_input_id AND original.session_id=root.id
 LEFT JOIN manager_work_assignments a ON a.id=w.current_assignment_id
 LEFT JOIN platform_agents e ON e.id=a.target_agent_id
 WHERE w.workspace_id=$1 AND k.source_chat_session_id=$2 AND root.conversation_id=$3
 AND CASE WHEN $4 THEN original.channel_principal_id ELSE original.submitted_by END=$5
 AND manager_input_actor_before_work_items(original.id)
 AND ($6::text='' OR k.id=NULLIF($6,'')::uuid) AND ($7::text='' OR w.id=NULLIF($7,'')::uuid)
 ORDER BY w.created_at DESC,w.id DESC LIMIT 1`
	if lock {
		query += " FOR UPDATE OF w"
	}
	row := tx.QueryRowContext(ctx, query, scope.WorkspaceID, scope.Identity.SessionID, scope.ConversationID, scope.Channel != nil, scope.ActorID, task, item)
	var err error
	v.workItem, err = scanWorkItem(appendedScanner{scanner: row, extra: []any{&v.session, &v.employee.ID, &v.employee.Name}})
	return v, err
}

type workAmendmentReceipt struct {
	InputID             string       `json:"input_id"`
	RequirementsVersion string       `json:"requirements_version"`
	Delivery            string       `json:"delivery"`
	Employee            workEmployee `json:"employee"`
}

// A native receipt proves delivery to the model context. A processed Turn or
// a successful enqueue alone does not.
func readWorkAmendment(ctx context.Context, tx *sql.Tx, item, key string) (workAmendmentReceipt, string, error) {
	var out workAmendmentReceipt
	var version int64
	var content string
	err := tx.QueryRowContext(ctx, `SELECT i.id,m.requirements_version,m.content,e.id,e.name,
 CASE WHEN i.context_state='committed' THEN 'received' WHEN i.cancel_reason='work_assignment_finished' THEN 'closed'
 WHEN i.status IN ('queued','canceled','rejected') THEN i.status
 WHEN i.context_state='absent' THEN 'not_received' WHEN i.status='started' AND i.context_state='pending' THEN 'pending' ELSE 'unknown' END
 FROM manager_work_amendments m JOIN manager_inputs i ON i.id=m.input_id
 JOIN manager_work_assignments a ON a.id=i.work_assignment_id JOIN platform_agents e ON e.id=a.target_agent_id
 WHERE m.work_item_id=$1 AND ($2='' OR m.request_key=$2) ORDER BY m.requirements_version DESC LIMIT 1`, item, key).
		Scan(&out.InputID, &version, &content, &out.Employee.ID, &out.Employee.Name, &out.Delivery)
	out.RequirementsVersion = decimalWorkVersion(version)
	return out, content, err
}

func (s *Store) ChatWorkItem(ctx context.Context, scope service.DelegationScope, task string) (*service.ChatWorkItemView, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // The read-only snapshot can be discarded.
	w, err := findChatWorkItem(ctx, tx, scope, task, "", false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return chatWorkItemView(ctx, tx, w)
}

func chatWorkItemView(ctx context.Context, tx *sql.Tx, w chatWorkItem) (*service.ChatWorkItemView, error) {
	var err error
	item := map[string]any{"id": w.id, "title": w.title, "goal": w.goal, "criteria": w.acceptanceCriteria(),
		"status": w.status, "requirements_version": decimalWorkVersion(w.requirementsVersion)}
	if w.status == "working" || w.status == "blocked" && w.submission == "" {
		item["current_employee"] = w.employee
	} else if w.status == "awaiting_acceptance" || w.status == "blocked" && w.submission != "" {
		var reviewer workEmployee
		if err = tx.QueryRowContext(ctx, `SELECT e.id,e.name FROM manager_sessions s JOIN platform_agents e ON e.id=s.agent_id WHERE s.id=$1`, w.root).Scan(&reviewer.ID, &reviewer.Name); err != nil {
			return nil, err
		}
		item["current_employee"] = reviewer
	}
	if w.requirementsVersion > 1 {
		receipt, _, e := readWorkAmendment(ctx, tx, w.id, "")
		if e != nil {
			return nil, e
		}
		item["latest_amendment"] = receipt
	}
	return &service.ChatWorkItemView{ItemID: w.id, AssignmentID: w.assignment, Version: w.requirementsVersion, Item: item}, nil
}

func (s *Store) AmendChatWorkItem(ctx context.Context, scope service.DelegationScope, item, assignment, key, content string, expected int64) (any, error) {
	var result any
	err := retrySessionTransaction(ctx, func() error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
		result, err = s.amendWorkItem(ctx, tx, scope, item, assignment, key, content, expected)
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return result, err
}

func (s *Store) amendWorkItem(ctx context.Context, tx *sql.Tx, scope service.DelegationScope, item, assignment, key, content string, expected int64) (any, error) {
	var receipt workAmendmentReceipt
	sourceText, err := s.lockWorkAmendmentSource(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	before, err := findChatWorkItem(ctx, tx, scope, "", item, false)
	if err != nil {
		return receipt, visibleError(err)
	}
	// WorkTool takes the worker's session lock before the item lock. Use
	// the same order so that queue allocation and submit can share a fence.
	var lifecycle string
	var stop int64
	if err = tx.QueryRowContext(ctx, `SELECT lifecycle,stop_revision FROM manager_sessions WHERE id=$1 FOR UPDATE`, before.session).Scan(&lifecycle, &stop); err != nil {
		return receipt, visibleError(err)
	}
	w, err := findChatWorkItem(ctx, tx, scope, "", item, true)
	if err != nil {
		return receipt, visibleError(err)
	}
	receipt, saved, err := readWorkAmendment(ctx, tx, item, key)
	if err == nil {
		if saved != content || receipt.RequirementsVersion != decimalWorkVersion(expected+1) {
			return receipt, service.Fail("idempotency_conflict", 409, "去重键已用于其他补充要求")
		}
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	if w.status != "working" {
		return receipt, service.Fail("work_amendment_closed", 409, "事项已进入提交、验收或已关闭，本次补充未受理")
	}
	if w.assignment != before.assignment || w.assignment != assignment {
		return nil, workConflict()
	}
	if err = checkWorkAmendmentTarget(ctx, tx, w, lifecycle, stop); err != nil {
		return receipt, err
	}
	if w.requirementsVersion != expected {
		view, err := chatWorkItemView(ctx, tx, w)
		if err != nil {
			return nil, err
		}
		return service.WorkAmendmentRebase{Latest: *view, SourceText: sourceText}, workConflict()
	}
	return s.queueWorkAmendment(ctx, tx, scope.WorkspaceID, w, key, content, scope.InputID)
}

// Only the exact authenticated user input is eligible, even if another input
// from the same actor has since been committed to this active turn.
func (s *Store) lockWorkAmendmentSource(ctx context.Context, tx *sql.Tx, scope service.DelegationScope) (string, error) {
	if err := s.lockDelegationSource(ctx, tx, service.Target{Workspace: scope.WorkspaceID, Actor: scope.ActorID, Conversation: scope.ConversationID, Source: &scope.Identity}); err != nil {
		return "", err
	}
	var text string
	err := tx.QueryRowContext(ctx, `SELECT u.text FROM manager_sessions s
 JOIN platform_agents a ON a.id=s.agent_id
 JOIN manager_turns t ON t.id=s.active_turn_id
 JOIN manager_inputs root ON root.id=t.input_id
 JOIN manager_inputs u ON u.turn_id=t.id AND u.session_id=s.id
 WHERE s.id=$1 AND u.id=$2 AND u.source_kind='user' AND u.trigger_agent_loop
 AND u.admission_epoch=a.admission_epoch
 AND (s.stop_revision=s.applied_stop_revision OR u.seq>s.stop_input_seq)
 AND (u.id=root.id OR u.context_state='committed')
 AND u.submitted_by IS NOT DISTINCT FROM root.submitted_by
 AND u.channel_principal_id IS NOT DISTINCT FROM root.channel_principal_id
 AND u.channel_generation IS NOT DISTINCT FROM root.channel_generation
 AND manager_input_actor_allowed(u.id)`, scope.Identity.SessionID, scope.InputID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.Fail("work_amendment_source_unavailable", 409, "原补充已不在当前执行轮次或权限已变化，本次未受理")
	}
	return text, err
}

func checkWorkAmendmentTarget(ctx context.Context, tx *sql.Tx, w chatWorkItem, lifecycle string, stop int64) error {
	var closing, allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_boundaries
 WHERE work_item_id=$1 AND session_id=$2 AND state IN ('preparing','safe','blocked')),
 EXISTS(SELECT FROM manager_inputs i WHERE i.work_assignment_id=$3 AND i.source_kind='work_item_assignment' AND manager_work_input_allowed(i.id))`,
		w.id, w.session, w.assignment).Scan(&closing, &allowed)
	if err != nil {
		return err
	}
	if closing {
		return service.Fail("work_amendment_closed", 409, "接手任务已进入提交或转交，本次补充未受理")
	}
	if !allowed || lifecycle != "active" || stop != 0 {
		return service.Fail("work_receiver_unavailable", 409, "当前接手任务不可继续，本次补充未受理")
	}
	return nil
}

func (s *Store) queueWorkAmendment(ctx context.Context, tx *sql.Tx, workspace string, w chatWorkItem, key, content, source string) (workAmendmentReceipt, error) {
	var receipt workAmendmentReceipt
	w.requirementsVersion++
	if w.additions != "" {
		w.additions += "\n"
	}
	w.additions += "- " + content
	prompt, err := workPrompt(w.workItem, w.employee, "当前事项的要求已更新。继续处理同一任务。用 work_item_get_current 核对最新验收标准，完成后再提交成果。", content)
	if err != nil {
		return receipt, err
	}
	if len(prompt) > 24000 {
		return receipt, service.Fail("work_requirements_too_large", 422, "事项要求总长度已达上限")
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, w.session).Scan(&seq); err != nil {
		return receipt, err
	}
	input := service.NewID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,seq,text,admission_epoch,work_assignment_id,delivery_mode,source_kind)
 SELECT $1,$2,$3,$4,$5,i.admission_epoch,$6,'queue','work_item_amendment' FROM manager_inputs i
 WHERE i.work_assignment_id=$6 AND i.source_kind='work_item_assignment'`, input, workspace, w.session, seq, prompt, w.assignment); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_work_amendments(work_item_id,requirements_version,request_key,source_input_id,input_id,content)
 VALUES($1,$2,$3,$4,$5,$6)`, w.id, w.requirementsVersion, key, source, input, content); err != nil {
		return receipt, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_work_items SET requirements_version=$2,version=version+1,updated_at=now() WHERE id=$1`, w.id, w.requirementsVersion); err != nil {
		return receipt, err
	}
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: workspace, Session: w.session}}
	if err = workInputEvent(&st, input); err != nil {
		return receipt, err
	}
	return workAmendmentReceipt{InputID: input, RequirementsVersion: decimalWorkVersion(w.requirementsVersion), Delivery: "queued", Employee: w.employee}, nil
}

func closeQueuedWorkAmendments(st *sessionTx) error {
	rows, err := st.tx.QueryContext(st.ctx, `UPDATE manager_inputs SET status='canceled',cancel_reason='work_assignment_finished',finished_at=now()
 WHERE session_id=$1 AND status='queued' AND source_kind='work_item_amendment' RETURNING `+inputColumns, st.target.Session)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Release the result before writing events.
	var inputs []service.Input
	for rows.Next() {
		input, e := scanInput(rows)
		if e != nil {
			return e
		}
		inputs = append(inputs, input)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, input := range inputs {
		if err = st.Event("input.updated", &input.ID, nil, service.InputFields(input)); err != nil {
			return err
		}
	}
	return nil
}
