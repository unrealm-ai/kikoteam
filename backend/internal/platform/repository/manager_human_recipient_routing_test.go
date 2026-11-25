package repository

import (
	"context"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestHumanRecipientFallbackAndRevocation(t *testing.T) {
	for _, decision := range []string{"approve", "revise"} {
		t.Run(decision, func(t *testing.T) { testHumanRecipientFallbackAndRevocation(t, decision) })
	}
}
func testHumanRecipientFallbackAndRevocation(t *testing.T, decision string) {
	db, sessions, _, workspace, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := &managerrepo.Store{DB: db}
	member := func(email string) manager.HumanRecipient {
		t.Helper()
		id := service.NewID()
		insertWorkspaceAccount(t, db, id, email)
		reviewExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspace, id)
		return manager.HumanRecipient{Ref: service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: id}}
	}
	specialist, fallback := member("specialist@example.test"), member("fallback@example.test")
	specialist.Scope, fallback.IsDefault = "财务", true
	rules, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Recipients: []manager.HumanRecipient{specialist, fallback}})
	if err != nil || rules.Version != 1 || !rules.Recipients[1].IsDefault {
		t.Fatal(rules, err)
	}
	specialist, fallback = rules.Recipients[0], rules.Recipients[1]
	duplicate := specialist
	duplicate.IsDefault = true
	if _, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: rules.Version, Recipients: []manager.HumanRecipient{duplicate, fallback}}); err == nil {
		t.Fatal("two default recipients were accepted")
	}
	conversation, err := sessions.CreateConversation(ctx, workspace, owner, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	create := manager.CreateSession{ConversationID: conversation.ID, Mode: "task"}
	create.Input = &struct {
		Text string `json:"text"`
	}{Text: "Synthetic confirmation routing test"}
	task, _, err := sessions.CreateSession(ctx, workspace, owner, "human-routing", create)
	if err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: task.ID, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if err := store.RegisterEngine(ctx, manager.Engine{ID: identity.EngineID, HostID: "human-routing", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	var input string
	if err := db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND seq=1`, task.ID).Scan(&input); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, workspace, task.ID, input, identity.AssignmentID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, task.ID, turn, identity.EngineID, identity.AssignmentID)
	scope := manager.HumanRuntimeScope{Identity: identity, InputID: input}
	candidates := func(wantSpecialist, wantDefault bool) {
		t.Helper()
		response, err := store.HumanTaskTool(ctx, scope, "recipients", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		result := response.(map[string]any)
		items := result["items"].([]map[string]string)
		selected := result["default_recipient"].(map[string]string)
		if (len(items) == 1 && items[0]["candidateId"] == specialist.ID) != wantSpecialist || (selected["candidateId"] == fallback.ID) != wantDefault {
			t.Fatal(result)
		}
	}
	candidates(true, true)
	question := &manager.HumanQuestion{RecipientID: specialist.ID, RulesVersion: rules.Version, Question: "确认费用？", AnswerType: "decision"}
	created, err := store.HumanTaskTool(ctx, scope, "request", "", question)
	if err != nil {
		t.Fatal(err)
	}
	request := created.(manager.HumanRequest)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, request.ID)
	inbox := func(want int) {
		t.Helper()
		items, err := store.HumanInbox(ctx, workspace, specialist.Ref.ExternalID)
		if err != nil || len(items) != want {
			t.Fatal(items, err)
		}
	}
	inbox(1)
	setDeny := func(user string) {
		t.Helper()
		reviewExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,deny}',
 CASE WHEN $2='' THEN '[]'::jsonb ELSE jsonb_build_array(jsonb_build_object('channel','web','kind','user','id',$2::text)) END) WHERE id=$1`, agent, user)
	}
	setDeny(specialist.Ref.ExternalID)
	candidates(false, true)
	inbox(1)
	changed := specialist
	changed.Scope = "unauthorized expansion"
	_, err = store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: rules.Version, Recipients: []manager.HumanRecipient{changed, fallback}})
	assertChannelError(t, err, "human_recipient_forbidden")
	setDeny(fallback.Ref.ExternalID)
	candidates(true, false)
	setDeny(specialist.Ref.ExternalID)
	answer := manager.HumanAnswer{Version: 1, Decision: decision}
	if decision == "revise" {
		answer.Text = "移除过期成本，提供整改草案"
	}
	if _, err = store.AnswerHumanRequest(ctx, workspace, fallback.Ref.ExternalID, request.ID, answer); err == nil {
		t.Fatal("another member could answer the request")
	}
	reviewExec(t, db, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, workspace, specialist.Ref.ExternalID)
	if _, err = store.HumanInbox(ctx, workspace, specialist.Ref.ExternalID); err == nil {
		t.Fatal("disabled member could access the inbox")
	}
	if _, err = store.AnswerHumanRequest(ctx, workspace, specialist.Ref.ExternalID, request.ID, answer); err == nil {
		t.Fatal("disabled member could answer the request")
	}
	reviewExec(t, db, `UPDATE platform_members SET status='active' WHERE workspace_id=$1 AND user_id=$2`, workspace, specialist.Ref.ExternalID)
	reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE input_id=$1`, input)
	reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
	if decision == "revise" {
		testHumanClarificationStaysPending(t, ctx, store, workspace, specialist.Ref.ExternalID, request.ID)
	}
	if _, err = store.AnswerHumanRequest(ctx, workspace, specialist.Ref.ExternalID, request.ID, answer); err != nil {
		t.Fatal("assigned Web recipient could not answer after losing employee use", err)
	}
	if decision == "revise" {
		testHumanRevisionReceipt(t, ctx, store, workspace, specialist.Ref.ExternalID, request.ID, answer)
		var resume string
		if err := db.QueryRowContext(ctx, `SELECT resume_input_id FROM manager_human_requests WHERE id=$1`, request.ID).Scan(&resume); err != nil {
			t.Fatal(err)
		}
		correctionTurn := service.NewID()
		reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)
 VALUES($1,$2,$3,$4,2,$5,1,'running')`, correctionTurn, workspace, task.ID, resume, identity.AssignmentID)
		reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, resume, correctionTurn)
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, task.ID, correctionTurn)
		_, err := store.HumanTaskTool(ctx, manager.HumanRuntimeScope{Identity: identity, InputID: resume}, "request", "", &manager.HumanQuestion{
			RecipientID: fallback.ID, RulesVersion: rules.Version, Question: "只补信息", AnswerType: "information",
		})
		assertChannelError(t, err, "human_reapproval_required")
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
	}
	var allowed bool
	if err = db.QueryRow(`SELECT manager_input_admission_allowed(resume_input_id) FROM manager_human_requests WHERE id=$1`, request.ID).Scan(&allowed); err != nil || !allowed {
		t.Fatal("original task could not continue", allowed, err)
	}
	_, _, err = sessions.CreateSession(ctx, workspace, specialist.Ref.ExternalID, "unrelated-web-task", create)
	assertChannelError(t, err, "employee_access_denied")
	setDeny(owner)
	if err = db.QueryRow(`SELECT manager_input_admission_allowed(resume_input_id) FROM manager_human_requests WHERE id=$1`, request.ID).Scan(&allowed); err != nil || allowed {
		t.Fatal("confirmation overrode the original actor's revoked grant", allowed, err)
	}
	setDeny(specialist.Ref.ExternalID)
	reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',answer=NULL,answered_at=NULL,resume_input_id=NULL WHERE id=$1`, request.ID)
	if _, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: rules.Version, Recipients: []manager.HumanRecipient{fallback}}); err != nil {
		t.Fatal(err)
	}
	inbox(0)
	if _, err := store.AnswerHumanRequest(ctx, workspace, specialist.Ref.ExternalID, request.ID, answer); err == nil {
		t.Fatal("recipient removed from configuration could still respond")
	}
	var storedRecipient string
	if err := db.QueryRow(`SELECT recipient->>'external_id' FROM manager_human_requests WHERE id=$1`, request.ID).Scan(&storedRecipient); err != nil || storedRecipient != specialist.Ref.ExternalID {
		t.Fatal("pending request lost original recipient", storedRecipient, err)
	}
}

