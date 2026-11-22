package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const workGetTool = "mcp__work_items__work_item_get_current"
const workAcceptTool = "mcp__work_items__work_item_accept"

type workRuntimeFixture struct {
	markUnknown                func()
	b, c                       string
	firstResult, releaseResult chan struct{}
}

type modelWorkItem struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Result  string `json:"result"`
	Files   []struct {
		ID string `json:"id"`
	} `json:"files"`
}

func (f workRuntimeFixture) reply(t *testing.T, w http.ResponseWriter, r *http.Request, req runtimeRequest) bool {
	t.Helper()
	var user, last string
	var item modelWorkItem
	readFiles := map[string]bool{}
	seenRoot := false
	for _, m := range req.Messages {
		if m.Role == "user" {
			user, last = fmt.Sprint(m.Content), ""
			item = modelWorkItem{}
			readFiles = map[string]bool{}
			seenRoot = seenRoot || strings.Contains(user, "WORK-ROOT")
		}
		if m.Role == "tool" {
			last = fmt.Sprint(m.Content)
			if strings.Contains(last, `"data_base64"`) {
				var chunk struct {
					File struct{ ID, Name string }
					Text string
				}
				if e := json.Unmarshal([]byte(last), &chunk); e != nil {
					t.Errorf("file chunk: %v", e)
				}
				want := map[string]string{"signed-brief.txt": "agreed original brief", "result.txt": "C produced artifact"}
				if chunk.Text != want[chunk.File.Name] {
					t.Errorf("wrong immutable content: %s", chunk.File.Name)
				}
				readFiles[chunk.File.ID] = true
			}
			if strings.Contains(last, `"version"`) {
				var current modelWorkItem
				if json.Unmarshal([]byte(last), &current) == nil && current.Version != "" && current.Status != "" {
					item = current
				}
			}
		}
	}
	names := map[string]bool{}
	var write string
	for _, tool := range req.Tools {
		name := tool.Function.Name
		names[name] = true
		if strings.HasSuffix(name, "__write") {
			write = name
		}
	}
	single := func(name string, a any) {
		if !names[name] {
			t.Errorf("work tool missing: %s", name)
			w.WriteHeader(500)
			return
		}
		humanToolStream(t, w, []string{name}, []any{a})
	}
	boundary := func(name string, a any) {
		if !names[name] || write == "" {
			t.Errorf("work boundary or write missing: %s", name)
			w.WriteHeader(500)
			return
		}
		humanToolStream(t, w, []string{write, name, write}, []any{map[string]string{"path": "work-forbidden-before", "content": "unsafe"}, a, map[string]string{"path": "work-forbidden-after", "content": "unsafe"}})
	}
	switch {
	case strings.Contains(user, "WORK-ROOT"):
		if names[workGetTool] || names[workAcceptTool] || names["work_item_submit_result"] {
			t.Error("ordinary task saw assignment or review tools")
		}
		if last == "" {
			single("mcp__work_items__work_item_list_handoff_employees", map[string]string{"q": "Handoff B"})
		} else {
			if !strings.Contains(last, f.b) {
				t.Errorf("eligible target missing: %s", last)
			}
			args := map[string]any{"target_employee_id": f.b, "title": "Verify handoff", "goal": "Check report", "criteria": "Include reconciliation", "summary": "WORK-FIRST-B"}
			if strings.Contains(user, "WORK-UNKNOWN") {
				args["summary"] = "WORK-UNKNOWN"
			}
			if strings.Contains(user, "WORK-FAIL") {
				args["summary"] = "WORK-DEFINITE-FAIL"
			}
			if strings.Contains(user, "WORK-FILES") {
				args["file_paths"] = []string{"signed-brief.txt"}
			}
			boundary("work_item_handoff", args)
		}
		return true
	case strings.Contains(user, "WORK-QUEUED"):
		humanTextStream(t, w, "ordinary input processed")
		return true
	case strings.Contains(user, "你收到一个事项处理任务"), strings.Contains(user, "已收到交接事项的新成果"):
		assignee := strings.Contains(user, "你收到一个事项处理任务")
		if assignee && (strings.Contains(user, "WORK-DEFINITE-FAIL") || strings.Contains(user, "WORK-UNKNOWN")) {
			if strings.Contains(user, "WORK-UNKNOWN") {
				f.markUnknown()
			}
			http.Error(w, `{"error":{"message":"fixture execution failed"}}`, http.StatusBadRequest)
			return true
		}
		if assignee && (names[workAcceptTool] || !names["mcp__task_space_history__task_space_history_get_task"]) {
			t.Error("assignee saw review tools or lost TaskSpace summary reads")
		}
		if !assignee && (names["work_item_submit_result"] || !seenRoot) {
			t.Error("reviewer saw assignee tools or lost the original native history")
		}
		if assignee && seenRoot {
			t.Error("assignee received original native history")
		}
		if last == "" {
			single(workGetTool, map[string]string{})
			return true
		}
		var receipt struct{ Status string }
		if !assignee && json.Unmarshal([]byte(last), &receipt) == nil && receipt.Status == "completed" {
			humanTextStream(t, w, "original task accepted the revision")
			return true
		}
		if item.Version == "" {
			t.Errorf("unexpected work item result: %s", last)
			w.WriteHeader(500)
			return true
		}
		// The model carries only the item version. The current input determines
		// the assignment/submission the server is allowed to act on.
		args := map[string]any{"expected_version": item.Version}
		if len(item.Files) > 0 {
			ids := []string{}
			for _, file := range item.Files {
				if !readFiles[file.ID] {
					single("mcp__work_items__work_item_read_file", map[string]string{"file_id": file.ID})
					return true
				}
				ids = append(ids, file.ID)
			}
			args["file_ids"] = ids
		}
		if assignee {
			if strings.Contains(user, "WORK-FIRST-B") {
				args["target_employee_id"], args["summary"] = f.c, "WORK-FIRST-C"
				boundary("work_item_handoff", args)
				return true
			}
			if strings.Contains(user, "WORK-FIRST-C") {
				select {
				case f.firstResult <- struct{}{}:
				default:
				}
				select {
				case <-f.releaseResult:
				case <-r.Context().Done():
					return true
				}
				if len(item.Files) > 0 {
					args["file_paths"] = []string{"result.txt"}
				}
				args["result"], args["completion_evidence"] = "first draft", "Reconciliation missing"
			} else {
				if !strings.Contains(user, "first draft") || !strings.Contains(user, "WORK-REWORK-B") {
					t.Error("rework context missing previous result or defects")
				}
				args["result"], args["completion_evidence"] = "fixed draft", "Reconciliation included"
			}
			boundary("work_item_submit_result", args)
			return true
		}
		if item.Result == "first draft" {
			args["target_employee_id"], args["summary"] = f.b, "WORK-REWORK-B: include reconciliation"
			boundary("work_item_handoff", args)
		} else {
			args["completion_evidence"] = "Checked the reconciliation"
			delete(args, "file_ids")
			single(workAcceptTool, args)
		}
		return true
	default:
		return false
	}
}

