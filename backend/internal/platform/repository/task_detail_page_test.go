package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
)

const taskDetailPagePath = "/api/v1/public/task-detail"

type taskDetailPageClient struct {
	mux   *http.ServeMux
	token string
}

func (c taskDetailPageClient) request(t *testing.T, method, path, key, body, cursor string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, taskDetailPagePath+path, strings.NewReader(body))
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if cursor != "" {
		r.Header.Set("Last-Event-ID", cursor)
	}
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s => %d want %d: %s", method, path, w.Code, status, w.Body)
	}
	return w
}

func detailResponse(t *testing.T, w *httptest.ResponseRecorder) manager.TaskDetailSnapshot {
	t.Helper()
	var view manager.TaskDetailSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestTaskDetailPageTokenLifecycleAndSilentInput(t *testing.T) {
	db, store, issue := taskDetailGrantFixture(t, "direct")
	grant, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	m := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: m}).Routes(mux)
	client := taskDetailPageClient{mux, grant.Token}
	task, chat := grant.Grant.SessionID, grant.Grant.SourceChatSessionID
	read := func() manager.TaskDetailSnapshot {
		return detailResponse(t, client.request(t, "GET", "", "", "", "", 200))
	}
	if view := read(); view.Permission != "write" || !view.CanAppend || !view.CanStop {
		t.Fatalf("initial writable task missing operations: %+v", view)
	}
	reviewExec(t, db, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event)
 VALUES($1,$2,999,'{"type":"agent.tool_use","status":"running","input":{"path":"INTERNAL_EXECUTION_PATH"}}')`, issue.Destination.WorkspaceID, task)
	step := client.request(t, "GET", "", "", "", "", 200)
	if !strings.Contains(step.Body.String(), "正在执行一个步骤") || strings.Contains(step.Body.String(), "INTERNAL_EXECUTION_PATH") {
		t.Fatalf("private progress omitted or leaked a raw tool argument: %s", step.Body)
	}
	client.request(t, "POST", "/append", "spoof", `{"text":"test","actor_id":"peer"}`, "", 400)
	for _, typ := range []string{"human_confirmation", "work_item"} {
		reviewExec(t, db, `UPDATE manager_sessions SET required_actions=jsonb_build_array(jsonb_build_object('type',$2::text)) WHERE id=$1`, task, typ)
		if view := read(); view.Status != "waiting_for_action" || !view.CanAppend || view.CanStop {
			t.Fatalf("incorrect waiting actions: %+v", view)
		}
		appendReq := `{"text":"review details ` + typ + `"}`
		w := client.request(t, "POST", "/append", typ, appendReq, "", 200)
		if view := detailResponse(t, w); view.InputReceipt == nil || view.InputReceipt.State != "queued" {
			t.Fatalf("append resumed a suspended task: %+v", view)
		}
		client.request(t, "POST", "/append", typ, appendReq, "", 200)
		client.request(t, "POST", "/stop", typ, "{}", "", 409)
	}
	// The accepted input and note preserve their roles and original route.
	var inputs, notes int
	err = db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE i.session_id=$1 AND i.trigger_agent_loop AND i.delivery_mode='queue' AND i.channel_principal_id=$3 AND i.submitted_by IS NULL),
 count(*) FILTER(WHERE i.session_id=$2 AND i.source_kind='system' AND NOT i.trigger_agent_loop AND strpos(i.text,'session_id=')=0 AND strpos(i.text,'task_id='||$4::text)>0 AND strpos(i.text,$1::text)=0)
 FROM manager_inputs i WHERE i.text LIKE '%review details%'`, task, chat, issue.Source.ActorID, grant.Grant.TaskID).Scan(&inputs, &notes)
	if err != nil || inputs != 2 || notes != 2 {
		t.Fatalf("expected two authorized task inputs and two silent source notes: %d %d %v", inputs, notes, err)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET required_actions='[]',engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, task, manager.NewID(), manager.NewID())
	w := client.request(t, "POST", "/stop", "stop-once", "{}", "", 200)
	view := detailResponse(t, w)
	if view.Status != "stopping" || view.CanContinue {
		t.Fatalf("stop was not confirmed by the Engine yet: %+v", view)
	}
	client.request(t, "POST", "/append", "during-stop", `{"text":"late"}`, "", 409)
	client.request(t, "POST", "/continue", "during-stop", `{"text":"explicit","stop_version":"1"}`, "", 409)
	reviewExec(t, db, `UPDATE manager_sessions SET applied_stop_revision=stop_revision,background_process_count=NULL WHERE id=$1`, task)
	client.request(t, "POST", "/continue", "unknown-background", `{"text":"explicit","stop_version":"1"}`, "", 409)
	reviewExec(t, db, `UPDATE manager_sessions SET background_process_count=0 WHERE id=$1`, task)
	view = read()
	if view.Status != "stopped" || !view.CanContinue || view.StopVersion == "" {
		t.Fatalf("unexpected stop state: %+v", view)
	}
	client.request(t, "POST", "/append", "late", `{"text":"late"}`, "", 409)
	client.request(t, "POST", "/continue", "wrong-version", `{"text":"explicit","stop_version":"999"}`, "", 409)
	cont := `{"text":"explicit","stop_version":"` + view.StopVersion + `"}`
	client.request(t, "POST", "/continue", "resume", cont, "", 200)
	client.request(t, "POST", "/continue", "resume", cont, "", 200)
	var restartNote string
	err = db.QueryRowContext(ctx, `SELECT text FROM manager_inputs WHERE session_id=$1 AND source_kind='system' AND NOT trigger_agent_loop
 AND text LIKE '%已提交新一轮%' ORDER BY seq DESC LIMIT 1`, chat).Scan(&restartNote)
	if err != nil || !strings.Contains(restartNote, "task_id="+grant.Grant.TaskID) || !strings.HasSuffix(restartNote, `"explicit"`) ||
		strings.Contains(restartNote, "session_id=") || strings.Contains(restartNote, task) {
		t.Fatalf("restart note must contain only task identity and quoted original text: %q %v", restartNote, err)
	}
	client.request(t, "POST", "/continue", "different-tab", cont, "", 409)
	client.request(t, "POST", "/stop", "stop-once", "{}", "", 200)
	if view = read(); view.Status != "queued" {
		t.Fatalf("replayed stop interrupted the next round: %+v", view)
	}
	// A genuine later turn (unlike the silent note) still reports to the original chat.
	var sourceInput string
	err = db.QueryRowContext(ctx, `SELECT input_id FROM manager_task_detail_operations WHERE grant_id=$1 AND action='continue'`, grant.Grant.ID).Scan(&sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	turn := reviewStartTurn(t, db, issue.Destination.WorkspaceID, task, sourceInput, manager.NewID(), 1)
	reviewFinishTurn(t, db, task, sourceInput, turn)
	rpt, err := store.ReportTaskResult(ctx, manager.TaskResult{TurnID: turn})
	if err != nil || rpt.Status != "admitted" || rpt.TargetInputID == nil {
		t.Fatalf("actual continuation did not produce its normal report: %+v %v", rpt, err)
	}
	var target string
	err = db.QueryRowContext(ctx, `SELECT session_id FROM manager_inputs WHERE id=$1 AND trigger_agent_loop`, *rpt.TargetInputID).Scan(&target)
	if err != nil || target != chat {
		t.Fatalf("continuation changed the stored report destination: %s %v", target, err)
	}
	assets, err := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store.WorkAssets = assets
	content := []byte("approved task result")
	assetVersion, err := assets.PutWorkFile(ctx, issue.Destination.WorkspaceID, content)
	if err != nil {
		t.Fatal(err)
	}
	artifactID := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes)
 VALUES($1,$2,'private-path/result.txt','result.txt','text/plain',$3,$4)`, artifactID, turn, assetVersion, len(content))
	w = client.request(t, "GET", "", "", "", "", 200)
	if view = detailResponse(t, w); len(view.Artifacts) != 1 || view.Artifacts[0].ID != artifactID || strings.Contains(w.Body.String(), "private-path") {
		t.Fatalf("unexpected artifact projection: %v", view.Artifacts)
	}
	w = client.request(t, "GET", "/artifacts/"+artifactID, "", "", "", 200)
	if !bytes.Equal(w.Body.Bytes(), content) || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("download must return an attachment with the published version bytes")
	}
	if err = store.RevokeTaskDetailGrant(ctx, issue.Destination.WorkspaceID, grant.Grant.ID); err != nil {
		t.Fatal(err)
	}
	client.request(t, "GET", "", "", "", "", 404)
}

func TestTaskDetailGroupPageCanReadTaskArtifacts(t *testing.T) {
	db, store, issue := taskDetailGrantFixture(t, "group")
	first, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	second, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	task := first.Grant.SessionID
	reviewExec(t, db, `UPDATE manager_sessions SET title='UNPUBLISHED TITLE' WHERE id=$1`, task)
	reviewExec(t, db, `INSERT INTO manager_session_events(workspace_id,session_id,seq,event)
 VALUES ($1,$2,999,'{"type":"turn.updated","status":"running","details":"UNPUBLISHED PAYLOAD"}')`, issue.Destination.WorkspaceID, task)
	var taskInput string
	err = db.QueryRowContext(t.Context(), `SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task).Scan(&taskInput)
	if err != nil {
		t.Fatal(err)
	}
	turn := reviewStartTurn(t, db, issue.Destination.WorkspaceID, task, taskInput, manager.NewID(), 1)
	reviewFinishTurn(t, db, task, taskInput, turn)
	unpublished := manager.NewID()
	assets, err := skillasset.NewStore(t.Context(), skillasset.Config{LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store.WorkAssets = assets
	content := []byte("task artifact")
	version, err := assets.PutWorkFile(t.Context(), issue.Destination.WorkspaceID, content)
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes)
 VALUES($1,$2,'secret.pdf','task result.pdf','application/pdf',$3,$4)`, unpublished, turn, version, len(content))
	m := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: m}).Routes(mux)
	client := taskDetailPageClient{mux, first.Token}
	w := client.request(t, "GET", "", "", "", "", 200)
	view := detailResponse(t, w)
	if view.Permission != "read" || view.CanAppend || view.CanStop || view.CanContinue || len(view.Activities) != 1 || len(view.Artifacts) != 1 || view.Artifacts[0].ID != unpublished {
		t.Fatalf("read grant escapes its boundaries: %+v", view)
	}
	for _, forbidden := range []string{task, issue.Destination.WorkspaceID, first.Grant.ID, first.Token, "UNPUBLISHED", "fixture instruction"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("presence of hidden value in group projection")
		}
	}
	client.request(t, "POST", "/append", "readonly", `{"text":"cannot append"}`, "", 403)
	client.request(t, "POST", "/stop", "readonly", "{}", "", 403)
	client.request(t, "POST", "/continue", "readonly", `{"text":"cannot continue","stop_version":"1"}`, "", 403)
	download := client.request(t, "GET", "/artifacts/"+unpublished, "", "", "", 200)
	if !bytes.Equal(download.Body.Bytes(), content) {
		t.Fatal("read-only token did not download the task artifact")
	}
	other := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes)
 SELECT $1,t.id,'other.pdf','other.pdf','application/pdf',$2,$3 FROM manager_turns t
 WHERE t.session_id=$4 LIMIT 1`, other, version, len(content), first.Grant.SourceChatSessionID)
	client.request(t, "GET", "/artifacts/"+other, "", "", "", 404)
	client.request(t, "GET", "/artifacts/"+manager.NewID(), "", "", "", 404)
	taskDetailPageClient{mux, second.Token}.request(t, "GET", "/events", "", "", view.Cursor, 400)
	taskDetailPageClient{mux, ""}.request(t, "GET", "", "", "", "", 401)
}

