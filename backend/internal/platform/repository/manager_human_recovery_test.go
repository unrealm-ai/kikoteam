package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestHumanRecoveryReassignIsVersionedAndAudited(t *testing.T) {
	db, sessions, _, workspace, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := sessions.Store.(*managerrepo.Store)
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "recover-member@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, member)
	rules, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: 1, Recipients: []manager.HumanRecipient{
		{Scope: "original", Ref: service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: owner}},
		{Scope: "backup", Ref: service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: member}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := sessions.ResolveWebConversation(ctx, workspace, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := sessions.CreateSession(ctx, workspace, owner, "admin-recovery", manager.CreateSession{
		ConversationID: conversation.ID, Mode: "task", Input: &struct {
			Text string `json:"text"`
		}{Text: "recovery fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var input string
	if err = db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND seq=1`, task.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	turn, request := service.NewID(), service.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at) VALUES($1,$2,$3,$4,1,$5,1,'completed',now())`, turn, workspace, task.ID, input, service.NewID())
	raw, err := json.Marshal(rules.Recipients[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,authority_input_id,recipient,rules_version,question,stop_revision,fenced_at,state)
 VALUES($1,$2,$3,$4,$5,$6,$6,$7,1,jsonb_build_object('recipient_id',$8::text,'question','fixture','background','private context','answer_type','decision'),0,now(),'pending')`,
		request, workspace, agent, task.ID, turn, input, raw, rules.Recipients[0].ID)
	if _, err = store.HumanRecoveryList(ctx, workspace, member, agent); err == nil {
		t.Fatal("ordinary member can read recovery metadata")
	}
	listing, err := store.HumanRecoveryList(ctx, workspace, owner, agent)
	if err != nil || len(listing.Items) != 1 || !listing.Items[0].CanReassign || listing.Items[0].CanRetry {
		t.Fatal(listing, err)
	}
	action := manager.HumanRecoveryAction{Version: 1, Action: "reassign", RecipientID: rules.Recipients[1].ID, Reason: "覆盖当班岗位"}
	if _, err = store.RecoverHumanRequest(ctx, workspace, member, agent, request, action); err == nil {
		t.Fatal("ordinary member can reassign")
	}
	out, err := store.RecoverHumanRequest(ctx, workspace, owner, agent, request, action)
	if err != nil || out.Version != 2 || out.Status != "web_inbox" {
		t.Fatal(out, err)
	}
	_, err = store.RecoverHumanRequest(ctx, workspace, owner, agent, request, action)
	assertChannelError(t, err, "human_request_changed")
	var audit int
	if err = db.QueryRow(`SELECT count(*) FROM manager_human_recovery_audit WHERE request_id=$1 AND actor_id=$2 AND action='reassign' AND previous_version=1 AND next_version=2`, request, owner).Scan(&audit); err != nil || audit != 1 {
		t.Fatal(audit, err)
	}
	inbox, err := store.HumanInbox(ctx, workspace, member)
	if err != nil || len(inbox) != 1 || inbox[0].ID != request || inbox[0].Version != 2 {
		t.Fatal(inbox, err)
	}
	inbox, err = store.HumanInbox(ctx, workspace, owner)
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
	for _, check := range []struct {
		actor               string
		offset, rows, total int
	}{
		{member, 0, 1, 1},
		{member, 100, 0, 1},
		{owner, 0, 0, 0},
	} {
		items, total, err := store.HumanInboxPage(ctx, workspace, check.actor, check.offset, request)
		if err != nil || len(items) != check.rows || total != check.total {
			t.Fatalf("recipient %s offset %d: items %v, total %d, err %v", check.actor, check.offset, items, total, err)
		}
	}
	reader := service.NewID()
	insertWorkspaceAccount(t, db, reader, "workspace-observer@example.test")
	reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, reader)
	for _, actor := range []string{reader, owner, member} {
		count, err := store.HumanPendingCount(ctx, workspace, actor)
		if err != nil || count != 1 {
			t.Fatal(actor, count, err)
		}
		for _, id := range []string{"", request} {
			page, err := store.HumanHistory(ctx, workspace, actor, "pending", id, 0)
			if err != nil || page.Pending != count || page.Total != 1 || page.Resolved != 0 || len(page.Items) != 1 {
				t.Fatal(page, err)
			}
			item := page.Items[0]
			if item.ID != request {
				t.Fatal(item)
			}
			if actor == reader {
				assertHumanHistorySummary(t, item)
			} else if !item.DetailVisible || item.RecipientName == nil || *item.RecipientName != "Tester" || item.Question == nil || (item.Question.Question != "fixture" || item.Question.Background != "private context") {
				t.Fatal(item)
			}
		}
	}
	// The owner's source Session is readable, but the assignee cannot read that
	// private Session; the two detail grants are independent.
	if _, err := store.Session(ctx, workspace, owner, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(ctx, workspace, member, task.ID); err == nil {
		t.Fatal("assignee unexpectedly has access to the source Session")
	}
	for _, actor := range []string{reader, owner} {
		if _, err := store.AnswerHumanRequest(ctx, workspace, actor, request, manager.HumanAnswer{Version: 2, Decision: "approve"}); err == nil {
			t.Fatal("unassigned reader could answer for someone else")
		}
	}
	inbox, err = store.HumanInbox(ctx, workspace, reader)
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
	other, err := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, reader, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	count, err := store.HumanPendingCount(ctx, other.ID, reader)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	page, err := store.HumanHistory(ctx, other.ID, reader, "all", request, 0)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	_, err = store.HumanPendingCount(ctx, other.ID, member)
	assertChannelError(t, err, "workspace_forbidden")
	_, err = store.HumanHistory(ctx, other.ID, member, "all", request, 0)
	assertChannelError(t, err, "workspace_forbidden")
	reviewExec(t, db, `UPDATE manager_human_requests SET state='rejected',answered_at=now(),answer='{"version":"2","decision":"reject","text":"金额待复核"}' WHERE id=$1`, request)
	reviewExec(t, db, `INSERT INTO manager_human_clarifications(request_id,question_version,body_digest,body) VALUES($1,2,$2,'补充保密细节')`, request, strings.Repeat("a", 64))
	for _, actor := range []string{reader, owner, member} {
		page, err = store.HumanHistory(ctx, workspace, actor, "resolved", request, 0)
		if err != nil || page.Pending != 0 || page.Total != 1 || len(page.Items) != 1 || page.Items[0].AnsweredAt == nil {
			t.Fatal(page, err)
		}
		item := page.Items[0]
		if actor == reader {
			assertHumanHistorySummary(t, item)
		} else if !item.DetailVisible || item.Answer == nil || item.Answer.Text != "金额待复核" || item.Clarification != "补充保密细节" {
			t.Fatal(item)
		}
	}
	page, err = store.HumanHistory(ctx, workspace, reader, "resolved", request, 100)
	if err != nil || page.Total != 1 || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	count, err = store.HumanPendingCount(ctx, workspace, reader)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	reviewExec(t, db, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, workspace, member)
	_, err = store.HumanPendingCount(ctx, workspace, member)
	assertChannelError(t, err, "workspace_forbidden")
	_, err = store.HumanHistory(ctx, workspace, member, "resolved", request, 0)
	assertChannelError(t, err, "workspace_forbidden")
	page, err = store.HumanHistory(ctx, workspace, owner, "resolved", request, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].RecipientName == nil || *page.Items[0].RecipientName != "Tester" {
		t.Fatalf("history lost the recorded recipient after deactivation: %+v, %v", page, err)
	}
	// Identical strings in a channel identity and a Web account are not an identity link.
	reviewExec(t, db, `UPDATE manager_human_requests SET recipient=recipient||jsonb_build_object('provider','feishu','external_id',$2::text) WHERE id=$1`, request, reader)
	page, err = store.HumanHistory(ctx, workspace, reader, "resolved", request, 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	assertHumanHistorySummary(t, page.Items[0])

}

func assertHumanHistorySummary(t *testing.T, item manager.HumanHistoryItem) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if err = json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if item.DetailVisible || string(row["detail_visible"]) != "false" {
		t.Fatal(string(raw))
	}
	for key := range row {
		switch key {
		case "id", "version", "state", "created_at", "answered_at", "detail_visible":
		default:
			t.Fatalf("summary leaked %s: %s", key, raw)
		}
	}
}