func TestHumanRecipientsAcrossChannels(t *testing.T) {
	db, sessions, _, workspace, owner, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := sessions.Store.(*managerrepo.Store)
	engine := service.NewID()
	if err := store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "channel-human-routing", Endpoint: "http://127.0.0.1:1", Capacity: 10}); err != nil {
		t.Fatal(err)
	}
	const externalID = "ou_same_id_across_channels"
	providers := []string{"feishu", "dingtalk", "wecom"}
	recipients := []manager.HumanRecipient{{Scope: "Web", Ref: service.SelectionRef{
		Kind: "user", Provider: "web", WorkspaceID: workspace, AgentID: agent, ExternalID: owner,
	}}}
	for _, provider := range providers {
		ref := service.SelectionRef{Kind: "user", Provider: provider, WorkspaceID: workspace, AgentID: agent,
			BindingID: service.NewID(), TenantID: "tenant-" + provider, Namespace: "user_id", Generation: 1, IdentityEpoch: 1, ExternalID: externalID}
		if provider == "feishu" {
			ref.Namespace = "open_id"
		}
		reviewExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,
 config_revision,enabled,app_id,external_tenant_id,bot_open_id,credential_ciphertext,trigger,history,access,identity_verified_generation)
 VALUES($1,$2,$3,$4,1,1,1,true,$5,$5,$5,decode('00','hex'),'{"mention":true}','{"enabled":false}','{"mode":"all"}',1)`,
			ref.BindingID, workspace, agent, provider, ref.TenantID)
		if provider == "wecom" {
			verification := service.NewID()
			reviewExec(t, db, `INSERT INTO platform_wecom_verifications(id,binding_id,identity_epoch,connection_generation,search_user_id,
 created_by,code_hash,code_ciphertext,status,callback_user_id,expires_at)
 VALUES($1::uuid,$2,1,1,'wecom-search',$3,$1::uuid::text,decode('','hex'),'verified',$4,now())`, verification, ref.BindingID, owner, externalID)
			reviewExec(t, db, `INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id)
 VALUES($1,1,1,'wecom-search',$2,$3)`, ref.BindingID, externalID, verification)
		} else {
			reviewExec(t, db, `INSERT INTO platform_channel_directory_state(binding_id,identity_epoch,connection_generation,external_tenant_id,full_snapshot,expires_at)
 VALUES($1,1,1,$2,true,now()+interval '1 hour')`, ref.BindingID, ref.TenantID)
			reviewExec(t, db, `INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known)
 VALUES($1,$2,$3,'active','[]',true)`, ref.BindingID, externalID, provider)
		}
		recipients = append(recipients, manager.HumanRecipient{Scope: provider, IsDefault: provider == "wecom", Ref: ref})
	}
	rules, err := store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Recipients: recipients})
	if err != nil || len(rules.Recipients) != 4 || !rules.Recipients[3].IsDefault {
		t.Fatal(rules, err)
	}
	identities := map[string]bool{}
	for _, recipient := range rules.Recipients {
		if recipient.ID == "" || identities[recipient.ID] {
			t.Fatalf("duplicate candidate ID: %+v", recipient)
		}
		identities[recipient.ID] = true
	}
	forged := rules.Recipients[3]
	forged.Ref.ExternalID = "wecom-search"
	_, err = store.HumanRecipients(ctx, workspace, owner, agent, &manager.HumanRecipients{Version: rules.Version, Recipients: []manager.HumanRecipient{forged}})
	assertChannelError(t, err, "human_recipient_forbidden")

	start := func(t *testing.T, session, input string) manager.HumanRuntimeScope {
		t.Helper()
		assignment, turn := service.NewID(), service.NewID()
		reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)
 SELECT $1,$2,$3,$4,COALESCE(max(turn_no),0)+1,$5,1,'running' FROM manager_turns WHERE session_id=$3`, turn, workspace, session, input, assignment)
		reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1`, session, turn, engine, assignment)
		return manager.HumanRuntimeScope{InputID: input, Identity: manager.DelegationIdentity{SessionID: session, EngineID: engine, AssignmentID: assignment, Generation: 1}}
	}
	receive := func(t *testing.T, ref service.SelectionRef, text string, quote ...string) manager.ChannelReceipt {
		t.Helper()
		v := inboundFixture(ref.BindingID, service.NewID(), ref.ExternalID)
		v.ExternalTenantID, v.SenderTenant, v.SenderIDType, v.BotID, v.Text = ref.TenantID, ref.TenantID, ref.Namespace, ref.TenantID, text
		if len(quote) > 0 {
			v.ReplyMessageID = quote[0]
		}
		inbox, err := store.ReceiveChannel(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
			t.Fatal(err)
		}
		return inbox
	}
	openChat := func(t *testing.T, ref service.SelectionRef, text string, quote ...string) manager.HumanRuntimeScope {
		t.Helper()
		inbox := receive(t, ref, text, quote...)
		var session, input string
		if err := db.QueryRow(`SELECT i.session_id,i.id FROM manager_inputs i JOIN manager_channel_inbox n ON n.input_id=i.id WHERE n.id=$1`, inbox.ID).Scan(&session, &input); err != nil {
			t.Fatal(err)
		}
		return start(t, session, input)
	}
	deliverAgentReply := func(t *testing.T, chat manager.HumanRuntimeScope, reply, messageID string) {
		t.Helper()
		session, input := chat.Identity.SessionID, chat.InputID
		reviewExec(t, db, `WITH next AS (UPDATE manager_sessions SET last_event_seq=last_event_seq+1 WHERE id=$1 RETURNING last_event_seq)
 INSERT INTO manager_session_events(workspace_id,session_id,seq,event) SELECT $2,$1,last_event_seq,
 jsonb_build_object('type','agent.message','input_id',$3::text,'content',jsonb_build_array(jsonb_build_object('type','text','text',$4::text))) FROM next`, session, workspace, input, reply)
		reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE input_id=$1`, input)
		reviewExec(t, db, `UPDATE manager_inputs SET status='processed' WHERE id=$1`, input)
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, session)
		if err := store.MaterializeChannelReplies(ctx); err != nil {
			t.Fatal(err)
		}
		var count int
		var kind string
		if err := db.QueryRow(`SELECT count(*),COALESCE(min(d.kind),'') FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id WHERE n.input_id=$1`, input).Scan(&count, &kind); err != nil || count != 1 || kind != "reply" {
			t.Fatal("Agent response did not use a single normal reply delivery", count, kind, err)
		}
		deliveries, err := store.ClaimChannelDeliveries(ctx, 5, true)
		if err != nil || len(deliveries) != 1 || deliveries[0].Text != reply {
			t.Fatal("expected the Agent's text in the send queue", deliveries, err)
		}
		d := deliveries[0]
		if d, err = store.AuthorizeChannelDelivery(ctx, d.ID, d.LeaseToken); err != nil || !d.Allowed {
			t.Fatal("Agent reply not authorized", err)
		}
		receipt := manager.DeliveryCompletion{Status: "sent", LeaseToken: d.LeaseToken, TargetDigest: d.TargetDigest, EvidenceID: messageID}
		switch d.Provider {
		case "feishu":
			receipt.ProviderMessageID, receipt.EvidenceType = messageID, "send_response"
		case "wecom":
			receipt.ProviderRequestID, receipt.ProviderSessionID, receipt.EvidenceType = messageID, "reply-socket", "business_ack"
		case "dingtalk":
			receipt.Status, receipt.ProviderRequestID, receipt.EvidenceType = "accepted", messageID, "send_response"
			if err = store.CompleteChannelDelivery(ctx, d.ID, receipt); err != nil {
				t.Fatal(err)
			}
			reviewExec(t, db, `UPDATE manager_channel_deliveries SET next_query_at=now() WHERE id=$1`, d.ID)
			queries, err := store.ClaimChannelDeliveryQueries(ctx, 5)
			if err != nil || len(queries) != 1 || queries[0].ID != d.ID {
				t.Fatal("reply receipt query not available", queries, err)
			}
			receipt.Status, receipt.EvidenceType, receipt.QueryToken = "sent", "status_query", queries[0].QueryToken
		}
		if err := store.CompleteChannelDelivery(ctx, d.ID, receipt); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := sessions.CreateConversation(ctx, workspace, owner, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	var priorChannelChat manager.HumanRuntimeScope
	for _, recipient := range rules.Recipients[1:] {
		t.Run(recipient.Ref.Provider, func(t *testing.T) {
			create := manager.CreateSession{ConversationID: conversation.ID, Mode: "task"}
			create.Input = &struct {
				Text string `json:"text"`
			}{Text: "Ask a channel recipient"}
			task, _, err := sessions.CreateSession(ctx, workspace, owner, "confirm-"+recipient.Ref.Provider, create)
			if err != nil {
				t.Fatal(err)
			}
			var input string
			if err = db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND seq=1`, task.ID).Scan(&input); err != nil {
				t.Fatal(err)
			}
			required := true
			created, err := store.HumanTaskTool(ctx, start(t, task.ID, input), "request", "", &manager.HumanQuestion{
				CandidateID: recipient.ID, RulesVersion: rules.Version, QuestionScope: recipient.Scope, Background: "有一笔费用待确认",
				Goal: "确认费用与日期", KnownFacts: []manager.HumanFact{},
				Questions: []manager.HumanQuestionItem{
					{ID: "amount", Text: "确认费用？", AnswerType: "number", Required: &required},
					{ID: "date", Text: "哪天开始？", AnswerType: "text", Required: &required},
				},
				CompletionCriteria: []string{"金额与日期齐备"}, ProposedAction: "核准计划", ImpactScope: "项目计划", IfNotConfirmed: "保持等待",
			})
			if err != nil {
				t.Fatal(err)
			}
			request := created.(manager.HumanRequest)
			reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE input_id=$1`, input)
			reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
			if items, e := store.ClaimHumanNotices(ctx, 10, false); e != nil || len(items) != 0 {
				t.Fatalf("notice before safe fence count=%d err=%v", len(items), e)
			}
			reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, request.ID)
			verifyHumanNotice(t, db, store, recipient.Ref, request.ID)
			_, err = store.RecoverHumanRequest(ctx, workspace, owner, agent, request.ID, manager.HumanRecoveryAction{Version: 1, Action: "retry", Reason: "不能重复发送已经送达的通知"})
			assertChannelError(t, err, "human_recovery_blocked")

			// An assignee can open a confirmation-only conversation before
			// ever using the employee's normal private chat.
			reviewExec(t, db, `UPDATE platform_channel_bindings SET access=jsonb_build_object('mode','all','deny_ids',jsonb_build_array($2::text)) WHERE id=$1`, recipient.Ref.BindingID, externalID)
			first := receive(t, recipient.Ref, "我来处理待确认事项")
			var admitted string
			if err = db.QueryRow(`SELECT n.input_id FROM manager_channel_inbox n JOIN manager_human_chat_inputs h ON h.input_id=n.input_id WHERE n.id=$1 AND manager_human_chat_input_allowed(n.input_id)`, first.ID).Scan(&admitted); err != nil {
				t.Fatal("confirmation-only first input was not admitted", err)
			}
			reviewExec(t, db, `UPDATE manager_inputs SET status='processed' WHERE id=$1`, admitted)
			reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, recipient.Ref.BindingID)

			if priorChannelChat.InputID != "" {
				pending, err := store.HumanChatTool(ctx, priorChannelChat, "pending", "", nil)
				if err != nil {
					assertChannelError(t, err, "resource_not_found")
				} else if len(pending.(map[string]any)["items"].([]manager.HumanRequest)) != 0 {
					t.Fatal("same external ID from another channel accessed the request", pending)
				}
			}
			answer := manager.HumanAnswer{Version: 1, Decision: "approve"}
			if _, err = store.AnswerHumanRequest(ctx, workspace, owner, request.ID, answer); err == nil {
				t.Fatal("Web actor accessed an external request")
			}
			other := recipient.Ref
			other.ExternalID = "ou_other_user"
			wrong := openChat(t, other, "查看当前任务")
			identity := wrong.Identity
			config, _, err := store.HarnessConfig(ctx, identity.EngineID, identity.SessionID, identity.AssignmentID, identity.Generation, wrong.InputID)
			if err != nil || config.HumanTools || !config.ChatCatalogPrivate || !config.ChatCatalogChannel {
				t.Fatal("unconfigured private sender must have only the structural confirmation catalog, never callable authority", err)
			}
			if _, err = store.HumanChatTool(ctx, wrong, "answer", request.ID, &answer); err == nil {
				t.Fatal("another authenticated sender approved the request")
			}
			chat := openChat(t, recipient.Ref, "查看当前任务")
			identity = chat.Identity
			config, _, err = store.HarnessConfig(ctx, identity.EngineID, identity.SessionID, identity.AssignmentID, identity.Generation, chat.InputID)
			if err != nil || !config.HumanTools {
				t.Fatal("confirmation tools unavailable in private chat", err)
			}
			pending, err := store.HumanChatTool(ctx, chat, "pending", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			items := pending.(map[string]any)["items"].([]manager.HumanRequest)
			if len(items) != 1 || items[0].ID != request.ID {
				t.Fatal(items)
			}
			current := items[0]
			answer.QuestionDigest = current.QuestionDigest
			answer.Association = "unclear"
			if _, err = store.HumanChatTool(ctx, chat, "propose", request.ID, &answer); err == nil {
				t.Fatal("an unclear reply was accepted")
			}
			const followup = "请确认按 500 元、周五开始执行吗？"
			clarification := manager.HumanAnswer{Version: 1, QuestionDigest: current.QuestionDigest, Decision: "needs_more", Association: "current_message", ClarificationText: followup}
			if _, err = store.HumanChatTool(ctx, chat, "propose", request.ID, &clarification); err != nil {
				t.Fatal(err)
			}
			var requestState string
			if err = db.QueryRow(`SELECT state FROM manager_human_requests WHERE id=$1 AND resume_input_id IS NULL`, request.ID).Scan(&requestState); err != nil || requestState != "pending" {
				t.Fatal("clarification unexpectedly resumed the task", requestState, err)
			}
			deliverAgentReply(t, chat, followup, "om-clarification")
			priorChannelChat = chat
			reviewExec(t, db, `UPDATE platform_channel_bindings SET access=jsonb_build_object('mode','all','deny_ids',jsonb_build_array($2::text)) WHERE id=$1`, recipient.Ref.BindingID, externalID)
			if _, err = store.HumanChatTool(ctx, chat, "propose", request.ID, &answer); err == nil {
				t.Fatal("ordinary chat from before revocation still accepted tools")
			}
			var quoted []string
			if recipient.Ref.Provider == "feishu" {
				quoted = []string{"om-clarification"}
			}
			chat = openChat(t, recipient.Ref, "同意", quoted...)
			identity = chat.Identity
			config, _, err = store.HarnessConfig(ctx, identity.EngineID, identity.SessionID, identity.AssignmentID, identity.Generation, chat.InputID)
			if err != nil || !config.HumanTools || !config.ConfirmationOnly || !config.ChatCatalogPrivate || !config.ChatCatalogChannel {
				t.Fatal("restricted confirmation harness unavailable", config.ConfirmationOnly, err)
			}
			if _, err := store.DelegationSource(ctx, chat.Identity); err == nil {
				t.Fatal("restricted confirmation got ordinary delegation access")
			}
			answer.Association = "clarification"
			incomplete, err := store.HumanChatTool(ctx, chat, "propose", request.ID, &answer)
			if err != nil || incomplete.(map[string]any)["state"] != "needs_more" {
				t.Fatal("mandatory answers were not enforced", incomplete, err)
			}
			answer.Answers = []manager.HumanAnswerItem{{QuestionID: "amount", Value: "500"}, {QuestionID: "date", Value: "周五"}}
			lookup, err := store.HumanChatSubmission(ctx, chat, request.ID, "approve-1", nil)
			if err != nil || lookup.(map[string]any)["state"] != "unknown" {
				t.Fatal("unexpected receipt before submit", lookup, err)
			}
			info := answer
			info.Decision = "information"
			missingDecision, err := store.HumanChatSubmission(ctx, chat, request.ID, "information-1", &info)
			if err != nil || missingDecision.(map[string]any)["state"] != "needs_more" ||
				missingDecision.(map[string]any)["reason"] != "explicit_decision_required" {
				t.Fatal("missing explicit approval was not surfaced", missingDecision, err)
			}
			stale := answer
			stale.Version++
			if _, err = store.HumanChatSubmission(ctx, chat, request.ID, "stale-1", &stale); err != nil {
				t.Fatal(err)
			}
			lookup, err = store.HumanChatSubmission(ctx, chat, request.ID, "stale-1", nil)
			if err != nil || lookup.(map[string]any)["state"] != "not_recorded" ||
				lookup.(map[string]any)["reason"] != "human_request_changed" {
				t.Fatal("no-effect receipt missing", lookup, err)
			}
			for range 2 {
				result, err := store.HumanChatSubmission(ctx, chat, request.ID, "approve-1", &answer)
				if err != nil || result.(map[string]any)["state"] != "approved" {
					t.Fatal("semantic answer was not accepted", result, err)
				}
			}
			assertHumanCardRefreshAfterText(t, db, store, request.ID, recipient.Ref.Provider)
			lookup, err = store.HumanChatSubmission(ctx, chat, request.ID, "approve-1", nil)
			if err != nil || lookup.(map[string]any)["continuation"].(map[string]string)["state"] != "queued" {
				t.Fatal("committed approval could not be reconciled", lookup, err)
			}
			if _, err = store.HumanChatSubmission(ctx, chat, request.ID, "approve-1", &info); err == nil {
				t.Fatal("same operation key accepted different content")
			}
			var evidence, digest string
			if err := db.QueryRow(`SELECT answer_input_id,continuation_context->>'questionDigest' FROM manager_human_requests WHERE id=$1`, request.ID).
				Scan(&evidence, &digest); err != nil || evidence != chat.InputID || digest != answer.QuestionDigest {
				t.Fatal("restricted answer did not retain its input evidence and question digest", evidence, digest, err)
			}
			var allowed bool
			if err = db.QueryRow(`SELECT manager_input_admission_allowed(resume_input_id) FROM manager_human_requests WHERE id=$1`, request.ID).Scan(&allowed); err != nil || !allowed {
				t.Fatal("task continuation failed", allowed, err)
			}
			deliverAgentReply(t, chat, "已收到同意，原任务已排队继续。", "om-confirmation-result")
			reviewExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all"}' WHERE id=$1`, recipient.Ref.BindingID)
		})
	}
	t.Run("wecom_text_approval_wins_over_old_reject_button", func(t *testing.T) {
		recipient := rules.Recipients[3]
		create := manager.CreateSession{ConversationID: conversation.ID, Mode: "task"}
		create.Input = &struct {
			Text string `json:"text"`
		}{Text: "Prepare sample approval"}
		task, _, err := sessions.CreateSession(ctx, workspace, owner, "confirm-wecom-text-first", create)
		if err != nil {
			t.Fatal(err)
		}
		var input string
		if err = db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND seq=1`, task.ID).Scan(&input); err != nil {
			t.Fatal(err)
		}
		created, err := store.HumanTaskTool(ctx, start(t, task.ID, input), "request", "", &manager.HumanQuestion{
			RecipientID: recipient.ID, RulesVersion: rules.Version, Question: "是否同意演练？", AnswerType: "decision",
		})
		if err != nil {
			t.Fatal(err)
		}
		request := created.(manager.HumanRequest)
		reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now() WHERE input_id=$1`, input)
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, task.ID)
		reviewExec(t, db, `UPDATE manager_human_requests SET state='pending',fenced_at=now() WHERE id=$1`, request.ID)
		verifyHumanNotice(t, db, store, recipient.Ref, request.ID)
		var notice string
		if err = db.QueryRow(`SELECT id FROM manager_human_notices WHERE request_id=$1 AND status='sent' AND receipt->>'human_confirmation_card'='true'`, request.ID).Scan(&notice); err != nil {
			t.Fatal(err)
		}
		chat := openChat(t, recipient.Ref, "我同意这次演练")
		list, err := store.HumanChatTool(ctx, chat, "pending", request.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		pending := list.(map[string]any)["items"].([]manager.HumanRequest)
		if len(pending) != 1 {
			t.Fatal("missing pending approval")
		}
		answer := manager.HumanAnswer{Version: request.Version, Decision: "approve", Association: "current_message", QuestionDigest: pending[0].QuestionDigest}
		result, err := store.HumanChatSubmission(ctx, chat, request.ID, "approve-wecom-text-first", &answer)
		if err != nil || result.(map[string]any)["state"] != "approved" {
			t.Fatal("text approval was not recorded", result, err)
		}
		click := manager.HumanCardAction{BindingID: recipient.Ref.BindingID, Provider: "wecom", ExternalTenant: recipient.Ref.TenantID, ExternalActor: recipient.Ref.ExternalID,
			Generation: 1, Revision: 1, ProviderCardID: notice, Decision: "reject"}
		for range 2 {
			got, e := store.AnswerHumanCard(ctx, notice, click)
			if e != nil || got.(map[string]any)["state"] != "approved" || !got.(map[string]any)["card"].(*manager.HumanConfirmationCard).DecisionRequired {
				t.Fatal("late rejection did not return stored approval state", got, e)
			}
		}
		var decision string
		var stopped, continuations int
		if err = db.QueryRow(`SELECT r.answer->>'decision',s.stop_revision,
 (SELECT count(*) FROM manager_inputs i WHERE i.human_request_id=r.id)
 FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id WHERE r.id=$1`, request.ID).Scan(&decision, &stopped, &continuations); err != nil || decision != "approve" || stopped != 0 || continuations != 1 {
			t.Fatalf("late rejection changed the task: decision=%q stops=%d resumes=%d %v", decision, stopped, continuations, err)
		}
	})
}