type workRuntimeCall func(string, string, any, string) map[string]any

func testRuntimeWorkItems(t *testing.T, ctx context.Context, db *sql.DB, store *managerrepo.Store, base, workspace, actor, agent, conversation, temp string, call workRuntimeCall, wait func(func() bool), f workRuntimeFixture) {
	t.Helper()
	project := filepath.Join(store.RuntimeRoot, "state", "sandboxes", "workspaces", workspace, "employees", agent, "conversations", conversation, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(project, "signed-brief.txt")
	if err := os.WriteFile(file, []byte("agreed original brief"), 0600); err != nil {
		t.Fatal(err)
	}
	root := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "WORK-ROOT WORK-FILES"}}, "work-root")["id"].(string)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		var snapshot []byte
		err := db.QueryRow(`SELECT jsonb_build_object('inputs',(SELECT jsonb_agg(jsonb_build_object('kind',i.source_kind,'status',i.status,'rejection',i.rejection_reason,'authorized',manager_input_actor_allowed(i.id))) FROM manager_inputs i WHERE i.session_id=$1),
 'work',(SELECT jsonb_agg(jsonb_build_object('status',status,'version',version,'submission',current_submission_id IS NOT NULL)) FROM manager_work_items WHERE root_session_id=$1),
 'boundaries',(SELECT jsonb_agg(jsonb_build_object('kind',kind,'state',state,'error',error_code)) FROM manager_work_boundaries),
 'required',(SELECT required_actions FROM manager_sessions WHERE id=$1))`, root).Scan(&snapshot)
		t.Logf("Work diagnostics: %s (%v)", snapshot, err)
	})

	wait(func() bool {
		select {
		case <-f.firstResult:
			return true
		default:
			return false
		}
	})
	path := base + "/sessions/" + root
	wait(func() bool {
		return strings.Contains(fmt.Sprint(call("GET", path, nil, "")["execution"]), "接手员工处理中")
	})
	input := call("POST", path+"/inputs", map[string]string{"text": "WORK-QUEUED", "delivery_mode": "steer"}, "work-queued")["id"].(string)
	if call("GET", path+"/inputs/"+input, nil, "")["status"] != "queued" {
		t.Fatal("ordinary input bypassed work wait")
	}
	if err := os.WriteFile(file, []byte("changed after handoff"), 0600); err != nil {
		t.Fatal(err)
	}
	setReviewer := func(enabled bool) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE platform_model_profiles SET enabled=$2 WHERE id=(SELECT model_profile_id FROM platform_agents WHERE id=$1)`, agent, enabled); err != nil {
			t.Fatal(err)
		}
	}
	var assignee, space, task string
	if err := db.QueryRowContext(ctx, `SELECT s.agent_id,s.task_space_id,s.task_id FROM manager_work_items w JOIN manager_work_assignments a ON a.id=w.current_assignment_id JOIN manager_sessions s ON s.id=a.session_id WHERE w.root_session_id=$1`, root).Scan(&assignee, &space, &task); err != nil {
		t.Fatal(err)
	}
	assertWorkAssigneeFences(t, ctx, db, store, root)
	product := filepath.Join(store.RuntimeRoot, "state", "sandboxes", "workspaces", workspace, "employees", assignee, "taskspaces", space, "tasks", task, "project", "result.txt")
	if err := os.WriteFile(product, []byte("C produced artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	setReviewer(false)
	close(f.releaseResult)
	wait(func() bool {
		return strings.Contains(fmt.Sprint(call("GET", path, nil, "")["execution"]), "等待原任务模型恢复")
	})
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND work_submission_id IS NOT NULL AND status='queued' AND turn_id IS NULL`, root).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("pending review=%d: %v", queued, err)
	}
	setReviewer(true)
	wait(func() bool { return call("GET", path+"/inputs/"+input, nil, "")["status"] == "processed" })
	assertWorkRuntime(t, ctx, db, root, input)
	page := call("GET", base+"/work-items?status=completed", nil, "")
	list := page["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("board returned %v", page)
	}
	item := list[0].(map[string]any)["id"].(string)
	detail := call("GET", base+"/work-items/"+item, nil, "")
	events := detail["events"].([]any)
	var sequence []string
	for _, v := range events {
		sequence = append(sequence, v.(map[string]any)["kind"].(string))
	}
	if strings.Join(sequence, ",") != "completed,submitted,started,returned,submitted,started,handoff,started,created" {
		t.Fatalf("timeline: %v", sequence)
	}
	var files, grants, notices int
	wait(func() bool {
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_work_events WHERE work_item_id=$1 AND notify AND notified_at IS NOT NULL`, item).Scan(&notices)
		return err == nil && notices == 3
	})
	if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT f.id),count(g.file_id) FROM manager_work_files f JOIN manager_work_file_selections g ON g.file_id=f.id JOIN manager_work_boundaries b ON b.id=f.boundary_id WHERE b.work_item_id=$1`, item).Scan(&files, &grants); err != nil || files != 2 || grants != 8 {
		t.Fatalf("immutable files=%d grants=%d: %v", files, grants, err)
	}
	if err := filepath.WalkDir(temp, func(path string, d fs.DirEntry, err error) error {
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if strings.HasPrefix(filepath.Base(path), "work-forbidden-") {
			t.Errorf("same-batch write escaped: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failed := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "WORK-ROOT WORK-FAIL"}}, "work-fail")["id"].(string)
	wait(func() bool {
		var closed bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_items w JOIN manager_sessions s ON s.id=w.root_session_id WHERE s.id=$1 AND w.status='failed' AND s.required_actions='[]' AND (SELECT count(*) FROM manager_work_events e WHERE e.work_item_id=w.id AND e.kind='failed' AND notified_at IS NOT NULL)=1)`, failed).Scan(&closed)
		return err == nil && closed
	})
	uncertain := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "WORK-ROOT WORK-UNKNOWN"}}, "work-uncertain")["id"].(string)
	wait(func() bool {
		var blocked bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_items w JOIN manager_sessions s ON s.id=w.root_session_id WHERE s.id=$1 AND w.status='blocked' AND s.required_actions<>'[]' AND NOT EXISTS(SELECT FROM manager_work_events e WHERE e.work_item_id=w.id AND e.kind='failed'))`, uncertain).Scan(&blocked)
		return err == nil && blocked
	})
	testTriggeredWorkRuntime(t, ctx, db, store, workspace, actor, agent, wait)
	policyURL := base + "/agents/" + f.b + "/handoff-scope"
	policy := call("GET", policyURL, nil, "")
	call("PUT", policyURL, map[string]any{"version": policy["version"], "scope": map[string]any{"mode": "denylist", "employee_ids": []string{agent}}}, "")
	var readable bool
	if err := db.QueryRowContext(ctx, `SELECT manager_work_file_allowed(w.current_submission_id,f.id) FROM manager_work_items w JOIN manager_work_boundaries b ON b.work_item_id=w.id JOIN manager_work_files f ON f.boundary_id=b.id WHERE w.id=$1 AND f.name='signed-brief.txt'`, item).Scan(&readable); err != nil || readable {
		t.Fatalf("revoked file remained readable: %v, %v", readable, err)
	}
}

// Exercise rejection while the real C model call is paused before it submits.
func assertWorkAssigneeFences(t *testing.T, ctx context.Context, db *sql.DB, store *managerrepo.Store, root string) {
	t.Helper()
	var scope manager.WorkScope
	var version int64
	var previousInput string
	id := &scope.Identity
	err := db.QueryRowContext(ctx, `SELECT s.id,s.engine_id,s.assignment_id,s.generation,t.input_id,w.version,
 (SELECT prior.id FROM manager_work_assignments a JOIN manager_inputs prior
   ON prior.work_assignment_id=a.id AND prior.source_kind='work_item_assignment'
  WHERE a.work_item_id=w.id AND a.id<>w.current_assignment_id)
 FROM manager_work_items w JOIN manager_work_assignments a ON a.id=w.current_assignment_id
 JOIN manager_sessions s ON s.id=a.session_id JOIN manager_turns t ON t.id=s.active_turn_id
 WHERE w.root_session_id=$1`, root).Scan(&id.SessionID, &id.EngineID, &id.AssignmentID, &id.Generation, &scope.InputID, &version, &previousInput)
	if err != nil || version <= 1 {
		t.Fatalf("current assignment scope: version=%d, err=%v", version, err)
	}
	args := manager.WorkArguments{
		OperationID: "fixture-stale-version", ExpectedVersion: version - 1,
		Result: "must not be submitted", Evidence: "stale work item version",
	}
	_, err = store.WorkTool(ctx, scope, "submit", args)
	var failure *manager.Error
	if !errors.As(err, &failure) || failure.Code != "work_version_changed" {
		t.Fatalf("stale version must conflict, got %v", err)
	}
	// Current engine credentials and version cannot make an old assignee Input
	// the authority for this in-flight task.
	scope.InputID, args.ExpectedVersion, args.OperationID = previousInput, version, "fixture-old-assignee"
	_, err = store.WorkTool(ctx, scope, "submit", args)
	if !errors.As(err, &failure) || failure.Code != "work_tool_forbidden" {
		t.Fatalf("previous assignment Input must be refused, got %v", err)
	}
	var prepared bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_boundaries WHERE session_id=$1)`, id.SessionID).Scan(&prepared); err != nil || prepared {
		t.Fatalf("refused submissions must not prepare a boundary: %v (%v)", prepared, err)
	}
}

func assertWorkRuntime(t *testing.T, ctx context.Context, db *sql.DB, root, ordinary string) {
	t.Helper()
	var status string
	var assignments, spaces, submissions, prioritized int
	err := db.QueryRowContext(ctx, `SELECT w.status,(SELECT count(*) FROM manager_work_assignments a WHERE a.work_item_id=w.id),
 (SELECT count(DISTINCT s.task_space_id) FROM manager_work_assignments a JOIN manager_sessions s ON s.id=a.session_id WHERE a.work_item_id=w.id),
 (SELECT count(*) FROM manager_inputs i WHERE i.session_id=w.root_session_id AND i.work_submission_id IS NOT NULL AND i.status='processed'),
 (SELECT count(*) FROM manager_inputs i JOIN manager_turns t ON t.id=i.turn_id JOIN manager_inputs o ON o.id=NULLIF($2,'')::uuid
 JOIN manager_turns ot ON ot.id=o.turn_id WHERE i.session_id=w.root_session_id AND i.work_submission_id IS NOT NULL AND t.turn_no<ot.turn_no)
 FROM manager_work_items w WHERE w.root_session_id=$1`, root, ordinary).Scan(&status, &assignments, &spaces, &submissions, &prioritized)
	if err != nil || status != "completed" || assignments != 3 || spaces != 1 || submissions != 2 || (ordinary != "" && prioritized != 2) {
		t.Fatalf("status=%s assignments=%d spaces=%d submissions=%d prioritized=%d err=%v", status, assignments, spaces, submissions, prioritized, err)
	}
}

func testTriggeredWorkRuntime(t *testing.T, ctx context.Context, db *sql.DB, store *managerrepo.Store, workspace, actor, agent string, wait func(func() bool)) {
	t.Helper()
	group := manager.NewID()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform_web_groups(id,workspace_id,title,created_by)VALUES($1,$2,'Handoff notifications',$3)`, group, workspace, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, group, workspace, agent); err != nil {
		t.Fatal(err)
	}
	dest, err := store.ResolveTriggerGroupCandidate(ctx, workspace, actor, agent, manager.TriggerGroupResolve{Provider: "web", CandidateID: group})
	if err != nil {
		t.Fatal(err)
	}
	targets := json.RawMessage(`[{"conversation_id":"` + dest.ConversationID + `"}]`)
	config := json.RawMessage(`{"adapter":"generic"}`)
	b, err := store.SaveTrigger(ctx, workspace, actor, agent, "", manager.TriggerWrite{Kind: "webhook", Config: config, AllowedActions: []string{"create_task"}, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateTriggerCredential(ctx, workspace, actor, agent, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = store.SaveTrigger(ctx, workspace, actor, agent, b.ID, manager.TriggerWrite{Version: b.Version, Kind: b.Kind, Enabled: true, Config: config, AllowedActions: []string{"create_task"}, NotificationTargets: targets})
	if err != nil {
		t.Fatal(err)
	}
	first, err := signedTrigger(t, store, b, cred, manager.TriggerCommand{Version: 1, EventID: "work-trigger", Action: "create_task", Description: "WORK-ROOT"})
	if err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		var completed bool
		if e := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_work_items WHERE root_session_id=$1 AND status='completed' AND (SELECT count(*) FROM manager_inputs i WHERE i.session_id=$1 AND i.work_submission_id IS NOT NULL AND i.status='processed')=2)`, first.SessionID).Scan(&completed); e != nil {
			t.Fatal(e)
		}
		return completed
	})
	assertWorkRuntime(t, ctx, db, first.SessionID, "")
	wait(func() bool {
		if err := store.MaterializeTriggerNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		var messages int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_web_group_messages WHERE group_id=$1 AND author_kind='system'`, group).Scan(&messages)
		return err == nil && messages >= 3
	})
	var labels string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(n.result_status,',' ORDER BY n.created_at),bool_and(n.status='sent') FROM manager_trigger_notifications n JOIN manager_tasks k ON k.id=n.task_id WHERE k.id=(SELECT task_id FROM manager_sessions WHERE id=$1) GROUP BY n.task_id`, first.SessionID).Scan(&labels, new(bool)); err != nil || labels != "created,returned,completed" {
		t.Fatalf("trigger milestones: %s (%v)", labels, err)
	}
}
