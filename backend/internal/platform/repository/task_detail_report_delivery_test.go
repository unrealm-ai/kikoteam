package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	delegationmcp "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestOrdinaryTaskReportDetailLink(t *testing.T) {
	for _, outcome := range []string{"sent", "failed", "unknown", "unclaimed"} {
		t.Run(outcome, func(t *testing.T) {
			db, store, issue := taskDetailGrantFixture(t, "direct")
			store.TriggerSecret = []byte(strings.Repeat("test", 12))
			ctx := t.Context()
			scope := *issue.Source
			task, reportScope, turn := startDetailReport(t, db, store, issue)
			_, err := store.QueueTaskDetailCard(ctx, reportScope, manager.NewID(), "another task")
			assertChannelError(t, err, "task_detail_report_task_mismatch")

			const result = "已核对三项。详情见链接。"
			got := callReportDetailTool(t, store, reportScope, task, result)
			if got["status"] != "queued" || !strings.Contains(got["message"], "尚未确认送达") || got["task_id"] != task {
				t.Fatalf("report receipt: %+v", got)
			}
			_, err = store.QueueTaskDetailCard(ctx, reportScope, task, "repeat")
			assertChannelError(t, err, "task_detail_delivery_pending")
			old, err := store.ClaimChannelDeliveries(ctx, 20, true, true, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range old {
				if d.TaskDetailCard != nil {
					t.Fatal("old Hub accepted a description it cannot deliver")
				}
			}
			reviewFinishTurn(t, db, scope.Identity.SessionID, reportScope.InputID, turn)
			if outcome == "unclaimed" {
				reviewExec(t, db, `UPDATE manager_channel_deliveries SET created_at=now()-interval '2 minutes' WHERE kind LIKE 'task_detail:body:%'`)
			} else {
				finishReportDetailDelivery(t, store, outcome, result)
			}
			for range 2 {
				if err = store.MaterializeChannelReplies(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var replies, notes int
			var body, note string
			err = db.QueryRow(`SELECT count(*),COALESCE(max(text),'') FROM manager_channel_deliveries WHERE kind=$1`, "task_reply:"+turn).Scan(&replies, &body)
			if err != nil {
				t.Fatal(err)
			}
			err = db.QueryRow(`SELECT count(*),COALESCE(max(text),'') FROM manager_inputs WHERE session_id=$1 AND source_kind='system' AND NOT trigger_agent_loop AND text LIKE '%已发送任务%'`, scope.Identity.SessionID).Scan(&notes, &note)
			if err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "sent":
				if replies != 0 || notes != 1 || !strings.Contains(note, "的详情链接") || strings.Contains(note, result) || strings.Contains(note, "/tasks/") {
					t.Fatalf("sent report duplicated or leaked context: %d %d %q", replies, notes, note)
				}
			case "failed", "unclaimed":
				if replies != 1 || notes != 0 || body != result {
					t.Fatalf("ordinary text fallback missing: %d %d %q", replies, notes, body)
				}
			case "unknown":
				if replies != 0 || notes != 0 {
					t.Fatalf("unknown was resubmitted or announced: %d %d", replies, notes)
				}
			}
		})
	}
}

func startDetailReport(t *testing.T, db *sql.DB, store *managerrepo.Store, issue manager.TaskDetailIssue) (string, manager.DelegationScope, string) {
	t.Helper()
	source := *issue.Source
	var task, taskInput, chatTurn string
	if err := db.QueryRow(`SELECT s.task_id,i.id FROM manager_sessions s JOIN manager_inputs i ON i.session_id=s.id WHERE s.id=$1 ORDER BY i.seq LIMIT 1`, issue.SessionID).Scan(&task, &taskInput); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT active_turn_id FROM manager_sessions WHERE id=$1`, source.Identity.SessionID).Scan(&chatTurn); err != nil {
		t.Fatal(err)
	}
	reviewFinishTurn(t, db, source.Identity.SessionID, source.InputID, chatTurn)
	taskTurn := reviewStartTurn(t, db, source.WorkspaceID, issue.SessionID, taskInput, manager.NewID(), 1)
	reviewFinishTurn(t, db, issue.SessionID, taskInput, taskTurn)
	report, err := store.ReportTaskResult(t.Context(), manager.TaskResult{TurnID: taskTurn})
	if err != nil || report.TargetInputID == nil {
		t.Fatal(report, err)
	}
	turn := reviewStartTurn(t, db, source.WorkspaceID, source.Identity.SessionID, *report.TargetInputID, source.Identity.AssignmentID, 1)
	scope, err := store.DelegationSource(t.Context(), source.Identity)
	if err != nil || !scope.ReportOnly || scope.ReportTaskID != task || scope.Channel == nil {
		t.Fatalf("report authority: %+v %v", scope, err)
	}
	return task, scope, turn
}

func finishReportDetailDelivery(t *testing.T, store *managerrepo.Store, outcome, body string) {
	t.Helper()
	ctx := t.Context()
	items, err := store.ClaimChannelDeliveries(ctx, 20, true, true, true, true)
	if err != nil || len(items) != 1 || items[0].TaskDetailCard == nil || items[0].TaskDetailCard.Description != body {
		t.Fatalf("report card claim: %+v %v", items, err)
	}
	d, err := store.AuthorizeChannelDelivery(ctx, items[0].ID, items[0].LeaseToken)
	if err != nil || d.TaskDetailCard == nil || d.TaskDetailCard.Description != body || d.TaskDetailCard.URL == "" {
		t.Fatal("report delivery authorization", err)
	}
	r := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: outcome}
	switch outcome {
	case "sent":
		r.EvidenceType, r.EvidenceID, r.ProviderMessageID = "send_response", "fixture", "fixture"
		r.ProviderChatID, r.ProviderThreadID = d.ChatID, d.ThreadID
		r.Presentation = manager.TaskDetailLinkPresentation
	case "failed":
		r.EvidenceType, r.ErrorCode = "not_dispatched", "fixture_rejection"
	case "unknown":
		r.EvidenceType, r.ErrorCode = "send_unknown", "fixture_timeout"
	}
	if err = store.CompleteChannelDelivery(ctx, d.ID, r); err != nil {
		t.Fatal(err)
	}
}

func TestOrdinaryTaskReportDetailLinkOnlyDeduplicatesReports(t *testing.T) {
	db, sessions, store, workspace, _, _, binding := newTaskDetailGrantEnvironment(t)
	issue := makeTaskDetailGrantIssue(t, db, sessions, store, workspace, binding, "group")
	store.TriggerSecret = []byte(strings.Repeat("test", 12))
	ctx := t.Context()
	delegate := &manager.Delegation{Manager: sessions, Store: store}
	another, err := delegate.Call(ctx, *issue.Source, "create_session", manager.DelegationArgs{Instruction: "同会话的另一个任务"})
	if err != nil {
		t.Fatal(err)
	}
	otherTask := *another.(map[string]any)["task_id"].(*string)
	task := taskDetailReportTaskID(t, db, issue.SessionID)
	for range 2 {
		r, e := store.QueueTaskDetailCard(ctx, *issue.Source, task, "")
		if e != nil || r.Status != "queued" {
			t.Fatal(r, e)
		}
	}
	var pending int
	if e := db.QueryRow(`SELECT count(*) FROM manager_task_detail_deliveries`).Scan(&pending); e != nil || pending != 2 {
		t.Fatal("ordinary turn deduplicated", pending, e)
	}
	task, scope, turn := startDetailReport(t, db, store, issue)
	_, err = delegate.Call(ctx, scope, "send_task_detail_link", manager.DelegationArgs{TaskID: otherTask})
	assertChannelError(t, err, "task_detail_report_task_mismatch")
	_, err = store.QueueTaskDetailCard(ctx, scope, task, "result")
	assertChannelError(t, err, "task_detail_delivery_pending")
	reviewExec(t, db, `UPDATE manager_channel_deliveries SET status='sent' WHERE kind LIKE 'task_detail:%'`)
	_, err = store.QueueTaskDetailCard(ctx, scope, task, "result")
	assertChannelError(t, err, "task_detail_already_sent")
	reviewExec(t, db, `UPDATE manager_task_detail_grants SET issued_at=issued_at-interval '4 days',expires_at=expires_at-interval '4 days'`)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := store.QueueTaskDetailCard(ctx, scope, task, "result")
			results <- e
		}()
	}
	queued, guarded := 0, 0
	for range 2 {
		switch e := <-results; {
		case e == nil:
			queued++
		default:
			var failure *manager.Error
			if !errors.As(e, &failure) || failure.Code != "task_detail_delivery_pending" {
				t.Fatal("expired link blocked report replacement", e)
			}
			guarded++
		}
	}
	if queued != 1 || guarded != 1 {
		t.Fatalf("concurrent reports did not serialize: accepted=%d guarded=%d", queued, guarded)
	}

	// Engine normally disallows steering into a report. Recreate an anomalous
	// committed user Input to ensure Manager cannot confuse it with the root.
	reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,turn_id,seq,text,source_kind,context_state,delivery_mode,admission_epoch,channel_principal_id,channel_generation)
 SELECT $1,workspace_id,session_id,$2,seq+100,'synthetic steer','user','committed','steer',admission_epoch,channel_principal_id,channel_generation FROM manager_inputs WHERE id=$3`, manager.NewID(), turn, scope.InputID)
	steered, err := store.DelegationSource(ctx, scope.Identity)
	if err != nil || steered.ReportOnly || steered.ReportTaskID != task || steered.InputID == scope.InputID {
		t.Fatal("steered report fixture did not change the effective input", steered, err)
	}
	_, err = delegate.Call(ctx, steered, "send_task_detail_link", manager.DelegationArgs{TaskID: otherTask})
	assertChannelError(t, err, "task_detail_report_task_mismatch")
}

func taskDetailReportTaskID(t *testing.T, db *sql.DB, session string) string {
	t.Helper()
	var task string
	if err := db.QueryRow(`SELECT task_id FROM manager_sessions WHERE id=$1`, session).Scan(&task); err != nil {
		t.Fatal(err)
	}
	return task
}

func callReportDetailTool(t *testing.T, store *managerrepo.Store, scope manager.DelegationScope, task, body string) map[string]string {
	t.Helper()
	secret := strings.Repeat("s", 32)
	token, err := manager.SignDelegation(secret, scope.Identity)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := delegationmcp.Handler(&manager.Delegation{Store: store, Secret: secret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"name": "send_task_detail_link", "arguments": map[string]string{"task_id": task, "text": body}}
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation?catalog=stable&mode=report&task_detail=enabled", strings.NewReader(string(raw)))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	var reply struct {
		Result struct {
			Structured map[string]string `json:"structuredContent"`
		} `json:"result"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &reply); err != nil || out.Code != 200 || strings.Contains(out.Body.String(), "/tasks/") {
		t.Fatalf("MCP response invalid: %d %v", out.Code, err)
	}
	return reply.Result.Structured
}