func testHumanClarificationStaysPending(t *testing.T, ctx context.Context, store *managerrepo.Store, workspace, actor, id string) {
	t.Helper()
	clarification := manager.HumanAnswer{Version: 1, Decision: "needs_more", Text: "请补上当月统计口径"}
	for range 2 {
		got, err := store.AnswerHumanRequest(ctx, workspace, actor, id, clarification)
		if err != nil || got.(map[string]string)["state"] != "needs_more" {
			t.Fatal(got, err)
		}
	}
	var state string
	var entries int
	var resume *string
	if err := store.DB.QueryRow(`SELECT state,resume_input_id,(SELECT count(*) FROM manager_human_clarifications WHERE request_id=$1) FROM manager_human_requests WHERE id=$1`, id).Scan(&state, &resume, &entries); err != nil || state != "pending" || resume != nil || entries != 1 {
		t.Fatal(state, resume, entries, err)
	}
}
func testHumanRevisionReceipt(t *testing.T, ctx context.Context, store *managerrepo.Store, workspace, actor, id string, answer manager.HumanAnswer) {
	t.Helper()
	for range 2 {
		result, err := store.AnswerHumanRequest(ctx, workspace, actor, id, answer)
		if err != nil || result.(map[string]string)["state"] != "revised" {
			t.Fatal(result, err)
		}
	}
	var inputs int
	if err := store.DB.QueryRow(`SELECT count(*) FROM manager_inputs WHERE human_request_id=$1`, id).Scan(&inputs); err != nil || inputs != 1 {
		t.Fatal(inputs, err)
	}
	if _, err := store.AnswerHumanRequest(ctx, workspace, actor, id, manager.HumanAnswer{Version: 1, Decision: "approve"}); err == nil {
		t.Fatal("revision was overwritten by approval")
	}
}
