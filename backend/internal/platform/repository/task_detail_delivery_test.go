package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
)

func TestTaskDetailChannelDispatchAndSilentContext(t *testing.T) {
	for _, chatType := range []string{"direct", "group"} {
		t.Run(chatType, func(t *testing.T) {
			db, store, issue := taskDetailGrantFixture(t, chatType)
			store.TriggerSecret = []byte(strings.Repeat("test", 12))
			ctx := t.Context()
			var task string
			if err := db.QueryRow(`SELECT task_id FROM manager_sessions WHERE id=$1`, issue.SessionID).Scan(&task); err != nil {
				t.Fatal(err)
			}
			send := func(want string) {
				t.Helper()
				r, err := store.QueueTaskDetailCard(ctx, *issue.Source, task, "")
				if err != nil || r.Status != want {
					t.Fatalf("queue receipt: %+v, %v", r, err)
				}
				raw, err := json.Marshal(r)
				if err != nil || strings.Contains(string(raw), "https://") {
					t.Fatal("receipt may not expose the bearer URL")
				}
			}
			send("accepted")
			if _, err := store.QueueTaskDetailCard(ctx, *issue.Source, manager.NewID(), ""); err == nil {
				t.Fatal("an unrelated task can be sent")
			}
			old, err := store.ClaimChannelDeliveries(ctx, 10, true, true)
			if err != nil || len(old) != 0 {
				t.Fatalf("old Hub claimed the card: %d, %v", len(old), err)
			}
			claims, err := store.ClaimChannelDeliveries(ctx, 10, true, true, true)
			if err != nil || len(claims) != 1 || claims[0].TaskDetailCard == nil || claims[0].TaskDetailCard.URL != "" {
				t.Fatalf("claim exposed a URL or was not created: %d, %v", len(claims), err)
			}
			claim := claims[0]
			out, err := store.AuthorizeChannelDelivery(ctx, claim.ID, claim.LeaseToken)
			if err != nil || out.TaskDetailCard == nil {
				t.Fatalf("authorization: %+v, %v", out.TaskDetailCard == nil, err)
			}
			token, ok := strings.CutPrefix(out.TaskDetailCard.URL, "https://tasks.example.test/tasks/")
			if !ok || len(token) != 43 || strings.Contains(out.Text, token) {
				t.Fatal("authorization returned an invalid public route")
			}
			grant, err := store.ResolveTaskDetailGrant(ctx, token)
			if err != nil || grant.CanWrite() != (chatType == "direct") {
				t.Fatalf("wrong grant scope %q: %v", grant.Scope, err)
			}
			if handled, err := store.CheckTaskDetailDispatch(ctx, claim.ID, claim.LeaseToken); !handled || err != nil {
				t.Fatalf("verified dispatch check failed: %t %v", handled, err)
			}
			reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=false WHERE singleton`)
			if handled, err := store.CheckTaskDetailDispatch(ctx, claim.ID, claim.LeaseToken); !handled || err == nil {
				t.Fatal("disabled settings did not stop predispatch")
			}
			reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=true WHERE singleton`)
			receipt := manager.DeliveryCompletion{LeaseToken: out.LeaseToken, TargetDigest: out.TargetDigest, Status: "sent",
				EvidenceType: "send_response", ProviderMessageID: "provider-fixture", EvidenceID: "provider-fixture",
				ProviderChatID: issue.Destination.ChatID, ProviderThreadID: issue.Destination.TopicID,
				Presentation: manager.TaskDetailCardPresentation}
			bad := receipt
			bad.Presentation = ""
			if err := store.CompleteChannelDelivery(ctx, claim.ID, bad); err == nil {
				t.Fatal("accepted card receipt without presentation evidence")
			}
			for range 2 {
				if err = store.CompleteChannelDelivery(ctx, claim.ID, receipt); err != nil {
					t.Fatal(err)
				}
				if err = store.MaterializeChannelReplies(ctx); err != nil {
					t.Fatal(err)
				}
			}
			send("accepted")
			var notes, triggers int
			var text string
			err = db.QueryRow(`SELECT count(*), count(*) FILTER (WHERE trigger_agent_loop), COALESCE(max(text),'')
 FROM manager_inputs WHERE session_id=$1 AND source_kind='system' AND text LIKE '%已发送任务%'`, issue.Source.Identity.SessionID).Scan(&notes, &triggers, &text)
			if err != nil || notes != 1 || triggers != 0 || !strings.Contains(text, task) || strings.Contains(text, token) {
				t.Fatalf("bad silent receipt: notes=%d triggers=%d %v", notes, triggers, err)
			}
			var cleared bool
			if err = db.QueryRow(`SELECT sealed_url IS NULL FROM manager_task_detail_deliveries WHERE delivery_id=$1`, claim.ID).Scan(&cleared); err != nil || !cleared {
				t.Fatal("sent card retained its outbox secret", err)
			}
		})
	}
}

