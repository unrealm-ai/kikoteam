package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestManagerChannelMarkdownDeliveryOverHTTP(t *testing.T) {
	db, m, store, workspace, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	var logs bytes.Buffer
	store.ChannelLog = slog.New(slog.NewJSONHandler(&logs, nil))
	in := inboundFixture(binding, "request", "sender")
	receipt, err := store.ReceiveChannel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var input, session string
	if err = db.QueryRow(`SELECT i.id,i.session_id FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.id=$1`, receipt.ID).Scan(&input, &session); err != nil {
		t.Fatal(err)
	}
	turn := reviewStartTurn(t, db, workspace, session, input, manager.NewID(), 1)
	const reply = "# 已整理 🎉\n\n**研发** 👩🏽‍💻"
	reviewExec(t, db, `WITH next AS (UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq)
 INSERT INTO manager_session_events(workspace_id,session_id,seq,event) SELECT $2,$1,last_event_seq,jsonb_build_object('type','agent.message','input_id',$3::text,'content',jsonb_build_array(jsonb_build_object('type','text','text',$4::text))) FROM next`, session, workspace, input, reply)
	reviewFinishTurn(t, db, session, input, turn)
	for range 2 {
		if err = store.MaterializeChannelReplies(ctx); err != nil {
			t.Fatal(err)
		}
	}
	token := strings.Repeat("h", 40)
	mux := http.NewServeMux()
	(&managercontroller.ChannelServer{Store: store, Manager: m, Token: token}).Routes(mux)
	post := func(path, raw string, markdown bool, out any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		if markdown {
			r.Header.Set("X-Kiko-Delivery-Markdown", "1")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if e := json.Unmarshal(w.Body.Bytes(), out); e != nil {
			t.Fatal(e)
		}
	}
	const path = "/api/internal/v1/channel-deliveries/"
	var batch struct {
		Items []manager.ChannelDelivery `json:"items"`
	}
	post(path+"claim", `{"limit":5}`, false, &batch)
	if len(batch.Items) != 0 {
		t.Fatal("legacy Hub received markdown", batch.Items)
	}
	post(path+"claim", `{"limit":5}`, true, &batch)
	if len(batch.Items) != 1 || batch.Items[0].Text != reply || batch.Items[0].Format != "markdown" {
		t.Fatal(batch.Items)
	}
	d := batch.Items[0]
	var authorized manager.ChannelDelivery
	post(path+d.ID+"/authorize", `{"lease_token":"`+d.LeaseToken+`"}`, false, &authorized)
	if !authorized.Allowed || authorized.TargetDigest == "" || authorized.ID != d.ID || authorized.Format != d.Format || authorized.Text != reply {
		t.Fatal(authorized)
	}
	msgID := "provider-reply-id"
	raw, err := json.Marshal(manager.DeliveryCompletion{LeaseToken: authorized.LeaseToken, TargetDigest: authorized.TargetDigest, Status: "sent", ProviderMessageID: msgID, EvidenceType: "send_response", EvidenceID: msgID})
	if err != nil {
		t.Fatal(err)
	}
	post(path+d.ID+"/complete", string(raw), false, &struct{}{})
	want := map[string]map[string]string{
		"channel.inbox_persisted":          {"inbox_id": receipt.ID, "provider_event_id": in.ProviderEventID},
		"channel.inbox_claimed":            {"inbox_id": receipt.ID},
		"channel.input_persisted":          {"inbox_id": receipt.ID, "input_id": input, "session_id": session},
		"channel.outbox_persisted":         {"inbox_id": receipt.ID, "input_id": input, "turn_id": turn, "delivery_id": d.ID},
		"channel.outbox_receipt_persisted": {"inbox_id": receipt.ID, "delivery_id": d.ID, "status": "sent"},
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatal(err)
		}
		event, _ := fields["event"].(string)
		if expected, ok := want[event]; ok {
			for key, value := range expected {
				if fields[key] != value {
					t.Errorf("%s missing expected %s correlation", event, key)
				}
			}
			delete(want, event)
		}
		for _, forbidden := range []string{"text", "body", "lease_token", "target_digest", "sender_id"} {
			if _, ok := fields[forbidden]; ok {
				t.Errorf("%s logged %s", event, forbidden)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing lifecycle events: %v", want)
	}

}

func TestChannelReplyForSteeredInputsUsesLatestConsumedMessage(t *testing.T) {
	for _, provider := range []string{"dingtalk", "feishu", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			db, sessions, store, workspace, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			switch provider {
			case "dingtalk":
				reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='dingtalk' WHERE id=$1`, binding)
			case "wecom":
				reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',bot_open_id='bot',external_tenant_id='bot' WHERE id=$1`, binding)
			}
			intake := func(message string) (input, session string) {
				t.Helper()
				in := inboundFixture(binding, message, "sender")
				switch provider {
				case "dingtalk":
					in.BotID, in.SenderIDType, in.SenderTenant = "bot", "user_id", in.ExternalTenantID
				case "wecom":
					in.ExternalChatID, in.ExternalTenantID, in.BotID, in.SenderIDType = in.SenderID, "bot", "bot", "user_id"
				}
				box, err := store.ReceiveChannel(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRowContext(ctx, `SELECT i.id,i.session_id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, box.ID).Scan(&input, &session); err != nil {
					t.Fatal(err)
				}
				return input, session
			}
			root, chat := intake("root")
			turn := reviewStartTurn(t, db, workspace, chat, root, manager.NewID(), 1)
			earlier, _ := intake("earlier")
			latest, _ := intake("latest")
			lateReceipt, _ := intake("late-receipt")
			reviewExec(t, db, `UPDATE manager_inputs SET turn_id=$1,batch_input_id=id,status='started',started_at=now(),context_state='committed',context_committed_at=now()
 WHERE id IN ($2,$3)`, turn, earlier, latest)
			reviewExec(t, db, `UPDATE manager_inputs SET turn_id=$1,batch_input_id=id,status='started',started_at=now(),context_state='unknown' WHERE id=$2`, turn, lateReceipt)
			for _, body := range []string{"已受理。", "已整理三项。"} {
				// Assistant events still use the starting input for history.
				reviewExec(t, db, `WITH next AS (UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq)
 INSERT INTO manager_session_events(workspace_id,session_id,seq,event)
 SELECT $2,$1,last_event_seq,jsonb_build_object('type','agent.message','input_id',$3::text,'turn_id',$4::text,'content',jsonb_build_array(jsonb_build_object('type','text','text',$5::text))) FROM next`, chat, workspace, root, turn, body)
			}
			reviewFinishTurn(t, db, chat, root, turn)
			reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE turn_id=$1 AND status='started'`, turn)
			reviewExec(t, db, `UPDATE manager_inputs SET context_state='committed',context_committed_at=now() WHERE id=$1`, lateReceipt)

			var scans sync.WaitGroup
			for range 3 {
				scans.Go(func() {
					if err := store.MaterializeChannelReplies(ctx); err != nil {
						t.Error(err)
					}
				})
			}
			scans.Wait()
			batch, err := store.ClaimChannelDeliveries(ctx, 5, true)
			if err != nil || len(batch) != 1 {
				t.Fatal(batch, err)
			}
			delivery, err := store.AuthorizeChannelDelivery(ctx, batch[0].ID, batch[0].LeaseToken)
			if err != nil || !delivery.Allowed || delivery.ReplyToMessageID != "latest" || delivery.Text != "已受理。\n已整理三项。" ||
				delivery.SessionWebhookEligible != (provider == "dingtalk") {
				t.Fatal(delivery, err)
			}
		})
	}
}

func TestTaskReportChannelReplyFormat(t *testing.T) {
	const reportBody = "## 结果\n\n**已完成**"
	for _, tc := range []struct{ name, provider, status, summary, text, format string }{
		{"feishu", "feishu", "completed", reportBody, reportBody, "markdown"},
		{"dingtalk", "dingtalk", "completed", reportBody, reportBody, "markdown"},
		{"image", "feishu", "completed", "![截图](key)", "![截图](key)", "text"},
		{"html-image", "dingtalk", "completed", "<IMG src='key'>", "<IMG src='key'>", "text"},
		{"empty", "dingtalk", "completed", "", "任务本轮已结束，没有可发送的文字回复。", "text"},
		{"failed", "feishu", "failed", reportBody, "任务结果已到达，但主会话回复未成功完成，请查看任务详情。", "text"},
		{"canceled", "dingtalk", "canceled", reportBody, "任务结果已到达，但主会话回复未成功完成，请查看任务详情。", "text"},
		{"existing-delivery", "feishu", "completed", reportBody, reportBody, "text"},
		{"legacy-mixed-turn", "feishu", "completed", reportBody, reportBody, "markdown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, sessions, store, workspace, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			in := inboundFixture(binding, "original", "sender")
			if tc.provider == "dingtalk" {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='dingtalk' WHERE id=$1`, binding)
				in.BotID, in.SenderIDType, in.SenderTenant = "bot", "user_id", in.ExternalTenantID
			} else {
				in.ThreadID = "original-thread"
			}
			box, err := store.ReceiveChannel(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
				t.Fatal(err)
			}
			var chat, input string
			if err = db.QueryRowContext(ctx, `SELECT session_id,id FROM manager_inputs WHERE id=(SELECT input_id FROM manager_channel_inbox WHERE id=$1)`, box.ID).Scan(&chat, &input); err != nil {
				t.Fatal(err)
			}
			identity := manager.DelegationIdentity{SessionID: chat, EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
			reviewExec(t, db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, chat, identity.EngineID, identity.AssignmentID)
			chatTurn := reviewStartTurn(t, db, workspace, chat, input, identity.AssignmentID, 1)
			scope, err := store.DelegationSource(ctx, identity)
			if err != nil {
				t.Fatal(err)
			}
			delegate := manager.Delegation{Manager: sessions, Store: store}
			result, err := delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{RequestKey: "report", Instruction: "整理结果"})
			if err != nil {
				t.Fatal(err)
			}
			task := result.(map[string]any)
			taskSession, taskInput := task["session_id"].(string), *task["input_id"].(*string)
			reviewFinishTurn(t, db, chat, input, chatTurn)
			taskTurn := reviewStartTurn(t, db, workspace, taskSession, taskInput, manager.NewID(), 1)
			reviewFinishTurn(t, db, taskSession, taskInput, taskTurn)
			report, err := store.ReportTaskResult(ctx, manager.TaskResult{TurnID: taskTurn})
			if err != nil || report.TargetInputID == nil {
				t.Fatal(report, err)
			}
			reportTurn := reviewStartTurn(t, db, workspace, chat, *report.TargetInputID, identity.AssignmentID, 1)
			if tc.name == "legacy-mixed-turn" {
				message := in
				message.ProviderEventID, message.ProviderMessageID, message.Text = "followup", "followup", "怎么样了"
				followup, e := store.ReceiveChannel(ctx, message)
				if e != nil {
					t.Fatal(e)
				}
				if e = store.ProcessChannelInbox(ctx, sessions); e != nil {
					t.Fatal(e)
				}
				var inputID string
				if e = db.QueryRowContext(ctx, `SELECT input_id FROM manager_channel_inbox WHERE id=$1`, followup.ID).Scan(&inputID); e != nil {
					t.Fatal(e)
				}
				reviewExec(t, db, `UPDATE manager_inputs SET turn_id=$1,batch_input_id=id,status='processed',context_state='committed' WHERE id=$2`, reportTurn, inputID)
			}
			reviewFinishTurn(t, db, chat, *report.TargetInputID, reportTurn)
			reviewExec(t, db, `UPDATE manager_turns SET status=$2,result_summary=$3 WHERE id=$1`, reportTurn, tc.status, tc.summary)
			if tc.name == "existing-delivery" {
				reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES(gen_random_uuid(),$1,$2,1,$3,$4)`, box.ID, binding, "task_reply:"+reportTurn, reportBody)
			}
			for range 2 {
				if err = store.MaterializeChannelReplies(ctx); err != nil {
					t.Fatal(err)
				}
			}
			batch, err := store.ClaimChannelDeliveries(ctx, 5, true)
			if err != nil || len(batch) != 2 {
				t.Fatal(batch, err)
			}
			for _, delivery := range batch {
				if delivery.ReplyToMessageID != in.ProviderMessageID || delivery.ChatID != in.ExternalChatID || delivery.ThreadID != in.ThreadID {
					t.Fatalf("unexpected reply target: %+v", delivery)
				}
			}
			var text, format string
			err = db.QueryRowContext(ctx, `SELECT text,content_format FROM manager_channel_deliveries WHERE inbox_id=$1 AND kind=$2`, box.ID, "task_reply:"+reportTurn).Scan(&text, &format)
			if err != nil || text != tc.text || format != tc.format {
				t.Fatalf("got %q (%s), want %q (%s): %v", text, format, tc.text, tc.format, err)
			}
		})
	}
}
