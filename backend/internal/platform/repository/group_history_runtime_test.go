package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	grouphistory "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/grouphistory"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Provider responses are synthetic; real PostgreSQL authorizes every read.
func sharingTask(t *testing.T, db *sql.DB, b service.ChannelBinding, target service.SelectionRef) (manager.DelegationIdentity, string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.ExecContext(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	principal, conv, task, session, input, turn := service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID(), service.NewID()
	// A different synthetic caller per task avoids source-identity assumptions.
	exec(`INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id,identity_epoch)VALUES($1,$2,$3,'feishu',$4,$5,$6)`, principal, b.WorkspaceID, b.ID, b.TenantID, "ou_"+principal, b.IdentityEpoch)
	exec(`INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id,channel_identity_epoch)VALUES($1,$2,$3,$4,$5,$6,'group',$7,$8) ON CONFLICT(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id,channel_thread_id) WHERE channel_binding_id IS NOT NULL DO NOTHING`, conv, b.WorkspaceID, b.AgentID, principal, b.ID, target.ExternalID, b.TenantID, b.IdentityEpoch)
	if e := db.QueryRow(`SELECT id FROM manager_conversations WHERE channel_binding_id=$1 AND external_chat_id=$2`, b.ID, target.ExternalID).Scan(&conv); e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO manager_tasks(id,workspace_id,conversation_id,agent_id)VALUES($1,$2,$3,$4)`, task, b.WorkspaceID, conv, b.AgentID)
	exec(`INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,task_id,channel_created_by,mode,configuration)VALUES($1,$2,$3,$4,$5,$6,'task','{}')`, session, b.WorkspaceID, b.AgentID, conv, task, principal)
	exec(`INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text) SELECT $1,$2,$3,$4,$5,admission_epoch,1,'shared read' FROM platform_agents WHERE id=$6`, input, b.WorkspaceID, session, principal, b.Generation, b.AgentID)
	id := manager.DelegationIdentity{SessionID: session, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	store := &managerrepo.Store{DB: db}
	if e := store.RegisterEngine(ctx, manager.Engine{ID: id.EngineID, HostID: "shared-" + session, Endpoint: "http://127.0.0.1:1", Capacity: 10}); e != nil {
		t.Fatal(e)
	}
	exec(`UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, session, id.EngineID, id.AssignmentID)
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, b.WorkspaceID, session, input, id.AssignmentID)
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	return id, input
}
func TestSharedHistoryRuntimeLineageAndFrozenGrants(t *testing.T) {
	api, b, actor, refs := sharingFixture(t)
	db := api.Store.(*GroupSharingStore).DB
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	policy := func(source, target service.SelectionRef) service.GroupSharingPolicy {
		t.Helper()
		p, e := api.Save(ctx, b.WorkspaceID, actor, b.AgentID, "", service.GroupSharingPut{Name: source.ExternalID + " to " + target.ExternalID, Mode: "directed", Enabled: true, Sources: []service.SelectionRef{source}, Targets: []service.SelectionRef{target}})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	ab := policy(refs[0], refs[1])
	policy(refs[1], refs[2])
	bRun, bInput := sharingTask(t, db, b, refs[1])
	cRun, _ := sharingTask(t, db, b, refs[2])
	contexts := func(id manager.DelegationIdentity) []manager.SharedHistoryContext {
		t.Helper()
		v, e := store.ListSharedHistoryContexts(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	bContexts, cContexts := contexts(bRun), contexts(cRun)
	if len(bContexts) != 2 || len(cContexts) != 2 {
		t.Fatal("unexpected direct grants", bContexts, cContexts)
	}
	request := func(id manager.DelegationIdentity, ref string) (manager.SharedHistoryResult, error) {
		scope, e := store.SharedHistoryGrant(ctx, id, ref)
		if e != nil {
			return manager.SharedHistoryResult{}, e
		}
		end := time.Now().Add(-time.Second).UnixMilli()
		return store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 3600000, EndMS: end, Limit: 10})
	}
	qa, e := request(bRun, bContexts[0].Ref)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(manager.SharedHistoryPage{Items: []manager.SharedHistoryItem{{ID: "one", Text: "A confidential summary", CreatedAtMS: qa.StartMS + 1, SenderType: "user", MessageType: "text"}}, Coverage: "partial"})
	if e != nil {
		t.Fatal(e)
	}
	reviewExec(t, db, `UPDATE manager_group_history_queries SET status='completed',snapshot=$2 WHERE id=$1`, qa.ID, raw)
	result, e := store.ReadSharedGroupHistory(ctx, qa.ID, bInput)
	if e != nil || len(result.Items) != 1 {
		t.Fatal("direct read failed", result, e)
	}
	var lineage, audit int
	if e = db.QueryRow(`SELECT count(*) FROM manager_group_sharing_lineage`).Scan(&lineage); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM manager_group_history_reads`).Scan(&audit); e != nil || lineage != 1 || audit != 1 {
		t.Fatal("lineage not persisted before read", lineage, audit, e)
	}
	if _, e = request(cRun, cContexts[0].Ref); e == nil {
		t.Fatal("A->B->C transitive disclosure")
	}
	policy(refs[0], refs[2])
	if len(contexts(cRun)) != 2 {
		t.Fatal("new policy expanded frozen grant")
	}
	if _, e = request(cRun, cContexts[0].Ref); e == nil {
		t.Fatal("new policy bypassed frozen grant")
	}
	newC, _ := sharingTask(t, db, b, refs[2])
	newContexts := contexts(newC)
	if len(newContexts) != 3 {
		t.Fatal("new input missing new direct authorization", newContexts)
	}
	for _, v := range newContexts {
		if _, e = request(newC, v.Ref); e != nil {
			t.Fatal("explicit direct authorization rejected", e)
		}
	}
	if _, e = store.SharedHistoryGrant(ctx, newC, bContexts[0].Ref); e == nil {
		t.Fatal("cross-input grant leaked")
	}
	if _, e = store.ReadSharedGroupHistory(ctx, qa.ID, service.NewID()); e == nil {
		t.Fatal("cross-input cached page leaked")
	}
	// Own group already contains inherited text: it is not a new cross-group disclosure.
	ownB := bContexts[len(bContexts)-1].Ref
	// Policy revocation denies even a previously cached completed page.
	if e = api.Delete(ctx, b.WorkspaceID, actor, b.AgentID, ab.ID, ab.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = request(bRun, ownB); e != nil {
		t.Fatal("own history blocked by revoked inbound grant", e)
	}
	if _, e = store.ReadSharedGroupHistory(ctx, qa.ID, bInput); e == nil {
		t.Fatal("revoked cached page leaked")
	}
	secret := strings.Repeat("g", 40)
	token, e := manager.SignGroupHistory(secret, newC)
	if e != nil {
		t.Fatal(e)
	}
	runtime := manager.GroupHistoryRuntime{Store: store, Secret: secret}
	if _, e = runtime.Authorize(ctx, token); e != nil {
		t.Fatal(e)
	}
	handler, e := grouphistory.Handler(&runtime, nil)
	if e != nil {
		t.Fatal(e)
	}
	call := func(body, cookie, credential string) (int, string) {
		r := httptest.NewRequest("POST", "http://localhost/mcp/group-history", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"group_history_list_contexts","arguments":{}}}`
	if code, text := call(body, "", token); code != 200 || strings.Contains(text, `"isError":true`) {
		t.Fatal("MCP authorized list failed", code, text)
	}
	if code, _ := call(body, "session=x", token); code != 401 {
		t.Fatal("cookie credential allowed", code)
	}
	badBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"group_history_list_contexts","arguments":{"workspace_id":"forged"}}}`
	if code, text := call(badBody, "", token); code != 200 || !strings.Contains(text, `"isError":true`) {
		t.Fatal("MCP identity injection allowed", code, text)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, newC.SessionID)
	if _, e = runtime.Authorize(ctx, token); e == nil {
		t.Fatal("stopped run reused token")
	}
}

func TestSharedHistoryWebTextPaginationAndMemberRevocation(t *testing.T) {
	db, m, store, w, actor, agent, group := webMessageFixture(t)
	ctx := context.Background()
	for i, text := range []string{"web source one", "web source two"} {
		if _, _, e := m.PostWebGroupMessage(ctx, w, actor, group, "history-"+string(rune('a'+i)), manager.WebGroupPost{Text: text}); e != nil {
			t.Fatal(e)
		}
	}
	// Use the same historical time reference for the messages and the query range.
	messageTime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	reviewExec(t, db, `UPDATE manager_web_group_messages SET created_at=$2 WHERE group_id=$1`, group, messageTime)
	source := service.SelectionRef{Kind: "group", Provider: "web", WorkspaceID: w, AgentID: agent, ID: group, Namespace: "web_group_id", ExternalID: group, GroupEpoch: 1, AgentEpoch: 1}
	target := service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: w, AgentID: agent, ExternalID: actor}
	sharing := &service.GroupSharingService{Store: &GroupSharingStore{DB: db}}
	if _, e := sharing.Save(ctx, w, actor, agent, "", service.GroupSharingPut{Name: "Web source to person", Mode: "directed", Enabled: true, Sources: []service.SelectionRef{source}, Targets: []service.SelectionRef{target}}); e != nil {
		t.Fatal(e)
	}
	conv, e := m.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if e != nil {
		t.Fatal(e)
	}
	task, _, e := m.CreateSession(ctx, w, actor, "shared-web-task", manager.CreateSession{ConversationID: conv.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "read shared group"}})
	if e != nil {
		t.Fatal(e)
	}
	identity := manager.DelegationIdentity{SessionID: task.ID, EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
	if e = store.RegisterEngine(ctx, manager.Engine{ID: identity.EngineID, HostID: "shared-web", Endpoint: "http://127.0.0.1:1", Capacity: 10}); e != nil {
		t.Fatal(e)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, task.ID, identity.EngineID, identity.AssignmentID)
	reviewStartTurn(t, db, w, task.ID, *task.InitialInputID, identity.AssignmentID, 1)
	list, e := store.ListSharedHistoryContexts(ctx, identity)
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	scope, e := store.SharedHistoryGrant(ctx, identity, list[0].Ref)
	if e != nil {
		t.Fatal(e)
	}
	end := messageTime.Add(time.Minute).UnixMilli()
	q, e := store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 3600000, EndMS: end, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	page, e := store.ReadSharedGroupHistory(ctx, q.ID, *task.InitialInputID)
	if e != nil || page.Status != "completed" || len(page.Items) != 1 || !page.HasMore {
		t.Fatal(page, e)
	}
	next, e := store.ContinueSharedGroupHistory(ctx, q.ID, *task.InitialInputID)
	if e != nil {
		t.Fatal(e)
	}
	same, e := store.ContinueSharedGroupHistory(ctx, q.ID, *task.InitialInputID)
	if e != nil || next != same {
		t.Fatal("continuation retry not stable", next, same, e)
	}
	last, e := store.ReadSharedGroupHistory(ctx, next, *task.InitialInputID)
	if e != nil || len(last.Items) != 1 || last.Items[0].ID == page.Items[0].ID || last.HasMore {
		t.Fatal(last, e)
	}
	reviewExec(t, db, `UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND agent_id=$2`, group, agent)
	if _, e = store.ReadSharedGroupHistory(ctx, q.ID, *task.InitialInputID); e == nil {
		t.Fatal("cached page survived source employee removal")
	}
}

func TestSharedHistorySelfGrantWithoutPolicy(t *testing.T) {
	api, b, _, refs := sharingFixture(t)
	db := api.Store.(*GroupSharingStore).DB
	reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, b.ID)
	run, input := sharingTask(t, db, b, refs[0])
	store := &managerrepo.Store{DB: db}
	ctx := context.Background()
	list, err := store.ListSharedHistoryContexts(ctx, run)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	scope, err := store.SharedHistoryGrant(ctx, run, list[0].Ref)
	if err != nil || scope.PolicyID != "" || scope.EdgeOrdinal != -1 {
		t.Fatal(scope, err)
	}
	end := time.Now().Add(-time.Second).UnixMilli()
	q, err := store.RequestSharedGroupHistory(ctx, scope, manager.SharedHistoryRequest{StartMS: end - 1000, EndMS: end, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE manager_group_history_queries SET status='completed',snapshot='{"items":[]}' WHERE id=$1`, q.ID)
	if _, err = store.ReadSharedGroupHistory(ctx, q.ID, input); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET enabled=false WHERE id=$1`, b.ID)
	if _, err = store.ReadSharedGroupHistory(ctx, q.ID, input); err == nil {
		t.Fatal("revoked own-group still readable")
	}
}