func TestTaskDetailAuthenticatedWebControlRejectsPendingAction(t *testing.T) {
	db, m, mux, ws, actor, employee, cookie := managerFixture(t)
	ctx := t.Context()
	conv, err := m.CreateConversation(ctx, ws, actor, manager.CreateConversation{AgentID: employee})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := m.CreateSession(ctx, ws, actor, "web-task", manager.CreateSession{
		ConversationID: conv.ID,
		Mode:           "task",
		Input: &struct {
			Text string `json:"text"`
		}{Text: "task input"},
	})
	if err != nil {
		t.Fatal(err)
	}
	control := func(action string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/v1/workspaces/"+ws+"/sessions/"+task.ID+"/"+action, strings.NewReader("{}"))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("X-CSRF-Protection", "1")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want || want == 409 && !strings.Contains(w.Body.String(), "task_waiting_for_action") {
			t.Fatalf("authenticated %s got %d, want %d: %s", action, w.Code, want, w.Body)
		}
	}
	for _, typ := range []string{"human_confirmation", "work_item"} {
		reviewExec(t, db, `UPDATE manager_sessions SET required_actions=jsonb_build_array(jsonb_build_object('type',$2::text)) WHERE id=$1`, task.ID, typ)
		control("stop", 409)
		control("archive", 409)
		detail, err := m.GetTask(ctx, ws, actor, *task.TaskID)
		if err != nil || detail.Lifecycle != "active" || detail.Actions["stop"].Allowed || detail.Actions["archive"].Allowed {
			t.Fatalf("waiting task controls were not preserved in the authenticated page projection: %+v %v", detail.Actions, err)
		}
	}
	reviewExec(t, db, `UPDATE manager_sessions SET required_actions='[]' WHERE id=$1`, task.ID)
	control("stop", 200)
}

