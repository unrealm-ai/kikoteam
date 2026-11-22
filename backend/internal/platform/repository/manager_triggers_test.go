package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func triggerFixture(t *testing.T) (*managerrepo.Store, *manager.Service, string, string, string, manager.Trigger, manager.TriggerCredential) {
	t.Helper()
	_, m, _, w, actor, agent, _ := managerFixture(t)
	st := m.Store.(*managerrepo.Store)
	st.TriggerSecret = []byte(strings.Repeat("x", 32))
	ctx := context.Background()
	b, e := st.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "webhook", Config: json.RawMessage(`{"adapter":"generic"}`), AllowedActions: []string{"create_task", "supplement_task", "cancel_task"}})
	if e != nil {
		t.Fatal(e)
	}
	cred, e := st.CreateTriggerCredential(ctx, w, actor, agent, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, e = st.SaveTrigger(ctx, w, actor, agent, b.ID, manager.TriggerWrite{Version: b.Version, Kind: b.Kind, Enabled: true, Config: b.Config, AllowedActions: b.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	return st, m, w, actor, agent, b, cred
}
func signedTrigger(t *testing.T, st *managerrepo.Store, b manager.Trigger, c manager.TriggerCredential, q manager.TriggerCommand) (manager.TriggerReceipt, error) {
	t.Helper()
	body, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	hash := sha256.Sum256(body)
	canonical := strings.Join([]string{"v1", "POST", "/api/triggers/webhook/" + b.ID, stamp, hex.EncodeToString(hash[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(c.Secret))
	_, _ = mac.Write([]byte(canonical))
	return st.ReceiveTriggerWebhook(context.Background(), b.ID, c.KeyID, stamp, hex.EncodeToString(mac.Sum(nil)), body, now)
}
func TestTriggerAtomicIdentityReplayAndTaskActions(t *testing.T) {
	st, m, w, actor, _, b, c := triggerFixture(t)
	ctx := context.Background()
	q := manager.TriggerCommand{Version: 1, EventID: "first", Action: "create_task", Description: "执行检查"}
	first, e := signedTrigger(t, st, b, c, q)
	if e != nil {
		t.Fatal(e)
	}
	again, e := signedTrigger(t, st, b, c, q)
	if e != nil || !again.Duplicate || again.TaskID != first.TaskID || again.TaskSpaceID != first.TaskSpaceID {
		t.Fatal(again, e)
	}
	q.Description = "other"
	if _, e = signedTrigger(t, st, b, c, q); e == nil {
		t.Fatal("conflicting event accepted")
	}
	var title string
	if e = st.DB.QueryRow(`SELECT title FROM manager_sessions WHERE id=$1`, first.SessionID).Scan(&title); e != nil || !strings.HasPrefix(title, b.Name+" · ") || !strings.HasSuffix(title, " UTC") {
		t.Fatal(title, e)
	}
	var user *string
	var text, kind, mode string
	var trigger, allowed bool
	if e = st.DB.QueryRow(`SELECT submitted_by::text,text,source_kind,delivery_mode,trigger_agent_loop,manager_input_actor_allowed(id) FROM manager_inputs WHERE id=$1`, first.InputID).Scan(&user, &text, &kind, &mode, &trigger, &allowed); e != nil {
		t.Fatal(e)
	}
	if user != nil || !strings.HasPrefix(text, "[系统消息]") || kind != "system" || mode != "queue" || !trigger || !allowed {
		t.Fatal(user, text, kind, mode, trigger, allowed)
	}
	if _, e = m.Store.Session(ctx, w, actor, first.SessionID); e != nil {
		t.Fatal("creator read", e)
	}
	if _, _, e = m.SubmitInput(ctx, w, actor, first.SessionID, "manual", "must not impersonate trigger"); e == nil {
		t.Fatal("manual trigger injection")
	}
	q = manager.TriggerCommand{Version: 1, EventID: "supplement", Action: "supplement_task", TaskID: first.TaskID, Description: "再检查"}
	supp, e := signedTrigger(t, st, b, c, q)
	if e != nil {
		t.Fatal(e)
	}
	if supp.TaskID != first.TaskID || supp.InputID == first.InputID {
		t.Fatal(supp)
	}
	q = manager.TriggerCommand{Version: 1, EventID: "cancel", Action: "cancel_task", TaskID: first.TaskID}
	if _, e = signedTrigger(t, st, b, c, q); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = st.DB.QueryRow(`SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND status='canceled'`, first.SessionID).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if e = st.RevokeTriggerCredential(ctx, w, actor, b.AgentID, b.ID, c.KeyID); e != nil {
		t.Fatal(e)
	}
	if _, e = signedTrigger(t, st, b, c, q); e == nil {
		t.Fatal("revoked credential")
	}
}
func TestTriggerConcurrentBusinessKeyAndNoKey(t *testing.T) {
	st, _, _, _, _, b, c := triggerFixture(t)
	key := "issue-7"
	var receipts [4]manager.TriggerReceipt
	var errs [4]error
	var wg sync.WaitGroup
	for i := range receipts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = signedTrigger(t, st, b, c, manager.TriggerCommand{Version: 1, EventID: "parallel-" + strconv.Itoa(i), Action: "create_task", Description: "check", BusinessKey: &key})
		}(i)
	}
	wg.Wait()
	for i, r := range receipts {
		if errs[i] != nil || r.TaskSpaceID != receipts[0].TaskSpaceID {
			t.Fatal(receipts, errs)
		}
	}
	x, e := signedTrigger(t, st, b, c, manager.TriggerCommand{Version: 1, EventID: "random-a", Action: "create_task", Description: "check"})
	if e != nil {
		t.Fatal(e)
	}
	y, e := signedTrigger(t, st, b, c, manager.TriggerCommand{Version: 1, EventID: "random-b", Action: "create_task", Description: "check"})
	if e != nil || x.TaskSpaceID == y.TaskSpaceID {
		t.Fatal(x, y, e)
	}
}

func TestTriggerGithubSignedIssueIdentity(t *testing.T) {
	st, _, w, actor, agent, _, _ := triggerFixture(t)
	ctx := context.Background()
	b, e := st.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "webhook", Name: "Issue", Config: json.RawMessage(`{"adapter":"github_issue","repositoryId":123,"installationId":44}`), AllowedActions: []string{"create_task"}})
	if e != nil {
		t.Fatal(e)
	}
	c, e := st.CreateTriggerCredential(ctx, w, actor, agent, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	b, e = st.SaveTrigger(ctx, w, actor, agent, b.ID, manager.TriggerWrite{Kind: b.Kind, Version: b.Version, Enabled: true, Name: b.Name, Config: b.Config, AllowedActions: b.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"action":"opened","repository":{"id":123},"installation":{"id":44},"issue":{"id":555,"number":7,"title":"bug","body":"check"}}`)
	send := func(raw []byte, delivery string) (manager.TriggerReceipt, bool, error) {
		mac := hmac.New(sha256.New, []byte(c.Secret))
		_, _ = mac.Write(raw)
		return st.ReceiveTriggerGithub(ctx, b.ID, "sha256="+hex.EncodeToString(mac.Sum(nil)), "issues", delivery, raw, time.Now())
	}
	first, ignored, e := send(body, manager.NewID())
	if e != nil || ignored {
		t.Fatal(first, ignored, e)
	}
	again, _, e := send(body, manager.NewID())
	if e != nil || !again.Duplicate || again.TaskID != first.TaskID {
		t.Fatal("untrusted delivery caused duplicate", again, e)
	}
	if _, _, e = send([]byte(strings.Replace(string(body), `"id":123`, `"id":456`, 1)), manager.NewID()); e == nil {
		t.Fatal("wrong repo accepted")
	}
	if _, ignored, e = send([]byte(strings.Replace(string(body), `"opened"`, `"closed"`, 1)), manager.NewID()); e != nil || !ignored {
		t.Fatal("non-open issue not ignored", ignored, e)
	}
	if _, _, e = send([]byte(strings.Replace(string(body), `"bug"`, `"other"`, 1)), manager.NewID()); e == nil {
		t.Fatal("changed signed event accepted")
	}
	if _, _, e = st.ReceiveTriggerGithub(ctx, b.ID, "sha256="+strings.Repeat("0", 64), "issues", manager.NewID(), body, time.Now()); e == nil {
		t.Fatal("bad signature accepted")
	}
}

func TestTriggerCrossWorkspaceKeyAndCrossBindingTaskDenied(t *testing.T) {
	st, _, w, actor, agent, b, c := triggerFixture(t)
	ctx := context.Background()
	key := "same-key"
	command := manager.TriggerCommand{Version: 1, EventID: "one", Action: "create_task", Description: "scope", BusinessKey: &key}
	first, e := signedTrigger(t, st, b, c, command)
	if e != nil {
		t.Fatal(e)
	}
	other, e := st.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "webhook", Config: json.RawMessage(`{"adapter":"generic"}`), AllowedActions: b.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	oc, e := st.CreateTriggerCredential(ctx, w, actor, agent, other.ID)
	if e != nil {
		t.Fatal(e)
	}
	other, e = st.SaveTrigger(ctx, w, actor, agent, other.ID, manager.TriggerWrite{Kind: other.Kind, Version: other.Version, Enabled: true, Config: other.Config, AllowedActions: other.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	second, e := signedTrigger(t, st, other, oc, command)
	if e != nil || second.TaskSpaceID != first.TaskSpaceID || second.TaskID == first.TaskID {
		t.Fatal(second, e)
	}
	for _, action := range []string{"supplement_task", "cancel_task"} {
		if _, e = signedTrigger(t, st, other, oc, manager.TriggerCommand{Version: 1, EventID: action, Action: action, TaskID: first.TaskID, Description: "forbidden"}); e == nil {
			t.Fatal("cross trigger", action)
		}
	}
	ws, e := (&platform.WorkspaceService{Store: NewWorkspaceStore(st.DB)}).Create(ctx, actor, "second", " ")
	if e != nil {
		t.Fatal(e)
	}
	models := &platform.ModelService{Store: NewModelStore(st.DB), Prober: modelProbeStub{}, Secret: []byte(strings.Repeat("m", 32)), NormalizeEndpoint: func(_ string, u string) (string, error) { return u, nil }}
	model := saveTestModel(t, models, ws.ID, actor)
	a, e := (&platform.AgentService{Store: NewAgentStore(st.DB)}).Create(ctx, ws.ID, actor, platform.AgentCreate{Name: "Other", ModelProfileID: model.ID})
	if e != nil {
		t.Fatal(e)
	}
	third, e := st.SaveTrigger(ctx, ws.ID, actor, a.ID, "", manager.TriggerWrite{Kind: "webhook", Config: b.Config, AllowedActions: b.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	tc, e := st.CreateTriggerCredential(ctx, ws.ID, actor, a.ID, third.ID)
	if e != nil {
		t.Fatal(e)
	}
	third, e = st.SaveTrigger(ctx, ws.ID, actor, a.ID, third.ID, manager.TriggerWrite{Kind: third.Kind, Version: third.Version, Enabled: true, Config: third.Config, AllowedActions: third.AllowedActions})
	if e != nil {
		t.Fatal(e)
	}
	result, e := signedTrigger(t, st, third, tc, command)
	if e != nil || result.TaskSpaceID == first.TaskSpaceID {
		t.Fatal("cross workspace alias", result, e)
	}
}
func TestTriggerGroupResolveCreatesNeutralConversationOnly(t *testing.T) {
	st, _, w, actor, agent, _, _ := triggerFixture(t)
	st.SnapshotCapabilities = SnapshotCapabilities
	ctx := context.Background()
	group := manager.NewID()
	for _, v := range []struct {
		q    string
		args []any
	}{{`INSERT INTO platform_web_groups(id,workspace_id,title,created_by)VALUES($1,$2,'new group',$3)`, []any{group, w, actor}}, {`INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status)VALUES($1,$2,$3,'owner','active')`, []any{group, w, actor}}, {`INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, []any{group, w, agent}}} {
		if _, e := st.DB.Exec(v.q, v.args...); e != nil {
			t.Fatal(e)
		}
	}
	candidates, _, e := st.ListTriggerGroupCandidates(ctx, w, actor, agent, 100, "", "")
	if e != nil || len(candidates) != 1 || candidates[0].ID != group {
		t.Fatal(candidates, e)
	}
	target, e := st.ResolveTriggerGroupCandidate(ctx, w, actor, agent, manager.TriggerGroupResolve{Provider: "web", CandidateID: group})
	if e != nil {
		t.Fatal(e)
	}
	again, e := st.ResolveTriggerGroupCandidate(ctx, w, actor, agent, manager.TriggerGroupResolve{Provider: "web", CandidateID: group})
	if e != nil || again != target {
		t.Fatal(again, e)
	}
	var neutral bool
	var count int
	if e = st.DB.QueryRow(`SELECT c.system_owned AND c.created_by IS NULL AND c.channel_created_by IS NULL AND s.system_owned AND s.created_by IS NULL FROM manager_conversations c JOIN manager_sessions s ON s.conversation_id=c.id WHERE c.id=$1`, target.ConversationID).Scan(&neutral); e != nil || !neutral {
		t.Fatal(neutral, e)
	}
	if e = st.DB.QueryRow(`SELECT (SELECT count(*) FROM manager_inputs)+(SELECT count(*) FROM manager_channel_inbox)+(SELECT count(*) FROM manager_web_group_inbox)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("fake inbound", count, e)
	}
	if _, e = st.DB.Exec(`UPDATE platform_web_group_members SET status='removed' WHERE group_id=$1`, group); e != nil {
		t.Fatal(e)
	}
	if _, e = st.ResolveTriggerGroupCandidate(ctx, w, actor, agent, manager.TriggerGroupResolve{Provider: "web", CandidateID: group}); e != nil {
		t.Fatal("employee group incorrectly filtered by operator membership", e)
	}
	var session string
	if e = st.DB.QueryRow(`SELECT id FROM manager_sessions WHERE conversation_id=$1`, target.ConversationID).Scan(&session); e != nil {
		t.Fatal(e)
	}
	if _, e = st.WebGroupMessages(ctx, w, actor, group, 0, 50); e == nil {
		t.Fatal("selection granted group messages")
	}
	if _, e = st.Session(ctx, w, actor, session); e == nil {
		t.Fatal("selection granted group session access")
	}
	if _, e = st.DB.Exec(`UPDATE platform_web_group_agents SET status='removed' WHERE group_id=$1`, group); e != nil {
		t.Fatal(e)
	}
	if _, e = st.ResolveTriggerGroupCandidate(ctx, w, actor, agent, manager.TriggerGroupResolve{Provider: "web", CandidateID: group}); e == nil {
		t.Fatal("removed employee resolved")
	}

}

func TestTriggerGroupTargetsUseEmployeeNotOperatorMembership(t *testing.T) {
	db, store, w, actor, agent, conv, input := triggerNoticeFixture(t)
	ctx := context.Background()
	var group string
	if e := db.QueryRow(`SELECT web_group_id FROM manager_conversations WHERE id=$1`, conv).Scan(&group); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, group, actor); e != nil {
		t.Fatal(e)
	}
	targets, _, e := store.TriggerNotificationCandidates(ctx, w, actor, agent, 100)
	if e != nil || len(targets) != 1 {
		t.Fatal("operator membership filtered employee group", targets, e)
	}
	endNoticeExecution(t, db, input)
	if e = store.MaterializeTriggerNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	var status, contextState string
	if e = db.QueryRow(`SELECT status,context_status FROM manager_trigger_notifications WHERE source_input_id=$1`, input).Scan(&status, &contextState); e != nil || status != "sent" || contextState != "admitted" {
		t.Fatal("valid employee group was denied", status, contextState, e)
	}
	if _, e = store.WebGroupMessages(ctx, w, actor, group, 0, 50); e == nil {
		t.Fatal("configuration granted group message visibility")
	}
	if _, e = db.Exec(`UPDATE platform_web_group_agents SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND agent_id=$2`, group, agent); e != nil {
		t.Fatal(e)
	}
	targets, _, e = store.TriggerNotificationCandidates(ctx, w, actor, agent, 100)
	if e != nil || len(targets) != 0 {
		t.Fatal("removed employee remains target", targets, e)
	}
}
