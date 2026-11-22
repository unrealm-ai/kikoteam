package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// ReconcileWorkItems consumes durable Engine evidence even if the original
// request, Engine or Backend process is gone.
func (s *Store) ReconcileWorkItems(ctx context.Context) error {
	// The native fence and terminal Turn are affirmative evidence. A cleared
	// queue by itself cannot establish whether a side effect happened.
	if _, err := s.DB.ExecContext(ctx, `UPDATE manager_work_boundaries b SET state='safe' FROM manager_sessions s,manager_turns t
 WHERE b.session_id=s.id AND b.turn_id=t.id AND b.state='blocked' AND b.error_code IS NULL AND b.fenced_at IS NOT NULL
 AND t.status='interrupted' AND t.error->>'code'='awaiting_work_commit' AND NOT t.has_unknown_tool_results
 AND s.lifecycle='active' AND s.active_turn_id IS NULL AND s.stop_revision=b.stop_revision AND s.applied_stop_revision=s.stop_revision
 AND s.background_process_count=0 AND s.block_reason IS NULL
 AND NOT EXISTS(SELECT FROM manager_capability_calls c WHERE c.session_id=s.id AND c.status='authorized')
 AND NOT EXISTS(SELECT FROM manager_work_items w WHERE w.id=b.work_item_id AND w.status NOT IN ('working','awaiting_acceptance'))`); err != nil {
		return err
	}
	for range 25 {
		processed, err := s.commitNextWorkBoundary(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

func (s *Store) commitNextWorkBoundary(ctx context.Context) (bool, error) {
	var processed bool
	err := retrySessionTransaction(ctx, func() error {
		processed = false
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
		b, err := nextWorkBoundary(ctx, tx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		processed = true
		if _, err = tx.ExecContext(ctx, "SAVEPOINT work_dispatch"); err != nil {
			return err
		}
		if err = s.commitWorkBoundary(ctx, tx, b); err != nil {
			var failure *service.Error
			var platformFailure *platform.Error
			if !errors.As(err, &failure) && !errors.As(err, &platformFailure) {
				return err
			}
			if _, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT work_dispatch"); err != nil {
				return err
			}
			if err = blockWorkBoundary(ctx, tx, s, b, failure, platformFailure); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	return processed && err == nil, err
}

type workBoundary struct {
	id, session, turn, kind, item string
	args                          service.WorkArguments
	src                           workSource
	requirementsVersion           int64
}

func nextWorkBoundary(ctx context.Context, tx *sql.Tx) (workBoundary, error) {
	var b workBoundary
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT b.id,b.session_id,b.turn_id,b.kind,COALESCE(b.work_item_id::text,''),b.payload,b.stop_revision,b.requirements_version,
 s.workspace_id,s.agent_id,COALESCE(h.authority_input_id,i.id)
 FROM manager_work_boundaries b JOIN manager_sessions s ON s.id=b.session_id
 JOIN manager_inputs i ON i.id=b.input_id LEFT JOIN manager_human_requests h ON h.id=i.human_request_id
 WHERE b.state='safe' AND (retry_after IS NULL OR retry_after<=now()) ORDER BY b.id FOR UPDATE OF b SKIP LOCKED LIMIT 1`).
		Scan(&b.id, &b.session, &b.turn, &b.kind, &b.item, &raw, &b.src.stopRev, &b.requirementsVersion, &b.src.workspace, &b.src.agent, &b.src.authority)
	if err == nil {
		err = json.Unmarshal(raw, &b.args)
	}
	return b, err
}

func (s *Store) commitWorkBoundary(ctx context.Context, tx *sql.Tx, b workBoundary) error {
	var w workItem
	var err error
	if b.item == "" {
		w, err = createWorkItem(ctx, tx, b)
		if err != nil {
			return fmt.Errorf("create work item: %w", err)
		}
	} else {
		w, err = scanWorkItem(tx.QueryRowContext(ctx, `SELECT `+workColumns+` FROM manager_work_items w
 LEFT JOIN manager_work_assignments a ON a.id=w.current_assignment_id WHERE w.id=$1 FOR UPDATE OF w`, b.item))
		if err == nil && (w.version != b.args.ExpectedVersion || w.requirementsVersion != b.requirementsVersion) {
			return workConflict()
		}
	}
	if err != nil {
		return err
	}
	if err = checkWorkBoundary(ctx, tx, b, w); err != nil {
		return fmt.Errorf("revalidate work boundary: %w", err)
	}
	if err = validateForwardedFiles(ctx, tx, b.id); err != nil {
		return err
	}
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: b.src.workspace, Session: b.session}}
	if err = closeQueuedWorkAmendments(&st); err != nil {
		return err
	}
	if b.kind == "submit" {
		if err = s.submitWorkResult(ctx, tx, b, w); err != nil {
			return fmt.Errorf("submit work result: %w", err)
		}
	} else if err = s.dispatchWorkAssignment(ctx, tx, b, w); err != nil {
		return fmt.Errorf("dispatch work assignment: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_work_boundaries SET state='committed',work_item_id=$2,error_code=NULL,retry_after=NULL WHERE id=$1`, b.id, w.id)
	return err
}

func createWorkItem(ctx context.Context, tx *sql.Tx, b workBoundary) (workItem, error) {
	w := workItem{id: service.NewID(), root: b.session, rootTurn: b.turn, authority: b.src.authority, rootStop: b.src.stopRev, space: service.NewID(), title: b.args.Title, goal: b.args.Goal, criteria: b.args.Criteria, requirementsVersion: 1}
	r, err := tx.ExecContext(ctx, `INSERT INTO manager_task_spaces(id,workspace_id,business_key,created_by)
 SELECT $1,s.workspace_id,$2,m.user_id FROM manager_sessions s
 JOIN platform_members m ON m.workspace_id=s.workspace_id AND m.status='active' AND m.role='owner'
 WHERE s.id=$3 LIMIT 1`, w.space, "workitem-"+w.id, b.session)
	if err != nil {
		return w, fmt.Errorf("insert work TaskSpace: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return w, err
	}
	if n != 1 {
		return w, service.Fail("work_space_unavailable", 409, "事项空间归属不可用")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_work_items(id,workspace_id,root_session_id,root_turn_id,authority_input_id,root_stop_revision,task_space_id,title,goal,criteria,status)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'working')`, w.id, b.src.workspace, w.root, w.rootTurn, w.authority, w.rootStop, w.space, w.title, w.goal, w.criteria)
	if err != nil {
		return w, fmt.Errorf("insert work item row: %w", err)
	}
	return w, nil
}

func checkWorkBoundary(ctx context.Context, tx *sql.Tx, b workBoundary, w workItem) error {
	// Row locks arbitrate stop and archive against the boundary commit. Root
	// and source can be the same Session or two independent Sessions.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM manager_sessions WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, b.session, w.root)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close releases the result before the next query.
	for rows.Next() {
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var source, root bool
	err = tx.QueryRowContext(ctx, `SELECT
 s.lifecycle='active' AND s.stop_revision=$2 AND s.stop_revision=s.applied_stop_revision AND s.active_turn_id IS NULL AND COALESCE(sc.lifecycle,sp.lifecycle)='active' AND sa.enabled,
 r.lifecycle='active' AND r.stop_revision=$4 AND r.stop_revision=r.applied_stop_revision AND r.active_turn_id IS NULL AND COALESCE(rc.lifecycle,rp.lifecycle)='active'
 AND ra.enabled AND original.admission_epoch=ra.admission_epoch AND manager_input_actor_before_work_items(original.id)
 FROM manager_sessions s JOIN platform_agents sa ON sa.id=s.agent_id
 LEFT JOIN manager_conversations sc ON sc.id=s.conversation_id LEFT JOIN manager_task_spaces sp ON sp.id=s.task_space_id
 JOIN manager_sessions r ON r.id=$3 JOIN platform_agents ra ON ra.id=r.agent_id
 LEFT JOIN manager_conversations rc ON rc.id=r.conversation_id LEFT JOIN manager_task_spaces rp ON rp.id=r.task_space_id
 JOIN manager_inputs original ON original.id=$5 AND original.session_id=r.id WHERE s.id=$1`, b.session, b.src.stopRev, w.root, w.rootStop, w.authority).Scan(&source, &root)
	if err != nil {
		return err
	}
	if !source || !root {
		return service.Fail("work_source_unavailable", 409, "来源任务已停止、归档或失去权限")
	}
	if b.item == "" {
		return nil
	}
	if b.session == w.root && w.status == "awaiting_acceptance" && w.submission == b.args.SubmissionID {
		return nil
	}
	var owner bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_work_assignments WHERE id=NULLIF($1,'')::uuid AND session_id=$2)`, w.assignment, b.session).Scan(&owner)
	if err != nil {
		return err
	}
	if owner {
		err = tx.QueryRowContext(ctx, `SELECT manager_input_actor_allowed(id) FROM manager_inputs WHERE id=$1`, b.src.authority).Scan(&owner)
		if err != nil {
			return err
		}
	}
	if !owner || w.status != "working" || w.assignment != b.args.ExpectedAssignmentID {
		return workConflict()
	}
	return nil
}

func (s *Store) dispatchWorkAssignment(ctx context.Context, tx *sql.Tx, b workBoundary, w workItem) error {
	agent := b.args.TargetEmployeeID
	var epoch int64
	employee := workEmployee{ID: agent}
	err := tx.QueryRowContext(ctx, `SELECT a.admission_epoch,a.name FROM platform_agents a WHERE a.id=$1 AND a.workspace_id=$2 AND a.id<>$3 AND a.enabled AND manager_work_receiver_allows(a.id,$3) AND NOT EXISTS (SELECT FROM manager_work_boundaries b WHERE b.id=$4 AND b.target_epoch IS NOT NULL AND b.target_epoch<>a.admission_epoch) FOR SHARE OF a`, agent, b.src.workspace, b.src.agent, b.id).Scan(&epoch, &employee.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("work_receiver_unavailable", 409, "接手员工当前不可用")
	}
	if err != nil {
		return err
	}
	task, session, assignment, input := service.NewID(), service.NewID(), service.NewID(), service.NewID()
	var creator string
	if err = tx.QueryRowContext(ctx, `SELECT created_by FROM manager_task_spaces WHERE id=$1 AND lifecycle='active' FOR SHARE`, w.space).Scan(&creator); err != nil {
		return visibleError(err)
	}
	st := sessionTx{ctx: platform.WithWorkExecution(ctx, platform.WorkExecution{AssignmentID: assignment, SourceAgentID: b.src.agent}), tx: tx, store: s,
		target: service.Target{Workspace: b.src.workspace, TaskSpaceID: w.space, AgentID: agent, Session: session}}
	st.state.AgentID = agent
	cfg, err := st.Configuration("task")
	if err != nil {
		return fmt.Errorf("resolve assignee configuration: %w", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_tasks(id,workspace_id,agent_id,task_space_id) VALUES($1,$2,$3,$4)`, task, b.src.workspace, agent, w.space); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_sessions(id,workspace_id,agent_id,task_id,task_space_id,task_space_agent_epoch,created_by,mode,title,configuration,last_input_seq)
 VALUES($1,$2,$3,$4,$5,1,$6,'task',$7,$8,1)`, session, b.src.workspace, agent, task, w.space, creator, w.title, raw); err != nil {
		return err
	}
	brief := b.args.Summary
	if b.args.SubmissionID != "" {
		var preceding []byte
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM manager_work_boundaries WHERE id=$1 AND work_item_id=$2 AND kind='submit' AND state='committed'`, b.args.SubmissionID, w.id).Scan(&preceding); err != nil {
			return err
		}
		var prior service.WorkArguments
		if err = json.Unmarshal(preceding, &prior); err != nil {
			return err
		}
		brief = "整改要求：\n" + brief + "\n上次提交的成果：\n" + prior.Result + "\n上次验收证据：\n" + prior.Evidence
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_work_assignments(id,work_item_id,source_agent_id,target_agent_id,session_id,summary,boundary_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, assignment, w.id, b.src.agent, agent, session, brief, b.id); err != nil {
		return err
	}
	prompt, err := workPrompt(w, employee, "你收到一个事项处理任务。调用 work_item_get_current 读取本次分配。继续转交时使用 work_item_handoff，完成工作时用 work_item_submit_result 提交成果供发起任务验收。", brief)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,work_assignment_id,admission_epoch,seq,text,delivery_mode,source_kind)
 VALUES($1,$2,$3,$4,$5,1,$6,'queue','work_item_assignment')`, input, b.src.workspace, session, assignment, epoch, prompt); err != nil {
		return err
	}
	if err = st.SetInitialInput(input); err != nil {
		return err
	}
	if err = st.Event("session.created", nil, nil, map[string]any{"session_id": session, "task_id": task, "work_item_id": w.id}); err != nil {
		return err
	}
	if err = workInputEvent(&st, input); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_work_items SET status='working',current_assignment_id=$2,current_submission_id=NULL,version=version+1,updated_at=now() WHERE id=$1`, w.id, assignment); err != nil {
		return err
	}
	if err = setWorkAction(ctx, tx, w.root, w.id, w.rootTurn, "接手任务已排队"); err != nil {
		return err
	}
	if b.session != w.root {
		if err = setWorkAction(ctx, tx, b.session, b.id, b.turn, "本环节已转交"); err != nil {
			return err
		}
		if err = workSnapshot(ctx, tx, s, b.src.workspace, b.session); err != nil {
			return err
		}
	}
	kind := "handoff"
	if b.item == "" {
		kind = "created"
	} else if b.args.SubmissionID != "" {
		kind = "returned"
	}
	if err = workEvent(ctx, tx, w.id, kind, b.src.agent, agent, b.turn, b.args.Summary, kind != "handoff"); err != nil {
		return err
	}
	return workSnapshot(ctx, tx, s, b.src.workspace, w.root)
}

func (s *Store) submitWorkResult(ctx context.Context, tx *sql.Tx, b workBoundary, w workItem) error {
	var employee workEmployee
	if err := tx.QueryRowContext(ctx, `SELECT id,name FROM platform_agents WHERE id=$1`, b.src.agent).Scan(&employee.ID, &employee.Name); err != nil {
		return err
	}
	prompt, err := workPrompt(w, employee, "已收到交接事项的新成果。调用 work_item_get_current 读取结果并按本次提交绑定的验收标准判断。通过时调用 work_item_accept，随后给出最终答复。需要整改时调用 work_item_handoff，写明缺陷与要求。", "")
	if err != nil {
		return err
	}
	input := service.NewID()
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, w.root).Scan(&seq); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,seq,text,submitted_by,admission_epoch,channel_principal_id,channel_generation,trigger_event_id,status,delivery_mode,source_kind,work_submission_id,report_enabled)
 SELECT $1,i.workspace_id,i.session_id,$2,$3,i.submitted_by,i.admission_epoch,i.channel_principal_id,i.channel_generation,i.trigger_event_id,'queued','queue','work_item_result',$4,i.report_enabled
 FROM manager_inputs i WHERE i.id=$5`, input, seq, prompt, b.id, w.authority)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_work_items SET status='awaiting_acceptance',current_submission_id=$2,version=version+1,updated_at=now() WHERE id=$1`, w.id, b.id); err != nil {
		return err
	}
	if err = setWorkAction(ctx, tx, w.root, w.id, w.rootTurn, "成果已提交，等待原任务验收"); err != nil {
		return err
	}
	if err = setWorkAction(ctx, tx, b.session, b.id, b.turn, "本环节已提交成果"); err != nil {
		return err
	}
	if err = workSnapshot(ctx, tx, s, b.src.workspace, b.session); err != nil {
		return err
	}
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: b.src.workspace, Session: w.root}}
	if err = workEvent(ctx, tx, w.id, "submitted", b.src.agent, "", b.turn, b.args.Evidence, false); err != nil {
		return err
	}
	return workInputEvent(&st, input)
}

func workInputEvent(st *sessionTx, id string) error {
	input, err := st.Input(id)
	if err != nil {
		return err
	}
	if err = st.Event("input.updated", &id, nil, service.InputFields(input)); err != nil {
		return err
	}
	return st.SnapshotEvent()
}

func workSnapshot(ctx context.Context, tx *sql.Tx, s *Store, workspace, session string) error {
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: workspace, Session: session}}
	return st.SnapshotEvent()
}

func blockWorkBoundary(ctx context.Context, tx *sql.Tx, s *Store, b workBoundary, failure *service.Error, platformFailure *platform.Error) error {
	code, message := "work_dispatch_blocked", "派发受阻"
	if failure != nil {
		code, message = failure.Code, failure.Message
	}
	if platformFailure != nil {
		code, message = platformFailure.Code, platformFailure.Message
	}
	if code == "configuration_unusable" && b.kind == "handoff" {
		var first bool
		if err := tx.QueryRowContext(ctx, `SELECT retry_after IS NULL FROM manager_work_boundaries WHERE id=$1`, b.id).Scan(&first); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE manager_work_boundaries SET retry_after=now()+interval '5 seconds',error_code=$2 WHERE id=$1`, b.id, code); err != nil {
			return err
		}
		if !first {
			return nil
		}
		if err := updateExistingWorkAction(ctx, tx, b.session, b.id, "等待接手员工的模型恢复"); err != nil {
			return err
		}
		return workSnapshot(ctx, tx, s, b.src.workspace, b.session)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE manager_work_boundaries SET state='blocked',error_code=$2 WHERE id=$1`, b.id, code); err != nil {
		return err
	}
	if err := updateExistingWorkAction(ctx, tx, b.session, b.id, message); err != nil {
		return err
	}
	if b.item != "" {
		var root string
		err := tx.QueryRowContext(ctx, `UPDATE manager_work_items SET status='blocked',reason=$3,version=version+1,updated_at=now() WHERE id=$1 AND status IN ('working','awaiting_acceptance') AND version=$2 RETURNING root_session_id`, b.item, b.args.ExpectedVersion, message).Scan(&root)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = workEvent(ctx, tx, b.item, "blocked", b.src.agent, "", b.turn, message, false); err != nil {
				return err
			}
		}
		if err == nil && root != b.session {
			if err = updateExistingWorkAction(ctx, tx, root, b.item, message); err != nil {
				return err
			}
			if err = workSnapshot(ctx, tx, s, b.src.workspace, root); err != nil {
				return err
			}
		}
	}
	return workSnapshot(ctx, tx, s, b.src.workspace, b.session)
}