func TestTaskDetailSSEReconnectAndRevoke(t *testing.T) {
	db, store, issue := taskDetailGrantFixture(t, "group")
	grant, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	m := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: m}).Routes(mux)
	api := httptest.NewServer(mux)
	defer api.Close()
	original := detailResponse(t, taskDetailPageClient{mux, grant.Token}.request(t, "GET", "", "", "", "", 200))
	request, err := http.NewRequestWithContext(ctx, "GET", api.URL+taskDetailPagePath+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+grant.Token)
	request.Header.Set("Last-Event-ID", original.Cursor)
	resp, err := api.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := resp.Body.Close(); e != nil {
			t.Error(e)
		}
	}()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected SSE handshake: %d", resp.StatusCode)
	}
	if err = store.RevokeTaskDetailGrant(ctx, issue.Destination.WorkspaceID, grant.Grant.ID); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Contains(data, []byte("event: stream.error")) || !bytes.Contains(data, []byte("task_detail_link_unavailable")) || bytes.Contains(data, []byte("event: task.updated")) {
		t.Fatalf("SSE repeated applied state or streamed revoked contents: %s %v", data, err)
	}
}

func TestTaskDetailHandoffProgressUsesCurrentAssignment(t *testing.T) {
	f := newAmendmentConflictFixture(t)
	ctx := t.Context()
	current := func() manager.TaskDetail {
		t.Helper()
		d, e := f.manager.GetTask(ctx, f.workspace, f.actor, f.task)
		if e != nil || d.Progress == nil || len(d.Progress.Handoffs) != 1 {
			t.Fatalf("task handoff summary: %+v %v", d.Progress, e)
		}
		if d.Progress.Phase != "waiting_handoff" {
			t.Fatalf("root not waiting: %+v", d.Progress)
		}
		return d
	}
	if got := current().Progress.Handoffs[0]; got.Phase != "queued" || got.Assignee != "Checklist reviewer" {
		t.Fatalf("queued assignee: %+v", got)
	}
	var session, input string
	f.check(f.db.QueryRow(`SELECT s.id,s.initial_input_id FROM manager_work_items w JOIN manager_work_assignments a ON a.id=w.current_assignment_id JOIN manager_sessions s ON s.id=a.session_id WHERE w.id=$1`, f.item).Scan(&session, &input))
	assignment := manager.NewID()
	turn := reviewStartTurn(t, f.db, f.workspace, session, input, assignment, 1)
	if got := current().Progress.Handoffs[0].Phase; got != "running" {
		t.Fatalf("child running: %s", got)
	}
	reviewFinishTurn(t, f.db, session, input, turn)
	reviewExec(t, f.db, `UPDATE manager_turns SET status='failed' WHERE id=$1`, turn)
	if got := current().Progress.Handoffs[0].Phase; got != "failed" {
		t.Fatalf("child failed independently of root: %s", got)
	}
	reviewExec(t, f.db, `UPDATE manager_work_items SET status='awaiting_acceptance' WHERE id=$1`, f.item)
	if got := current().Progress.Handoffs[0].Phase; got != "waiting_acceptance" {
		t.Fatalf("waiting acceptance: %s", got)
	}
	reviewExec(t, f.db, `UPDATE manager_work_items SET status='failed' WHERE id=$1`, f.item)
	reviewExec(t, f.db, `UPDATE manager_sessions SET required_actions='[]' WHERE task_id=$1`, f.task)
	d, err := f.manager.GetTask(ctx, f.workspace, f.actor, f.task)
	if err != nil || d.Progress == nil || d.Progress.Phase != "handoff_failed" {
		t.Fatalf("an ended handoff-originating turn is not task completion: %+v %v", d.Progress, err)
	}
}

