package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type workSource struct {
	workspace, session, agent, input, authority, turn, role string
	assignment, submission                                  string
	stopRev                                                 int64
}

type workItem struct {
	id, root, rootTurn, authority, space, title, goal, criteria, status string
	assignment, submission, summary, additions                          string
	version, rootStop, requirementsVersion                              int64
}

const workColumns = `w.id,w.root_session_id,w.root_turn_id,w.authority_input_id,w.task_space_id,w.title,w.goal,w.criteria,w.status,w.version,w.root_stop_revision,
 COALESCE(w.current_assignment_id::text,''),COALESCE(w.current_submission_id::text,''),COALESCE(a.summary,''),w.requirements_version,
 COALESCE((SELECT string_agg('- '||m.content,E'\n' ORDER BY m.requirements_version) FROM manager_work_amendments m WHERE m.work_item_id=w.id),'')`

func scanWorkItem(r scanner) (w workItem, err error) {
	err = r.Scan(&w.id, &w.root, &w.rootTurn, &w.authority, &w.space, &w.title, &w.goal, &w.criteria, &w.status, &w.version, &w.rootStop, &w.assignment, &w.submission, &w.summary, &w.requirementsVersion, &w.additions)
	return
}

func (w workItem) acceptanceCriteria() string {
	if w.additions == "" {
		return w.criteria
	}
	return w.criteria + "\n新增验收要求：\n" + w.additions
}