func TestTriggerTaskDetailLinkAttachesOnlyToAuthorizedResultNotice(t *testing.T) {
	for _, kind := range []string{"webhook", "schedule"} {
		t.Run(kind, func(t *testing.T) { verifyTriggerTaskDetailNotifications(t, kind) })
	}
}

func verifyTriggerTaskDetailNotifications(t *testing.T, kind string) {
	t.Helper()
	db, w, actor, agent, execution := triggerRuntimeFixture(t)
	ctx := t.Context()
	if kind == "schedule" {
		reviewExec(t, db, `UPDATE manager_triggers SET kind='schedule' WHERE id=$1`, execution.TriggerID)
	}
	store := &managerrepo.Store{DB: db}
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access}', '{"mode":"all","group_members_enabled":true,"allow":[],"deny":[]}'::jsonb) WHERE id=$1`, agent)
	binding := manager.NewID()
	reviewExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,credential_ciphertext,trigger,history,access)
 VALUES($1,$2,$3,'feishu',1,1,1,true,'cli_fixture','tenant',decode('00','hex'),'{"mention":true}','{"enabled":false}','{"mode":"all"}')`, binding, w, agent)
	var raw []manager.TriggerNotificationTarget
	for _, dest := range []string{"new-hub-group", "fallback-group", "old-hub-group", "disabled-group"} {
		target := manager.NewID()
		reviewExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,system_owned,channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id)
VALUES($1,$2,$3,true,$4,1,'tenant','group',$5)`, target, w, agent, binding, dest)
		reviewExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,mode,configuration)VALUES($1,$2,$3,$4,$5,'chat','{}')`, manager.NewID(), w, agent, target, actor)
		raw = append(raw, manager.TriggerNotificationTarget{ConversationID: target})
	}
	targets, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var task string
	if err = db.QueryRow(`SELECT task_id FROM manager_trigger_events WHERE id=$1`, execution.EventID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit or rollback.
	if err = managerrepo.SnapshotTriggerNotificationsTx(ctx, tx, execution.TriggerID, task, 1, targets); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = (&TaskLinkSettingsStore{DB: db}).SaveAdmin(ctx, actor, 0, "https://tasks.example.test", true); err != nil {
		t.Fatal(err)
	}
	endNoticeExecution(t, db, execution.InputID)
	if err = store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimTriggerNotifications(ctx, 10)
	if err != nil || len(claims) != 4 {
		t.Fatalf("notification claims: %d %v", len(claims), err)
	}
	var card, fallback manager.ChannelDelivery
	for _, chat := range []string{"old-hub-group", "new-hub-group", "fallback-group", "disabled-group"} {
		for _, d := range claims {
			if d.TaskDetailURL != "" {
				t.Fatal("notification claim exposed capability")
			}
			if d.ChatID != chat {
				continue
			}
			switch chat {
			case "old-hub-group":
				d, err = store.AuthorizeTriggerNotification(ctx, d.ID, d.LeaseToken)
			case "disabled-group":
				reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=false WHERE singleton`)
				d, err = store.AuthorizeTriggerNotificationWithResultCard(ctx, d.ID, d.LeaseToken)
			default:
				d, err = store.AuthorizeTriggerNotificationWithResultCard(ctx, d.ID, d.LeaseToken)
			}
			if err != nil || !strings.Contains(d.Text, "public result") || strings.Contains(d.Text, "/tasks/") {
				t.Fatal("notice body failed or changed", err)
			}
			linked := chat == "new-hub-group" || chat == "fallback-group"
			if (d.TaskDetailURL != "") != linked || d.TriggerResultCard != linked {
				t.Fatal("unexpected optional result link presence")
			}
			if linked {
				presentation := manager.TaskDetailCardPresentation
				if chat == "fallback-group" {
					presentation = manager.TaskDetailLinkPresentation
					fallback = d
				} else {
					card = d
				}
				verifyTriggerDetailGrant(t, store, d, presentation)
			}
		}
	}
	reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=true WHERE singleton`)
	verifyTriggerDetailContexts(t, store)
	verifyTriggerDetailSession(t, store, actor, card, fallback)
	var grants int
	if err = db.QueryRow(`SELECT count(*) FROM manager_task_detail_grants WHERE task_id=$1`, task).Scan(&grants); err != nil || grants != 2 {
		t.Fatalf("unexpected issuance count %d: %v", grants, err)
	}
}

func verifyTriggerDetailGrant(t *testing.T, store *managerrepo.Store, d manager.ChannelDelivery, presentation string) {
	t.Helper()
	token, ok := strings.CutPrefix(d.TaskDetailURL, "https://tasks.example.test/tasks/")
	if !ok || len(token) != 43 {
		t.Fatal("invalid link")
	}
	if _, err := store.ResolveTaskDetailGrant(t.Context(), token); err == nil {
		t.Fatal("unconfirmed result notice exposed task")
	}
	receipt := manager.DeliveryCompletion{LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, Status: "sent",
		ProviderMessageID: "provider-notice", EvidenceType: "send_response", EvidenceID: "provider-notice",
		Presentation: presentation, ProviderCreatedAtMS: 1001, ProviderChatID: d.ChatID, ProviderThreadID: d.ThreadID}
	if err := store.CompleteTriggerNotification(t.Context(), d.ID, receipt); err != nil {
		t.Fatal(err)
	}
	result, err := store.ResolveTaskDetailGrant(t.Context(), token)
	if err != nil || result.CanWrite() || result.TriggerNotificationID != d.ID || result.Destination.ChatID != d.ChatID {
		t.Fatalf("bad result grant: %q %v", result.Scope, err)
	}
}

func verifyTriggerDetailSession(t *testing.T, store *managerrepo.Store, actor string, d, fallback manager.ChannelDelivery) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	token := strings.TrimPrefix(d.TaskDetailURL, "https://tasks.example.test/tasks/")
	peerToken := strings.TrimPrefix(fallback.TaskDetailURL, "https://tasks.example.test/tasks/")
	g, err := store.ResolveTaskDetailGrant(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.ResolveTaskDetailGrant(ctx, peerToken)
	if err != nil {
		t.Fatal(err)
	}
	db := store.DB
	var sourceTurn, sourceInput string
	if err = db.QueryRowContext(ctx, `SELECT source_turn_id,source_input_id FROM manager_trigger_notifications WHERE id=$1`, d.ID).Scan(&sourceTurn, &sourceInput); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE manager_turns SET turn_no=2 WHERE id=$1`, sourceTurn)
	reviewExec(t, db, `UPDATE manager_inputs SET seq=2 WHERE id=$1`, sourceInput)
	reviewExec(t, db, `UPDATE manager_sessions SET last_input_seq=2,last_turn_no=2 WHERE id=$1`, g.SessionID)
	earlier, later := manager.NewID(), manager.NewID()
	for idx, turn := range []string{earlier, later} {
		seq, input := idx*2+1, manager.NewID()
		reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,seq,text,status,submitted_by,admission_epoch)
 VALUES($1,$2,$3,$4,'synthetic continuation','processed',$5,1)`, input, g.WorkspaceID, g.SessionID, seq, actor)
		reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 VALUES($1,$2,$3,$4,$5,$6,1,'completed',now())`, turn, g.WorkspaceID, g.SessionID, input, seq, manager.NewID())
		reviewExec(t, db, `UPDATE manager_inputs SET turn_id=$2 WHERE id=$1`, input, turn)
	}
	reviewExec(t, db, `UPDATE manager_sessions SET last_input_seq=3,last_turn_no=3 WHERE id=$1`, g.SessionID)
	// A second run of the same trigger in the same task space must stay private.
	otherTask, other := manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_tasks(id,workspace_id,agent_id,task_space_id,trigger_id,trigger_version)
 SELECT $1,workspace_id,agent_id,task_space_id,trigger_id,trigger_version FROM manager_tasks WHERE id=$2`, otherTask, g.TaskID)
	reviewExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,task_space_id,task_id,trigger_id,trigger_version,mode,configuration,last_input_seq,last_turn_no)
 SELECT $1,workspace_id,agent_id,task_space_id,id,trigger_id,trigger_version,'task','{}',1,1 FROM manager_tasks WHERE id=$2`, other, otherTask)
	appendEvent := func(session, turn, kind, status, tag string) {
		t.Helper()
		reviewExec(t, db, `WITH s AS (UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING *)
 INSERT INTO manager_session_events(workspace_id,session_id,seq,event)
 SELECT workspace_id,id,last_event_seq,jsonb_build_object('session_id',id,'seq',last_event_seq::text,'type',$3::text,'status',$4::text,'turn_id',NULLIF($2::text,''),'output',$5::text) FROM s`, session, turn, kind, status, tag)
	}
	appendEvent(g.SessionID, earlier, "agent.tool_result", "completed", "EARLIER_TURN")
	appendEvent(g.SessionID, sourceTurn, "agent.tool_result", "completed", "NOTICE_TURN")
	appendEvent(g.SessionID, "", "turn.updated", "completed", "SESSION_EVENT")
	appendEvent(other, "", "agent.tool_result", "completed", "OTHER_SESSION")

	svc := &manager.Service{Store: store, CursorSecret: []byte(strings.Repeat("s", 32))}
	mux := http.NewServeMux()
	(&managercontroller.Server{Service: svc}).Routes(mux)
	client := taskDetailPageClient{mux, token}
	var history manager.EventPage
	raw := client.request(t, "GET", "/execution", "", "", "", 200).Body.Bytes()
	if err = json.Unmarshal(raw, &history); err != nil || history.ResumeCursor == nil {
		t.Fatalf("invalid execution history: %v", err)
	}
	if bytes.Contains(raw, []byte("OTHER_SESSION")) {
		t.Fatal("trigger grant exposed a different Session")
	}
	for _, tag := range []string{"EARLIER_TURN", "NOTICE_TURN", "SESSION_EVENT"} {
		if !bytes.Contains(raw, []byte(tag)) {
			t.Fatalf("trigger history missed %s", tag)
		}
	}
	api := httptest.NewServer(mux)
	defer api.Close()
	req, err := http.NewRequestWithContext(ctx, "GET", api.URL+taskDetailPagePath+"/execution/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Last-Event-ID", *history.ResumeCursor)
	resp, err := api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream handshake: %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	waitEvent := func(tag string) {
		t.Helper()
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") && strings.Contains(scanner.Text(), tag) {
				return
			}
		}
		t.Fatalf("stream did not deliver %s: %v", tag, scanner.Err())
	}
	appendEvent(g.SessionID, later, "agent.tool_result", "completed", "LATER_TURN")
	waitEvent("LATER_TURN")

	assets, err := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store.WorkAssets = assets
	content := []byte("synthetic task artifact")
	version, err := assets.PutWorkFile(ctx, g.WorkspaceID, content)
	if err != nil {
		t.Fatal(err)
	}
	file := func(turn string) string {
		t.Helper()
		id := manager.NewID()
		reviewExec(t, db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes)
 VALUES($1,$2,'result.txt','result.txt','text/plain',$3,$4)`, id, turn, version, len(content))
		return id
	}
	earlierArtifact, laterArtifact := file(earlier), file(later)
	otherInput, otherTurn := manager.NewID(), manager.NewID()
	reviewExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,seq,text,status,submitted_by,admission_epoch) VALUES($1,$2,$3,1,'other Session','processed',$4,1)`, otherInput, g.WorkspaceID, other, actor)
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 VALUES($1,$2,$3,$4,1,$5,1,'completed',now())`, otherTurn, g.WorkspaceID, other, otherInput, manager.NewID())
	client.request(t, "GET", "/artifacts/"+file(otherTurn), "", "", "", 404)
	for _, id := range []string{earlierArtifact, laterArtifact} {
		if !bytes.Equal(client.request(t, "GET", "/artifacts/"+id, "", "", "", 200).Body.Bytes(), content) {
			t.Fatal("trigger read-only grant must be able to download artifacts across the Session")
		}
	}
	view := detailResponse(t, client.request(t, "GET", "", "", "", "", 200))
	if view.Permission != "read" || view.CanAppend || view.CanStop || view.CanContinue || len(view.Artifacts) != 2 || view.Progress == nil || view.Progress.Phase != "completed" {
		t.Fatalf("unexpected trigger overview: %+v", view)
	}
	reviewExec(t, db, `UPDATE manager_turns SET status='running',finished_at=NULL WHERE id=$1`, later)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, g.SessionID, later)
	view = detailResponse(t, client.request(t, "GET", "", "", "", "", 200))
	if view.Status != "running" || view.Progress == nil || view.Progress.Phase != "running" {
		t.Fatalf("overview stayed on the old notification status: %s %+v", view.Status, view.Progress)
	}
	if err = store.RevokeTaskDetailGrant(ctx, g.WorkspaceID, g.ID); err != nil {
		t.Fatal(err)
	}
	waitEvent("task_detail_link_unavailable")
	client.request(t, "GET", "/execution", "", "", "", 404)
	client.request(t, "GET", "/artifacts/"+earlierArtifact, "", "", "", 404)
	reviewExec(t, db, `UPDATE manager_task_detail_grants SET issued_at=now()-interval '4 days',expires_at=now()-interval '1 day' WHERE id=$1`, peer.ID)
	expired := taskDetailPageClient{mux, peerToken}
	expired.request(t, "GET", "/execution/stream", "", "", "", 404)
	expired.request(t, "GET", "/artifacts/"+earlierArtifact, "", "", "", 404)
}

func verifyTriggerDetailContexts(t *testing.T, store *managerrepo.Store) {
	t.Helper()
	jobs, err := store.ClaimNotificationContexts(t.Context(), 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("notification context jobs: %d %v", len(jobs), err)
	}
	for _, j := range jobs {
		isCard := j.ChatID == "new-hub-group"
		expected := manager.TaskDetailLinkPresentation
		if isCard {
			expected = manager.TaskDetailCardPresentation
		}
		if j.TriggerFormat != expected || !strings.Contains(j.TriggerText, "public result") || strings.Contains(j.TriggerText, "/tasks/") {
			t.Fatalf("wrong redacted context: %+v", j)
		}
		err = store.CompleteNotificationContext(t.Context(), j.ID, manager.NotificationContextCompletion{
			LeaseToken: j.LeaseToken, AnchorID: j.TriggerMessageID, AnchorTime: j.TriggerCreatedAtMS, ChatID: j.ChatID, ThreadID: j.ThreadID,
			Snapshot: manager.ChannelHistorySnapshot{Status: "complete"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var body string
		var loops bool
		if err = store.DB.QueryRow(`SELECT text,trigger_agent_loop FROM manager_inputs WHERE notification_id=$1 AND source_kind='task_result'`, j.ID).Scan(&body, &loops); err != nil {
			t.Fatal(err)
		}
		if loops || strings.Contains(body, "/tasks/") || !strings.Contains(body, "public result") || !strings.HasPrefix(body, "[系统消息] 已发送任务结果，任务详情链接可查看。\n") {
			t.Fatal("incorrect silent input:", body, loops)
		}
	}
}