func TestWorkSubmissionShowsOnlyCommittedArtifactVersions(t *testing.T) {
	f := newAmendmentConflictFixture(t)
	var session, input, task string
	f.check(f.db.QueryRow(`SELECT s.id,s.initial_input_id,s.task_id FROM manager_work_assignments a JOIN manager_sessions s ON s.id=a.session_id WHERE a.work_item_id=$1`, f.item).Scan(&session, &input, &task))
	turn := reviewStartTurn(t, f.db, f.workspace, session, input, manager.NewID(), 1)
	reviewFinishTurn(t, f.db, session, input, turn)
	reviewExec(t, f.db, `UPDATE manager_turns SET status='interrupted',error='{"code":"awaiting_work_commit"}' WHERE id=$1`, turn)
	boundary, file, published := manager.NewID(), manager.NewID(), manager.NewID()
	reviewExec(t, f.db, `INSERT INTO manager_work_boundaries(id,session_id,turn_id,input_id,stop_revision,operation_id,kind,work_item_id,payload,state,fenced_at)
 VALUES($1,$2,$3,$4,0,'submit-image','submit',$5,'{}','safe',now())`, boundary, session, turn, input, f.item)
	reviewExec(t, f.db, `INSERT INTO manager_work_files(id,boundary_id,name,digest,size_bytes) VALUES($1,$2,'poster.png','submitted',42)`, file, boundary)
	reviewExec(t, f.db, `INSERT INTO manager_work_file_selections(boundary_id,file_id) VALUES($1,$2)`, boundary, file)
	reviewExec(t, f.db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes) VALUES
 ($2,$1,'poster.png','poster.png','image/png','submitted',42),
 (gen_random_uuid(),$1,'poster.png','poster.png','image/png','draft',42),
 (gen_random_uuid(),$1,'private.png','private.png','image/png','submitted',42)`, turn, published)
	check := func(want int) {
		t.Helper()
		detail, err := f.manager.GetTask(t.Context(), f.workspace, f.actor, task)
		if err != nil || len(detail.Images) != want || want == 1 && detail.Images[0].ID != published {
			t.Fatal(detail.Images, err)
		}
	}
	check(0)
	reviewExec(t, f.db, "UPDATE manager_work_boundaries SET state='committed' WHERE id=$1", boundary)
	check(1)
	reviewExec(t, f.db, "UPDATE manager_turns SET has_unknown_tool_results=true WHERE id=$1", turn)
	check(0)
}

func TestTaskDetailConfirmationProgressKeepsResumeSeparate(t *testing.T) {
	db, m, mux, w, actor, employee, cookie := managerFixture(t)
	ctx := t.Context()
	c, e := m.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: employee})
	if e != nil {
		t.Fatal(e)
	}
	session, _, e := m.CreateSession(ctx, w, actor, "confirm-progress", manager.CreateSession{ConversationID: c.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "等待确认的测试"}})
	if e != nil {
		t.Fatal(e)
	}
	taskPath := "/api/v1/workspaces/" + w + "/tasks/" + *session.TaskID
	assert := func(phase string, canStop bool) {
		t.Helper()
		r := httptest.NewRequest("GET", taskPath, nil)
		r.Header.Set("Cookie", cookie)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, r)
		var d manager.TaskDetail
		if e := json.Unmarshal(res.Body.Bytes(), &d); e != nil || res.Code != 200 || d.Progress == nil || d.Progress.Phase != phase || d.Actions["stop"].Allowed != canStop {
			t.Fatalf("want %s/stop %t got %d %s (%v)", phase, canStop, res.Code, res.Body, e)
		}
		if !canStop && (d.Progress.Confirmation == nil || d.Progress.Confirmation.Question != "请确认范围") {
			t.Fatalf("missing confirmation summary: %+v", d.Progress.Confirmation)
		}
	}
	assert("queued", true)
	turn := reviewStartTurn(t, db, w, session.ID, *session.InitialInputID, manager.NewID(), 1)
	reviewFinishTurn(t, db, session.ID, *session.InitialInputID, turn)
	id := manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,authority_input_id,recipient,rules_version,question,stop_revision,fenced_at,state)
 VALUES($1,$2,$3,$4,$5,$6,$6,'{}',1,'{"question":"请确认范围"}',0,now(),'pending')`, id, w, employee, session.ID, turn, *session.InitialInputID)
	reviewExec(t, db, `UPDATE manager_sessions SET required_actions=jsonb_build_array(jsonb_build_object('type','human_confirmation','request_id',$2::text)) WHERE id=$1`, session.ID, id)
	assert("waiting_confirmation", false)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='approved',answer='{"decision":"approve"}',answered_at=now(),resume_input_id=$2 WHERE id=$1`, id, *session.InitialInputID)
	assert("waiting_resume", false)
}

func TestTaskDetailResultDeliveryDoesNotChangeExecutionOutcome(t *testing.T) {
	db, store, w, actor, _, _, input := triggerNoticeFixture(t)
	ctx := t.Context()
	endNoticeExecution(t, db, input)
	if err := store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	var task string
	if err := db.QueryRow(`SELECT task_id FROM manager_trigger_notifications WHERE source_input_id=$1`, input).Scan(&task); err != nil {
		t.Fatal(err)
	}
	assert := func(want string) {
		t.Helper()
		d, e := store.Task(ctx, w, actor, task)
		if e != nil || d.Progress == nil || d.Progress.Phase != "completed" || len(d.Progress.Delivery) != 1 || d.Progress.Delivery[0].Channel != "web" || d.Progress.Delivery[0].Phase != want {
			t.Fatalf("completed, delivery=%s: %+v %v", want, d.Progress, e)
		}
	}
	// The Web materializer may have already confirmed delivery; use
	// deterministic ledger states to exercise this read-only projection.
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='unknown' WHERE source_input_id=$1`, input)
	assert("unconfirmed")
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='failed' WHERE source_input_id=$1`, input)
	assert("failed")
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='sent',confirmed_at=now() WHERE source_input_id=$1`, input)
	assert("confirmed")
}

