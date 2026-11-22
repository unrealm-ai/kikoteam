package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestEmployeeChatModelSelectionAcrossIngress(t *testing.T) {
	for _, scenario := range []struct {
		name, fallback string
		disabled       bool
	}{
		{name: "without_workspace_default"},
		{name: "ignores_different_workspace_default", fallback: "unrelated"},
		{name: "disabled_employee_model_does_not_fallback", fallback: "unrelated", disabled: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, m, mux, workspace, owner, agent, cookie := managerFixture(t)
			var selected string
			if err := db.QueryRow(`SELECT model_profile_id FROM platform_agents WHERE id=$1`, agent).Scan(&selected); err != nil {
				t.Fatal(err)
			}
			reviewExec(t, db, `UPDATE platform_model_profiles SET is_default=false,enabled=$2 WHERE id=$1`, selected, !scenario.disabled)
			if scenario.fallback != "" {
				reviewExec(t, db, `INSERT INTO platform_model_profiles
 (id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,enabled,is_default,tested_at)
 SELECT $1,p.workspace_id,$2,p.protocol,p.base_url,'other-model',1,p.has_key,true,p.credential,1,p.vision_status,true,true,now()
 FROM platform_model_profiles p WHERE p.id=$3`, manager.NewID(), scenario.fallback, selected)
			}
			t.Run("web", func(t *testing.T) {
				conversation, err := m.ResolveWebConversation(t.Context(), workspace, owner, agent)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+workspace+"/sessions",
					strings.NewReader(`{"mode":"chat","conversation_id":"`+conversation.ID+`","input":{"text":"fixture"}}`))
				request.Header.Set("Cookie", cookie)
				request.Header.Set("X-CSRF-Protection", "1")
				request.Header.Set("Idempotency-Key", "employee-chat-web")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if scenario.disabled {
					if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "configuration_unusable") {
						t.Fatalf("expected employee model refusal, got %d %s", response.Code, response.Body)
					}
					assertNoChatSession(t, db, conversation.ID)
					return
				}
				var session manager.Session
				if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || response.Code != http.StatusCreated {
					t.Fatalf("create Web chat: %d %s (%v)", response.Code, response.Body, err)
				}
				if session.Configuration.ModelProfileID != selected || session.InitialInputID == nil {
					t.Fatalf("unexpected Web chat model or input: %s, %v", session.Configuration.ModelProfileID, session.InitialInputID)
				}
			})
			for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
				t.Run(provider, func(t *testing.T) {
					receipt := submitEmployeeChatChannelFixture(t, db, m, workspace, owner, agent, provider)
					var status, code, model, conversation string
					var input bool
					if err := db.QueryRow(`SELECT n.status,COALESCE(n.error_code,''),n.input_id IS NOT NULL,n.conversation_id,
 COALESCE(s.configuration->>'model_profile_id','')
 FROM manager_channel_inbox n LEFT JOIN manager_sessions s ON s.conversation_id=n.conversation_id AND s.mode='chat'
 WHERE n.id=$1`, receipt.ID).Scan(&status, &code, &input, &conversation, &model); err != nil {
						t.Fatal(err)
					}
					if scenario.disabled {
						if status != "rejected" || code != "configuration_unusable" || input || model != "" {
							t.Fatalf("unusable model fallback: %s %s input=%v model=%s", status, code, input, model)
						}
						assertNoChatSession(t, db, conversation)
						var deliveries int
						if err := db.QueryRow(`SELECT count(*) FROM manager_channel_deliveries WHERE inbox_id=$1 AND status='pending' AND text='此 AI 员工的模型配置当前不可用，请联系管理员检查员工模型设置。'`, receipt.ID).Scan(&deliveries); err != nil || deliveries != 1 {
							t.Fatalf("expected exactly one queued failure reply: %d %v", deliveries, err)
						}
					} else if status != "accepted" || code != "" || !input || model != selected {
						t.Fatalf("employee model not selected: %s %s input=%v model=%s want=%s", status, code, input, model, selected)
					}
				})
			}
		})
	}
}

func assertNoChatSession(t *testing.T, db *sql.DB, conversation string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM manager_sessions WHERE conversation_id=$1`, conversation).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejection left a Session: %d %v", count, err)
	}
}

func submitEmployeeChatChannelFixture(t *testing.T, db *sql.DB, m *manager.Service, workspace, owner, agent, provider string) manager.ChannelReceipt {
	t.Helper()
	ctx := context.Background()
	api := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte("provider-test-secret-32-bytes-long!")}
	secret := "synthetic-fixture-secret"
	app := "model-fixture-" + provider
	if provider == "feishu" {
		app = "cli_model_fixture"
	}
	binding, err := api.PutProvider(ctx, workspace, owner, agent, provider, service.ChannelPut{AppID: app, AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "all"}})
	if err != nil {
		t.Fatal(err)
	}
	event := inboundFixture(binding.ID, "employee-model", "fixture-sender")
	event.ConnectionGeneration, event.ConfigRevision, event.RulesVersion = binding.Generation, binding.Revision, binding.RulesVersion
	switch provider {
	case "feishu":
		event.SenderIDType = "open_id"
		err = api.Apply(ctx, binding.ID, service.ChannelApplied{Generation: binding.Generation, Revision: binding.Revision, RulesVersion: binding.RulesVersion, Status: "connected", ObservedTenantID: event.ExternalTenantID, ObservedBotID: "fixture-bot"})
		if err != nil {
			t.Fatal(err)
		}
	case "dingtalk":
		event.SenderIDType, event.BotID, event.SenderTenant = "user_id", "fixture-robot", event.ExternalTenantID
	case "wecom":
		event.SenderIDType, event.BotID, event.ExternalTenantID, event.ProviderDirected = "user_id", binding.AppID, binding.AppID, true
	}
	store := m.Store.(*managerrepo.Store)
	receipt, err := store.ReceiveChannel(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	return receipt
}
