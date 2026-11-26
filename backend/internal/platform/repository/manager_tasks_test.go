package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	repo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
)

func TestTaskReadsPaginationPrivateOwnershipAndReports(t *testing.T) {
	db, s, mux, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	c, err := s.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: c.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(title string) manager.Session {
		t.Helper()
		r := manager.CreateSession{ConversationID: c.ID, Mode: "task", Title: &title, Input: &struct {
			Text string `json:"text"`
		}{Text: "任务要求：" + title}}
		v, _, e := s.CreateSession(ctx, w, actor, manager.NewID(), r)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	one, two, three := create("报表一"), create("报表二"), create("其他任务")
	if _, err = db.ExecContext(ctx, `UPDATE manager_tasks SET created_at='2026-11-17T01:00:00Z',source_chat_session_id=$1`, chat.ID); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + w + "/tasks"
	get := func(path string, status int) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Cookie", cookie)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != status {
			t.Fatalf("%s: %d %s", path, out.Code, out.Body.String())
		}
		var body map[string]any
		if err = json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	first := get(base+"?limit=1&include_counts=true&q="+url.QueryEscape("报表"), 200)
	if counts := first["status_counts"].(map[string]any); counts["queued"] != float64(2) {
		t.Fatalf("filtered counts: %v", counts)
	}
	if items := get(base+"?work_item_id="+manager.NewID(), 200)["items"].([]any); len(items) != 0 {
		t.Fatal(items)
	}
	get(base+"?include_counts=invalid", 400)
	second := get(base+"?limit=1&q="+url.QueryEscape("报表")+"&cursor="+url.QueryEscape(first["next_cursor"].(string)), 200)
	f := first["items"].([]any)[0].(map[string]any)
	v := second["items"].([]any)[0].(map[string]any)
	if f["task_id"] == v["task_id"] || second["next_cursor"] != nil {
		t.Fatal(first, second)
	}
	get(base+"?q=other&cursor="+url.QueryEscape(first["next_cursor"].(string)), 400)
	for _, q := range []string{"?limit=101", "?mode=task", "?q=a&q=b", "?execution_state=completed", "?agent_id=no"} {
		get(base+q, 400)
	}
	get(base+"/"+manager.NewID(), 404)
	get(base+"/bad-id", 404)
	if items := get(base+"?q="+*one.TaskID, 200)["items"].([]any); len(items) != 1 {
		t.Fatal(items)
	}
	detail := get(base+"/"+*one.TaskID, 200)
	if detail["session_id"] != one.ID || detail["latest_turn"] != nil || detail["initial_input"].(map[string]any)["text"] != "任务要求：报表一" {
		t.Fatal(detail)
	}
	if strings.Contains(string(mustJSON(t, detail)), "private employee instruction") {
		t.Fatal("private configuration exposed")
	}
	// A committed Turn and a newer queued Input remain separate facts.
	turn := manager.NewID()
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,result_summary,finished_at) VALUES($1,$2,$3,$4,1,$5,1,'completed',$6,now())`, turn, w, one.ID, *one.InitialInputID, manager.NewID(), strings.Repeat("中", 1700)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='processed',turn_id=$2,finished_at=now() WHERE id=$1`, *one.InitialInputID, turn); err != nil {
		t.Fatal(err)
	}
	newInput, _, err := s.SubmitInput(ctx, w, actor, one.ID, "follow-up", "补充要求")
	if err != nil {
		t.Fatal(err)
	}
	detail = get(base+"/"+*one.TaskID, 200)
	if detail["latest_turn"].(map[string]any)["status"] != "completed" || detail["latest_input"].(map[string]any)["id"] != newInput.ID || detail["execution"].(map[string]any)["state"] != "queued" {
		t.Fatal(detail)
	}
	preview := detail["latest_result"].(map[string]any)
	if preview["truncated"] != true || len([]rune(preview["text"].(string))) != 1600 {
		t.Fatal(preview)
	}
	if len(get(base+"?execution_state=queued&latest_turn_status=completed", 200)["items"].([]any)) != 1 {
		t.Fatal("combined filtering failed")
	}
	reports := func(id string) []any { return get(base+"/"+id+"/reports", 200)["items"].([]any) }
	if r := reports(*one.TaskID); len(r) != 1 || r[0].(map[string]any)["status"] != "pending" {
		t.Fatal(r)
	}
	store := &repo.Store{DB: db}
	if _, err = store.ReportTaskResult(ctx, manager.TaskResult{TurnID: turn}); err != nil {
		t.Fatal(err)
	}
	r := reports(*one.TaskID)[0].(map[string]any)
	if r["status"] != "admitted" || r["target"].(map[string]any)["input_status"] != "queued" {
		t.Fatal(r)
	}
	get(base+"/"+*one.TaskID+"/reports?turn_id="+turn+"&input_id="+newInput.ID, 400)
	// Ended inputs without Turns also have report entries, including historical exclusions.
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='rejected',finished_at=now(),report_enabled=false WHERE id=$1`, *two.InitialInputID); err != nil {
		t.Fatal(err)
	}
	if r = reports(*two.TaskID)[0].(map[string]any); r["status"] != "not_enabled" || r["source_turn_id"] != nil {
		t.Fatal(r)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='canceled',finished_at=now() WHERE id=$1`, *three.InitialInputID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET lifecycle='archived',archived_at=now() WHERE id=$1`, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReportTaskResult(ctx, manager.TaskResult{InputID: *three.InitialInputID}); err != nil {
		t.Fatal(err)
	}
	if r = reports(*three.TaskID)[0].(map[string]any); r["status"] != "suppressed" || r["reason"] != "target_archived" {
		t.Fatal(r)
	}
	more, _, err := s.SubmitInput(ctx, w, actor, one.ID, "report-page", "分页输入")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_inputs SET status='canceled',finished_at=now() WHERE id IN ($1,$2)`, newInput.ID, more.ID); err != nil {
		t.Fatal(err)
	}
	rp := get(base+"/"+*one.TaskID+"/reports?limit=1", 200)
	rpNext := get(base+"/"+*one.TaskID+"/reports?limit=1&cursor="+url.QueryEscape(rp["next_cursor"].(string)), 200)
	if rp["items"].([]any)[0].(map[string]any)["source_input_id"] == rpNext["items"].([]any)[0].(map[string]any)["source_input_id"] {
		t.Fatal("duplicate report page")
	}
	get(base+"/"+*two.TaskID+"/reports?cursor="+url.QueryEscape(rp["next_cursor"].(string)), 400)
	// Another active workspace member still cannot read a private conversation.
	var other string
	if err = db.QueryRowContext(ctx, `INSERT INTO platform_accounts(id,email,name,password_hash) VALUES($1,'task-reader@example.com','Other','unused') RETURNING id`, manager.NewID()).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, other); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTasks(ctx, w, other, "", manager.TaskFilter{})
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	if _, err = s.GetTask(ctx, w, other, *one.TaskID); err == nil {
		t.Fatal("other member read private task")
	}
	if _, err = s.ListTaskReports(ctx, w, other, *one.TaskID, "", manager.ReportFilter{}); err == nil {
		t.Fatal("other member read reports")
	}
	// Completed turn artifacts are returned in authenticated task detail and downloadable by authorized owner only.
	assets, err := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.Store.(*repo.Store).WorkAssets = assets
	artifactBytes := []byte("# Q3 归因报告\n总额 184200")
	artifactVer, err := assets.PutWorkFile(ctx, w, artifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	artifactID := manager.NewID()
	if _, err = db.ExecContext(ctx, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes) VALUES($1,$2,'outputs/report.md','report.md','text/markdown',$3,$4)`, artifactID, turn, artifactVer, len(artifactBytes)); err != nil {
		t.Fatal(err)
	}
	detail = get(base+"/"+*one.TaskID, 200)
	artifacts := detail["artifacts"].([]any)
	if len(artifacts) != 1 || artifacts[0].(map[string]any)["id"] != artifactID || artifacts[0].(map[string]any)["name"] != "report.md" {
		t.Fatalf("unexpected authenticated artifacts: %v", artifacts)
	}
	dlReq := httptest.NewRequest("GET", base+"/"+*one.TaskID+"/artifacts/"+artifactID, nil)
	dlReq.Header.Set("Cookie", cookie)
	dlRes := httptest.NewRecorder()
	mux.ServeHTTP(dlRes, dlReq)
	if dlRes.Code != 200 || !bytes.Equal(dlRes.Body.Bytes(), artifactBytes) || !strings.HasPrefix(dlRes.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("artifact download failed: %d %q", dlRes.Code, dlRes.Body.String())
	}
	if _, _, err = s.TaskArtifact(ctx, w, other, *one.TaskID, artifactID); err == nil {
		t.Fatal("other member downloaded private task artifact")
	}
	if _, _, err = s.TaskArtifact(ctx, w, actor, *two.TaskID, artifactID); err == nil {
		t.Fatal("cross-task artifact download succeeded")
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, w, actor); err != nil {
		t.Fatal(err)
	}
	get(base+"/"+*one.TaskID, 404)
	get(base+"/"+*one.TaskID+"/reports", 404)
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestTaskCenterWorkLinksRespectBothReaders(t *testing.T) {
	db, svc, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	conversation, err := svc.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	space, _, err := svc.CreateTaskSpace(ctx, w, actor, manager.NewID(), manager.TaskSpaceCreate{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	create := func(body manager.CreateSession) manager.Session {
		t.Helper()
		body.Mode = "task"
		body.Input = &struct {
			Text string `json:"text"`
		}{Text: "核对明细"}
		task, _, err := svc.CreateSession(ctx, w, actor, manager.NewID(), body)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	root := create(manager.CreateSession{ConversationID: conversation.ID})
	child := create(manager.CreateSession{TaskSpaceID: space.ID, AgentID: agent})
	before, err := svc.GetTask(ctx, w, actor, *child.TaskID)
	if err != nil || !before.Actions["supplement"].Allowed {
		t.Fatalf("ordinary space task should accept supplements: %v %v", before.Actions, err)
	}
	turn, work := manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 VALUES($1,$2,$3,$4,1,$5,1,'completed',now())`, turn, w, root.ID, *root.InitialInputID, manager.NewID())
	reviewExec(t, db, `INSERT INTO manager_work_items(id,workspace_id,root_session_id,root_turn_id,authority_input_id,root_stop_revision,task_space_id,title,goal,criteria,status)
 VALUES($1,$2,$3,$4,$5,0,$6,'对账','核对差异','列出明细','working')`, work, w, root.ID, turn, *root.InitialInputID, space.ID)
	reviewExec(t, db, `INSERT INTO manager_work_assignments(id,work_item_id,source_agent_id,target_agent_id,session_id,summary)
 VALUES($1,$2,$3,$3,$4,'接手')`, manager.NewID(), work, agent, child.ID)
	after, err := svc.GetTask(ctx, w, actor, *child.TaskID)
	if err != nil || after.Actions["supplement"].Allowed || after.Actions["supplement"].Reason == nil || *after.Actions["supplement"].Reason != "system_managed_input" || !after.Actions["stop"].Allowed {
		t.Fatalf("work space should only restrict manual input: %v %v", after.Actions, err)
	}
	filter := manager.TaskFilter{WorkItem: work, IncludeCounts: true, Limit: 1}
	first, err := svc.ListTasks(ctx, w, actor, "", filter)
	if err != nil || len(first.Items) != 1 || first.NextCursor == nil || first.StatusCounts["queued"] != 2 {
		t.Fatal(first, err)
	}
	second, err := svc.ListTasks(ctx, w, actor, *first.NextCursor, filter)
	if err != nil || len(second.Items) != 1 || second.Items[0].TaskID == first.Items[0].TaskID || second.NextCursor != nil {
		t.Fatal(second, err)
	}
	for _, row := range append(first.Items, second.Items...) {
		if row.WorkItemID == nil || *row.WorkItemID != work {
			t.Fatal(row)
		}
	}
	board, err := svc.ListWork(ctx, w, actor, "working", agent, "", 1, true)
	if err != nil || len(board.Items) != 1 || board.StatusCounts["working"] != 1 || len(board.Items[0].LinkedTaskIDs) != 2 {
		t.Fatal(board, err)
	}
	other := manager.NewID()
	insertWorkspaceAccount(t, db, other, "linked-space-reader@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, other)
	reviewExec(t, db, `UPDATE manager_task_spaces SET created_by=$1 WHERE id=$2`, other, space.ID)
	board, err = svc.ListWork(ctx, w, actor, "", "", "", 20, true)
	if err != nil || len(board.Items) != 1 || len(board.Items[0].LinkedTaskIDs) != 1 || board.Items[0].LinkedTaskIDs[0] != *root.TaskID {
		t.Fatal(board, err)
	}
	detail, err := svc.GetTask(ctx, w, other, *child.TaskID)
	if err != nil || detail.WorkItemID != nil {
		t.Fatalf("child reader learned private root: %v %v", detail, err)
	}
	page, err := svc.ListTasks(ctx, w, other, "", filter)
	if err != nil || len(page.Items) != 0 || len(page.StatusCounts) != 0 {
		t.Fatal(page, err)
	}
}