func TestTaskDetailExecutionSSELiveResumeAndScope(t *testing.T) {
	db, store, issue := taskDetailGrantFixture(t, "group")
	grant, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE manager_task_detail_grants SET full_execution=false WHERE id=$1`, legacy.Grant.ID)
	m := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: m}).Routes(mux)
	taskDetailPageClient{mux, legacy.Token}.request(t, "GET", "/execution", "", "", "", 404)
	client := taskDetailPageClient{mux, grant.Token}
	readHistory := func(path string) manager.EventPage {
		t.Helper()
		var history manager.EventPage
		if e := json.Unmarshal(client.request(t, "GET", path, "", "", "", 200).Body.Bytes(), &history); e != nil || history.ResumeCursor == nil {
			t.Fatalf("history: %+v %v", history, e)
		}
		return history
	}
	history := readHistory("/execution")
	taskDetailPageClient{mux, peer.Token}.request(t, "GET", "/execution/stream", "", "", *history.ResumeCursor, 400)
	api := httptest.NewServer(mux)
	defer api.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	open := func(last string) *http.Response {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, "GET", api.URL+taskDetailPagePath+"/execution/stream", nil)
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+grant.Token)
		r.Header.Set("Last-Event-ID", last)
		resp, e := api.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("handshake: %d", resp.StatusCode)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	find := func(reader *bufio.Reader, want, payload string) string {
		t.Helper()
		var id, event string
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case event == want && strings.HasPrefix(line, "data: "):
				if !strings.Contains(line, payload) {
					t.Fatalf("unexpected %s contents: %s", event, line)
				}
				return id
			}
		}
	}
	appendEvent := func(event, status, tag string) {
		t.Helper()
		reviewExec(t, db, `WITH s AS (UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING *)
 INSERT INTO manager_session_events(workspace_id,session_id,seq,event)
 SELECT workspace_id,id,last_event_seq,jsonb_build_object('session_id',id,'seq',last_event_seq::text,'type',$2::text,'status',$3::text,'tool_use_id','test-tool','output',$4::text) FROM s`, grant.Grant.SessionID, event, status, tag)
	}
	first := open(*history.ResumeCursor)
	appendEvent("agent.tool_use", "running", "STEP ONE RUNNING")
	id := find(bufio.NewReader(first.Body), "agent.tool_use", "STEP ONE RUNNING")
	if id == "" {
		t.Fatal("missing reconnect cursor")
	}
	if err = first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	// Result is written only after the client has observed the running event.
	appendEvent("agent.tool_result", "completed", "STEP ONE COMPLETE")
	second := open(id)
	defer func() { _ = second.Body.Close() }()
	reader := bufio.NewReader(second.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "id: ") || strings.TrimSpace(line) == "id: "+id {
		t.Fatalf("stream replayed the last seen event: %q %v", line, err)
	}
	find(reader, "agent.tool_result", "STEP ONE COMPLETE")
	appendEvent("agent.tool_use", "running", "STEP TWO RUNNING")
	find(reader, "agent.tool_use", "STEP TWO RUNNING")
	if err = store.RevokeTaskDetailGrant(ctx, grant.Grant.WorkspaceID, grant.Grant.ID); err != nil {
		t.Fatal(err)
	}
	find(reader, "stream.error", "task_detail_link_unavailable")
	client.request(t, "GET", "/execution", "", "", "", 404)
}
