package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestHumanChatListPagesAndReferences(t *testing.T) {
	db, sessions, _, workspace, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := sessions.Store.(*managerrepo.Store)
	ref := service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: owner}
	rules, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{
		Recipients: []manager.HumanRecipient{{Ref: ref, IsDefault: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	recipientJSON, err := json.Marshal(rules.Recipients[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := sessions.CreateConversation(ctx, workspace, owner, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 2)
	for i := range 2 {
		created, _, err := sessions.CreateSession(ctx, workspace, owner, fmt.Sprintf("list-task-%d", i), manager.CreateSession{
			Mode: "task", ConversationID: conversation.ID, Input: &struct {
				Text string `json:"text"`
			}{Text: "task awaiting review"},
		})
		if err != nil {
			t.Fatal(err)
		}
		request, turn := service.NewID(), service.NewID()
		reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 VALUES($1,$2,$3,$4,1,$5,1,'completed',now())`, turn, workspace, created.ID, *created.InitialInputID, service.NewID())
		reviewExec(t, db, `INSERT INTO manager_human_requests(id,workspace_id,agent_id,session_id,turn_id,input_id,authority_input_id,recipient,rules_version,question,stop_revision,fenced_at,state,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$6,$7,1,jsonb_build_object('recipient_id',$8::text,'question','请确认','answer_type','decision'),0,now(),'pending',now()+$9*interval '1 second')`,
			request, workspace, agent, created.ID, turn, *created.InitialInputID, recipientJSON, rules.Recipients[0].ID, i)
		ids = append(ids, request)
	}
	direct, err := sessions.ResolveWebConversation(ctx, workspace, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	code := manager.HumanShortCode(ids[1], 1)
	chat, _, err := sessions.CreateSession(ctx, workspace, owner, "human-pagination-input", manager.CreateSession{
		ConversationID: direct.ID, Mode: "chat", Input: &struct {
			Text string `json:"text"`
		}{Text: "请查看 " + code},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, assignment, turn := service.NewID(), service.NewID(), service.NewID()
	if err := store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "human-list", Endpoint: "http://127.0.0.1:1", Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)
 VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, workspace, chat.ID, *chat.InitialInputID, assignment)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, *chat.InitialInputID, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, chat.ID, turn, engine, assignment)
	scope := manager.HumanRuntimeScope{InputID: *chat.InitialInputID,
		Identity: manager.DelegationIdentity{SessionID: chat.ID, EngineID: engine, AssignmentID: assignment, Generation: 1}}
	for _, tc := range []struct {
		page, total int
		locator     string
		item        string
		more        bool
	}{
		{page: 1, total: 2, item: ids[1], more: true},
		{page: 2, total: 2, item: ids[0]},
		{page: 3, total: 2},
		{page: 1, total: 1, locator: code, item: ids[1]},
		{page: 1, total: 1, locator: ids[1], item: ids[1]},
	} {
		t.Run(fmt.Sprintf("%d-%s", tc.page, tc.locator), func(t *testing.T) {
			size := 1
			value, err := store.HumanChatList(ctx, scope, manager.HumanListQuery{Page: &tc.page, PageSize: &size, RequestID: tc.locator})
			if err != nil {
				t.Fatal(err)
			}
			reply := value.(map[string]any)
			items, references := reply["items"].([]manager.HumanRequest), reply["referencedRequests"].([]manager.HumanRequest)
			if reply["total"] != tc.total || reply["totalPages"] != tc.total || reply["hasMore"] != tc.more || (tc.item == "" && len(items) != 0) ||
				(tc.item != "" && (len(items) != 1 || items[0].ID != tc.item)) || len(references) != 1 || references[0].ID != ids[1] {
				t.Fatalf("unexpected page or current-message references: %v", reply)
			}
		})
	}
	// Completed results remain visible even when the task stop revision has advanced.
	reviewExec(t, db, `UPDATE manager_human_requests SET state='approved',answer='{"decision":"information"}',answered_at=now()+interval '1 second' WHERE id=$1`, ids[0])
	reviewExec(t, db, `UPDATE manager_human_requests SET state='rejected',answer='{"decision":"reject"}',answered_at=now() WHERE id=$1`, ids[1])
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=1 WHERE id=(SELECT session_id FROM manager_human_requests WHERE id=$1)`, ids[1])
	for _, tc := range []struct {
		status, locator, wantID, wantDecision string
		page, total                           int
	}{
		{page: 1},
		{status: "pending", locator: code, page: 1},
		{locator: code, page: 1, total: 1, wantID: ids[1], wantDecision: "reject"},
		{locator: ids[0], page: 1, total: 1, wantID: ids[0], wantDecision: "information"},
		{locator: service.NewID(), page: 1},
		{status: "resolved", page: 1, total: 2, wantID: ids[1], wantDecision: "reject"},
		{status: "resolved", page: 2, total: 2, wantID: ids[0], wantDecision: "information"},
		{status: "resolved", page: 3, total: 2},
		{status: "resolved", locator: code, page: 1, total: 1, wantID: ids[1], wantDecision: "reject"},
	} {
		size := 1
		value, err := store.HumanChatList(ctx, scope, manager.HumanListQuery{Status: tc.status, Page: &tc.page, PageSize: &size, RequestID: tc.locator})
		if err != nil {
			t.Fatal(err)
		}
		reply := value.(map[string]any)
		items := reply["items"].([]manager.HumanRequest)
		if reply["total"] != tc.total || len(reply["referencedRequests"].([]manager.HumanRequest)) != 0 ||
			(tc.wantID == "" && len(items) != 0) || (tc.wantID != "" && (len(items) != 1 || items[0].ID != tc.wantID || items[0].RecordedDecision != tc.wantDecision)) {
			t.Fatalf("unexpected completed page: %+v, got %v", tc, reply)
		}
	}
	if _, err := store.HumanChatList(ctx, scope, manager.HumanListQuery{Status: "anything"}); err == nil {
		t.Fatal("accepted an unknown status")
	}
	// A resolved list must still enforce that the actor is the current configured recipient.
	reviewExec(t, db, `UPDATE platform_agent_human_recipients SET recipients='[]' WHERE agent_id=$1`, agent)
	for _, status := range []string{"", "resolved"} {
		value, err := store.HumanChatList(ctx, scope, manager.HumanListQuery{Status: status, RequestID: code})
		if err != nil || value.(map[string]any)["total"] != 0 {
			t.Fatalf("revoked resolution leaked for status %q: %v %v", status, value, err)
		}
	}

}
