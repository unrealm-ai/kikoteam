package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWebHumanNoticeRecordsOnePassiveInputForAssignee(t *testing.T) {
	db, sessions, _, workspace, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := sessions.Store.(*managerrepo.Store)
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "web-confirmation@example.test")
	reviewExec(t, db, "INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')", workspace, member)
	ref := service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: member}
	rules, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: 1, Recipients: []manager.HumanRecipient{{Ref: ref, IsDefault: true}}})
	if err != nil {
		t.Fatal(err)
	}
	initiator, err := sessions.ResolveWebConversation(ctx, workspace, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	createChat := func(actor, conversation string) string {
		t.Helper()
		chat, _, e := sessions.CreateSession(ctx, workspace, actor, "", manager.CreateSession{ConversationID: conversation, Mode: "chat"})
		if e != nil {
			t.Fatal(e)
		}
		return chat.ID
	}
	initiatorChat := createChat(owner, initiator.ID)
	create := manager.CreateSession{ConversationID: initiator.ID, Mode: "task"}
	create.Input = &struct {
		Text string `json:"text"`
	}{Text: "Run Web notice test"}
	task, _, err := sessions.CreateSession(ctx, workspace, owner, "web-notice-task", create)
	if err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: task.ID, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if err = store.RegisterEngine(ctx, manager.Engine{ID: identity.EngineID, HostID: "web-notice", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	var input string
	if err = db.QueryRow("SELECT id FROM manager_inputs WHERE session_id=$1 AND seq=1", task.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, workspace, task.ID, input, identity.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, task.ID, turn, identity.EngineID, identity.AssignmentID)
	created, err := store.HumanTaskTool(ctx, manager.HumanRuntimeScope{Identity: identity, InputID: input}, "request", "", &manager.HumanQuestion{RecipientID: rules.Recipients[0].ID, RulesVersion: rules.Version, Question: "是否同意演练上线？", AnswerType: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	request := created.(manager.HumanRequest)
	poll := func() {
		t.Helper()
		if items, e := store.ClaimHumanNotices(ctx, 10, false); e != nil || len(items) != 0 {
			t.Fatalf("Web should not leave an IM delivery: %v %v", items, e)
		}
	}
	assertNoWritebacks := func() {
		t.Helper()
		var count int
		if e := db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE workspace_id=$1 AND text LIKE '[系统消息] 已发布%'`, workspace).Scan(&count); e != nil || count != 0 {
			t.Fatalf("unexpected Web writeback: %d %v", count, e)
		}
	}
	poll()
	assertNoWritebacks()
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1`, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, request.ID)
	poll()
	assertNoWritebacks() // No assignee chat: do not create one or leak into the owner's chat.
	assignee, err := sessions.ResolveWebConversation(ctx, workspace, member, agent)
	if err != nil {
		t.Fatal(err)
	}
	assigneeChat := createChat(member, assignee.ID)
	poll()
	poll()
	code := manager.HumanShortCode(request.ID, request.Version)
	var total, quiet, other, turns int
	if err = db.QueryRow(`SELECT count(*),
 count(*) FILTER (WHERE session_id=$2 AND submitted_by=$3 AND source_kind='system' AND NOT trigger_agent_loop AND turn_id IS NULL AND text LIKE '%'||$4||'%'),
 count(*) FILTER (WHERE session_id IN ($5,$6)),
 (SELECT count(*) FROM manager_turns WHERE session_id=$2)
 FROM manager_inputs WHERE workspace_id=$1 AND text LIKE '[系统消息] 已发布%'`, workspace, assigneeChat, member, code, initiatorChat, task.ID).Scan(&total, &quiet, &other, &turns); err != nil || total != 1 || quiet != 1 || other != 0 || turns != 0 {
		t.Fatalf("unexpected Web notice writes: total=%d quiet=%d other=%d turns=%d %v", total, quiet, other, turns, err)
	}
	events, err := sessions.ListEvents(ctx, workspace, member, assigneeChat, "", "input.updated", "", 10, "backward")
	if err != nil || !strings.Contains(fmt.Sprint(events.Items), code) {
		t.Fatalf("assignee's chat did not get confirmation event: %+v %v", events, err)
	}
}