func lockWorkSource(ctx context.Context, tx *sql.Tx, scoped service.WorkScope) (workSource, error) {
	v := workSource{session: scoped.Identity.SessionID, input: scoped.InputID}
	id := scoped.Identity
	err := tx.QueryRowContext(ctx, `SELECT s.workspace_id,s.agent_id,t.id,s.stop_revision,COALESCE(h.authority_input_id,i.id),
 COALESCE(i.work_assignment_id::text,''),COALESCE(b.id::text,''),
 CASE WHEN i.work_assignment_id IS NOT NULL THEN 'assignee'
 WHEN r.id IS NOT NULL THEN 'review'
 WHEN EXISTS(SELECT 1 FROM manager_work_items w WHERE w.root_session_id=s.id AND w.status IN ('working','awaiting_acceptance','blocked')) THEN ''
 ELSE 'ordinary' END
 FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id
 JOIN manager_inputs i ON i.id=t.input_id AND i.session_id=s.id
 JOIN platform_agents a ON a.id=s.agent_id AND a.workspace_id=s.workspace_id
 LEFT JOIN manager_human_requests h ON h.id=i.human_request_id
 LEFT JOIN manager_inputs authority ON authority.id=h.authority_input_id
 LEFT JOIN manager_work_boundaries b ON b.id=COALESCE(i.work_submission_id,authority.work_submission_id)
 LEFT JOIN manager_work_items r ON r.id=b.work_item_id AND r.root_session_id=s.id AND r.current_submission_id=b.id AND r.status IN ('awaiting_acceptance','completed')
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND i.id=$5
 AND t.assignment_id=$3 AND t.generation=$4 AND t.status='running' AND i.status='started'
 AND s.mode='task' AND s.lifecycle='active' AND COALESCE(c.lifecycle,p.lifecycle)='active'
 AND s.stop_revision=s.applied_stop_revision AND a.enabled AND i.admission_epoch=a.admission_epoch
 AND manager_input_actor_allowed(i.id) FOR UPDATE OF s`, id.SessionID, id.EngineID, id.AssignmentID, id.Generation, scoped.InputID).
		Scan(&v.workspace, &v.agent, &v.turn, &v.stopRev, &v.authority, &v.assignment, &v.submission, &v.role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && v.role == "" {
		return v, service.Fail("work_tool_forbidden", 403, "当前执行无事项工具权限")
	}
	return v, err
}

func currentWorkItem(ctx context.Context, tx *sql.Tx, src workSource) (workItem, error) {
	if src.role == "ordinary" {
		return workItem{}, nil
	}
	return scanWorkItem(tx.QueryRowContext(ctx, `SELECT `+workColumns+` FROM manager_work_items w
 LEFT JOIN manager_work_assignments a ON a.id=w.current_assignment_id
 WHERE ($2='review' AND w.root_session_id=$1 AND w.current_submission_id=NULLIF($3,'')::uuid)
 OR ($2='assignee' AND a.session_id=$1 AND a.id=NULLIF($4,'')::uuid AND w.status='working') FOR UPDATE OF w`,
		src.session, src.role, src.submission, src.assignment))
}

func (s *Store) WorkTool(ctx context.Context, scope service.WorkScope, op string, arg service.WorkArguments) (any, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	src, err := lockWorkSource(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	if op == "artifact_publish" {
		a, err := s.publishArtifact(ctx, tx, src, arg.Path)
		if err != nil {
			return nil, err
		}
		return a, tx.Commit()
	}
	if op == "employees" {
		return discoverWorkEmployees(ctx, tx, src, arg.Query)
	}
	if op == "fenced" {
		return readWorkFence(ctx, tx, src, arg.BoundaryID)
	}
	w, err := currentWorkItem(ctx, tx, src)
	if err != nil {
		return nil, visibleError(err)
	}
	if op == "file" {
		return s.readAssignedFile(ctx, tx, src, w, arg)
	}
	if op == "get" {
		if w.id == "" {
			return nil, service.Fail("work_tool_forbidden", 403, "当前任务没有可读取的事项")
		}
		return readWorkItem(ctx, tx, src, w)
	}
	if err = bindWorkArguments(src, op, &arg); err != nil {
		return nil, err
	}
	if err = arg.ValidateAction(op, src.role); err != nil {
		return nil, err
	}
	var out any
	if op == "accept" {
		out, err = acceptWorkItem(ctx, tx, s, src, w, arg)
	} else {
		out, err = s.prepareWorkBoundary(ctx, tx, src, w, op, arg)
	}
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// Persist the same fenced payload as before, but source its target IDs from the
// authenticated Input. A legacy caller may echo an ID, never change the target.
func bindWorkArguments(src workSource, op string, arg *service.WorkArguments) error {
	switch {
	case src.role == "assignee" && (op == "handoff" || op == "submit"):
		if arg.ExpectedAssignmentID != "" && arg.ExpectedAssignmentID != src.assignment {
			return workConflict()
		}
		arg.ExpectedAssignmentID = src.assignment
	case src.role == "review" && (op == "handoff" || op == "accept"):
		if arg.SubmissionID != "" && arg.SubmissionID != src.submission {
			return workConflict()
		}
		arg.SubmissionID = src.submission
	}
	return nil
}

const workReceiverReady = `a.enabled AND (
 EXISTS(SELECT 1 FROM platform_model_profiles p WHERE p.id=a.model_profile_id AND p.workspace_id=a.workspace_id AND p.enabled AND p.validated)
 OR EXISTS(SELECT FROM platform_proxy_models p JOIN platform_agent_model_keys k ON k.agent_id=a.id WHERE p.id=a.platform_model_id AND p.enabled AND p.validated))`

func discoverWorkEmployees(ctx context.Context, tx *sql.Tx, src workSource, q string) (any, error) {
	if utf8.RuneCountInString(q) > 100 {
		return nil, service.Fail("invalid_work_arguments", 400, "员工查询过长")
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.name,a.role,COALESCE((
 SELECT jsonb_agg(jsonb_build_object('name',p.name,'description',p.description) ORDER BY p.name)
 FROM platform_capability_packages p JOIN platform_capability_package_versions v
 ON v.package_id=p.id AND v.version=p.version AND v.workspace_id=p.workspace_id
 WHERE p.workspace_id=a.workspace_id AND p.agent_id=a.id AND manager_work_package_allows(v.snapshot,$2)
 ),'[]'::jsonb)
 FROM platform_agents a WHERE a.workspace_id=$1 AND a.id<>$2 AND manager_work_receiver_allows(a.id,$2) AND `+workReceiverReady+`
 AND ($3='' OR strpos(lower(a.name||' '||a.role),lower($3))>0) ORDER BY a.name,a.id LIMIT 100`, src.workspace, src.agent, strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Scan and iteration errors are returned.
	items := []map[string]any{}
	for rows.Next() {
		var id, name, description string
		var packages json.RawMessage
		if err = rows.Scan(&id, &name, &description, &packages); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name, "description": description, "available_packages": packages})
	}
	return map[string]any{"items": items}, rows.Err()
}

func readWorkFence(ctx context.Context, tx *sql.Tx, src workSource, id string) (any, error) {
	if !service.ValidID(id) {
		return nil, service.Fail("invalid_reference", 400, "事项意图标识无效")
	}
	var fenced bool
	err := tx.QueryRowContext(ctx, `SELECT fenced_at IS NOT NULL FROM manager_work_boundaries WHERE id=$1 AND session_id=$2 AND turn_id=$3 AND state='preparing'`, id, src.session, src.turn).Scan(&fenced)
	return map[string]bool{"fenced": fenced}, visibleError(err)
}

func readWorkItem(ctx context.Context, tx *sql.Tx, src workSource, w workItem) (any, error) {
	files, err := assignedFiles(ctx, tx, src, w)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": w.id, "status": w.status, "title": w.title, "goal": w.goal, "criteria": w.acceptanceCriteria(), "version": decimalWorkVersion(w.version), "requirements_version": decimalWorkVersion(w.requirementsVersion)}
	if len(files) > 0 {
		out["files"] = files
	}
	if src.role == "assignee" {
		out["assignment_id"], out["summary"] = w.assignment, w.summary
		return out, nil
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM manager_work_boundaries WHERE id=$1 AND state='committed'`, w.submission).Scan(&payload); err != nil {
		return nil, err
	}
	var result service.WorkArguments
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	out["submission_id"], out["result"], out["completion_evidence"] = w.submission, result.Result, result.Evidence
	return out, nil
}

func workConflict() error {
	return service.Fail("work_version_changed", 409, "事项版本或当前环节已变化，请重新查询")
}

func (s *Store) prepareWorkBoundary(ctx context.Context, tx *sql.Tx, src workSource, w workItem, op string, arg service.WorkArguments) (any, error) {
	if len(arg.OperationID) == 0 || len(arg.OperationID) > 200 {
		return nil, service.Fail("invalid_work_operation", 400, "事项操作标识无效")
	}
	payload, err := json.Marshal(arg)
	if err != nil {
		return nil, err
	}
	var existing, operation, kind string
	var existingPayload []byte
	err = tx.QueryRowContext(ctx, `SELECT id,operation_id,kind,payload FROM manager_work_boundaries WHERE turn_id=$1`, src.turn).Scan(&existing, &operation, &kind, &existingPayload)
	if err == nil {
		if operation != arg.OperationID || kind != op || !sameWorkPayload(existingPayload, payload) {
			return nil, workConflict()
		}
		return map[string]string{"id": existing, "status": "preparing"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if (src.role == "assignee" && (w.assignment != arg.ExpectedAssignmentID || w.version != arg.ExpectedVersion)) ||
		(src.role == "review" && (w.status != "awaiting_acceptance" || w.submission != arg.SubmissionID || w.version != arg.ExpectedVersion)) {
		return nil, workConflict()
	}
	if err = checkWorkActionAvailable(ctx, tx, src.session, op, arg.TargetEmployeeID, src.workspace, src.agent); err != nil {
		return nil, err
	}
	id := service.NewID()
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_work_boundaries(id,session_id,turn_id,input_id,operation_id,kind,work_item_id,payload,stop_revision,target_epoch,requirements_version)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,(SELECT admission_epoch FROM platform_agents WHERE id=$10),$11)`, id, src.session, src.turn, src.input, arg.OperationID, op, nullID(w.id), payload, src.stopRev, nullID(arg.TargetEmployeeID), max(int64(1), w.requirementsVersion))
	if err != nil {
		return nil, err
	}
	if err = s.selectWorkFiles(ctx, tx, src, w, id, arg); err != nil {
		return nil, err
	}
	if err = setWorkAction(ctx, tx, src.session, id, src.turn, "正在停稳"); err != nil {
		return nil, err
	}
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: src.workspace, Session: src.session}}
	return map[string]string{"id": id, "status": "preparing"}, st.SnapshotEvent()
}

func sameWorkPayload(a, b []byte) bool {
	var x, y service.WorkArguments
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

func checkWorkActionAvailable(ctx context.Context, tx *sql.Tx, session, op, target, workspace, source string) error {
	var human bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_human_requests WHERE session_id=$1 AND state IN ('preparing','pending','blocked'))`, session).Scan(&human); err != nil {
		return err
	}
	if human {
		return service.Fail("work_boundary_conflict", 409, "本任务正在等待人工确认")
	}
	if op != "handoff" {
		return nil
	}
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_agents a WHERE a.id=$1 AND a.workspace_id=$2 AND a.id<>$3 AND manager_work_receiver_allows(a.id,$3) AND `+workReceiverReady+`)`, target, workspace, source).Scan(&ready)
	if err == nil && !ready {
		return service.Fail("work_receiver_unavailable", 409, "接手员工当前不可用")
	}
	return err
}

func setWorkAction(ctx context.Context, tx *sql.Tx, session, request, turn, message string) error {
	_, err := tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((SELECT jsonb_agg(q ORDER BY ord)
 FROM jsonb_array_elements(required_actions) WITH ORDINALITY AS r(q,ord) WHERE q->>'type'<>'work_item'),'[]')
 || jsonb_build_array(jsonb_build_object('type','work_item','request_id',$2::text,'turn_id',$3::text,'title','事项交接','message',$4::text)) WHERE id=$1`, session, request, turn, message)
	return err
}

func acceptWorkItem(ctx context.Context, tx *sql.Tx, s *Store, src workSource, w workItem, a service.WorkArguments) (any, error) {
	if w.status == "completed" && w.submission == a.SubmissionID && w.version == a.ExpectedVersion+1 {
		var saved string
		err := tx.QueryRowContext(ctx, `SELECT acceptance_evidence FROM manager_work_items WHERE id=$1`, w.id).Scan(&saved)
		if err != nil {
			return nil, err
		}
		if saved == a.Evidence {
			return map[string]string{"id": w.id, "status": "completed"}, nil
		}
	}
	if w.status != "awaiting_acceptance" || w.submission != a.SubmissionID || w.version != a.ExpectedVersion {
		return nil, workConflict()
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT requirements_version FROM manager_work_boundaries WHERE id=$1 AND state='committed'`, w.submission).Scan(&revision); err != nil {
		return nil, visibleError(err)
	}
	if revision != w.requirementsVersion {
		return nil, workConflict()
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_work_boundaries WHERE turn_id=$1)`, src.turn).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, service.Fail("work_boundary_conflict", 409, "本轮已提交结束动作")
	}
	_, err := tx.ExecContext(ctx, `UPDATE manager_work_items SET status='completed',version=version+1,acceptance_evidence=$2,updated_at=now() WHERE id=$1`, w.id, a.Evidence)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_sessions SET required_actions=COALESCE((SELECT jsonb_agg(q ORDER BY ord) FROM jsonb_array_elements(required_actions)
 WITH ORDINALITY AS r(q,ord) WHERE q->>'type'<>'work_item'),'[]') WHERE id=$1`, src.session)
	if err != nil {
		return nil, err
	}
	st := sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: src.workspace, Session: src.session}}
	if err = workEvent(ctx, tx, w.id, "completed", src.agent, "", src.turn, a.Evidence, true); err != nil {
		return nil, err
	}
	return map[string]string{"id": w.id, "status": "completed"}, st.SnapshotEvent()
}

func decimalWorkVersion(version int64) string { return strconv.FormatInt(version, 10) }

type workEmployee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func workPrompt(w workItem, employee workEmployee, header, brief string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"title": w.title, "goal": w.goal, "criteria": w.acceptanceCriteria(), "summary": brief,
		"requirements_version": decimalWorkVersion(w.requirementsVersion), "current_employee": employee,
	})
	if err != nil {
		return "", err
	}
	return "[系统消息] " + header + "\n" + string(payload), nil
}

func (s *Store) workHarness(ctx context.Context, cfg *service.HarnessConfig) error {
	if cfg.Mode != "task" {
		return nil
	}
	return s.DB.QueryRowContext(ctx, `SELECT COALESCE(a.id::text,''),COALESCE(a.source_agent_id::text,''),
 CASE WHEN a.id IS NOT NULL THEN 'assignee'
 WHEN w.id IS NOT NULL THEN 'review'
 WHEN EXISTS(SELECT 1 FROM manager_work_items root WHERE root.root_session_id=i.session_id AND root.status IN ('working','awaiting_acceptance','blocked')) THEN ''
 ELSE 'ordinary' END
 FROM manager_inputs i
 LEFT JOIN manager_work_assignments a ON a.id=i.work_assignment_id AND a.session_id=i.session_id
 LEFT JOIN manager_human_requests h ON h.id=i.human_request_id LEFT JOIN manager_inputs original ON original.id=h.authority_input_id
 LEFT JOIN manager_work_boundaries b ON b.id=COALESCE(i.work_submission_id,original.work_submission_id)
 LEFT JOIN manager_work_items w ON w.id=b.work_item_id AND w.root_session_id=i.session_id AND w.current_submission_id=b.id
 AND w.status IN ('awaiting_acceptance','completed') WHERE i.id=$1 AND i.session_id=$2`, cfg.InputID, cfg.SessionID).
		Scan(&cfg.WorkAssignmentID, &cfg.WorkSourceAgentID, &cfg.WorkRole)
}
