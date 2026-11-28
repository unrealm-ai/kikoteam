package repository

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pr "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestHostedAssistantProvisioningBothWorkspacePaths(t *testing.T) {
	ctx := t.Context()
	db := workspaceTestDBThrough(t, "000111_platform_default_images.sql")
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "assistant-admin@example.test")
	if _, err := db.Exec("UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", owner); err != nil {
		t.Fatal(err)
	}
	model := service.NewID()
	if _, err := db.Exec(`INSERT INTO platform_proxy_models(id,name,protocol,base_url,upstream_model_id,credential_ciphertext,
 input_price_units,output_price_units,enabled,is_default,validated,version,tested_at,updated_by)
 VALUES($1,'Hosted Model','chat_completions','https://example.test','test','',1,1,true,true,true,1,now(),$2)`, model, owner); err != nil {
		t.Fatal(err)
	}
	store := &HostedAssistantStore{DB: db}
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db)}
	makeWorkspace := func(name string) service.Workspace {
		t.Helper()
		out, err := ws.Create(ctx, owner, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := makeWorkspace("Existing without queue")
	reviewExec(t, db, `UPDATE platform_assistant_settings SET enabled=true WHERE singleton`)
	oldPending := makeWorkspace("Existing with legacy queue")
	applyTestMigrations(t, ctx, db, "000111_platform_default_images.sql", "")
	manual := makeWorkspace("Manual")
	newOwner, personal := service.NewID(), service.NewID()
	auth := &AuthStore{DB: db, InitCredits: pr.Signup}
	if err := auth.RegisterPassword(ctx, service.Account{ID: newOwner, Email: "assistant-new@example.test", Name: "New Owner", PasswordHash: "fixture"}, service.Workspace{ID: personal, Name: "Personal"}, "synthetic-session-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := store.ProvisionNext(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, ws := range []string{before.ID, oldPending.ID} {
		if v, err := store.Entry(ctx, ws, owner); err != nil || v.AgentID != "" || v.Pending {
			t.Fatal("existing workspace got assistant", v, err)
		}
	}
	for w, user := range map[string]string{manual.ID: owner, personal: newOwner} {
		entry, err := store.Entry(ctx, w, user)
		if err != nil || entry.AgentID == "" || len(entry.Cards) != 3 {
			t.Fatal(entry, err)
		}
		agents := &service.AgentService{Store: NewAgentStore(db)}
		before, err := agents.Get(ctx, w, user, entry.AgentID)
		if err != nil || !before.IsAssistant {
			t.Fatal(before, err)
		}
		avatar := "custom-avatar"
		_, err = agents.Update(ctx, w, user, entry.AgentID, service.AgentPatch{Version: before.Version, Avatar: &avatar})
		var denied *service.Error
		if !errors.As(err, &denied) || denied.Code != "assistant_avatar_locked" {
			t.Fatal(err)
		}
		var creator string
		var follows bool
		if err = db.QueryRow(`SELECT creator_account_id,follows_default_model FROM platform_agents WHERE id=$1`, entry.AgentID).Scan(&creator, &follows); err != nil || creator != user || !follows || !before.ModelAvailable {
			t.Fatal(creator, follows, before.ModelAvailable, err)
		}
		out, err := (&service.PackageService{Store: NewPackageStore(db)}).List(ctx, w, user, entry.AgentID, 1, 20)
		if err != nil || len(out.Items) != 2 {
			t.Fatal(out, err)
		}
		var demoCount int
		for _, p := range out.Items {
			if p.AssistantToolSet == "" {
				assertDefaultEmployeePackage(t, p, entry.AgentID)
				continue
			}
			demoCount++
			if p.AssistantToolSet != "demo" || !p.ConversationScope.Private.Enabled || p.ConversationScope.Group.Enabled || p.TriggerScope.Enabled == nil || *p.TriggerScope.Enabled {
				t.Fatal(p)
			}
		}
		if demoCount != 1 {
			t.Fatal("expected exactly one built-in demo package", out)
		}
	}
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "assistant-member@example.test")
	if _, err := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, manual.ID, member); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Entry(ctx, manual.ID, member)
	if err != nil || entry.AgentID == "" || len(entry.Cards) != 3 {
		t.Fatal(entry, err)
	}
	if _, err = db.Exec(`UPDATE platform_capability_packages SET conversation_scope=jsonb_set(jsonb_set(conversation_scope,'{private,enabled}','false'),'{group,enabled}','true')
 WHERE agent_id=$1 AND assistant_tool_set='demo'`, entry.AgentID); err != nil {
		t.Fatal(err)
	}
	entry, err = store.Entry(ctx, manual.ID, member)
	if err != nil || entry.AgentID == "" || len(entry.Cards) != 0 {
		t.Fatal("group-only must not show demo cards", entry, err)
	}
	retry := makeWorkspace("Retry")
	reviewExec(t, db, `ALTER TABLE platform_agents ADD CONSTRAINT assistant_qa_retry CHECK (name <> 'KIKO 助手') NOT VALID`)
	if _, err = store.ProvisionNext(ctx); err == nil {
		t.Fatal("failed provisioning did not cause retry")
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM platform_agents WHERE workspace_id=$1", retry.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial assistant created", count, err)
	}
	reviewExec(t, db, `ALTER TABLE platform_agents DROP CONSTRAINT assistant_qa_retry`)
	if _, err = db.Exec("UPDATE platform_assistant_provisioning SET next_attempt_at=now() WHERE workspace_id=$1", retry.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ProvisionNext(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if _, err = db.Exec("UPDATE platform_proxy_models SET enabled=false,is_default=false,validated=false WHERE id=$1", model); err != nil {
		t.Fatal(err)
	}
	noModel := makeWorkspace("Without usable platform default")
	if worked, err := store.ProvisionNext(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	v, err := store.Entry(ctx, noModel.ID, owner)
	if err != nil || v.AgentID == "" || v.Pending || len(v.Cards) != 3 {
		t.Fatal("model-less workspace should have an assistant and demo cards", v, err)
	}
	agents := &service.AgentService{Store: NewAgentStore(db)}
	got, err := agents.Get(ctx, noModel.ID, owner, v.AgentID)
	if err != nil || got.ModelAvailable || !got.IsAssistant {
		t.Fatal(got, err)
	}
	name := "My assistant"
	if _, err := agents.Update(ctx, noModel.ID, owner, v.AgentID, service.AgentPatch{Version: got.Version, Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.Create(ctx, noModel.ID, owner, service.AgentCreate{Name: "Ordinary employee"}); err == nil {
		t.Fatal("ordinary employee may not be created without a model")
	}
}

func TestHostedAssistantModelAdmissionAndRetry(t *testing.T) {
	f := modelProxyFixture(t)
	c := proxyBrowserClient(t)
	owner, w := f.register(t, c, "assistant-setup@example.test")
	if worked, err := f.assistant.ProvisionNext(t.Context()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	entry, err := f.assistant.Entry(t.Context(), w, owner)
	if err != nil || entry.AgentID == "" || len(entry.Cards) != 3 {
		t.Fatal(entry, err)
	}
	base := "/workspaces/" + w
	conversation := f.api(t, c, "POST", base+"/conversations/web-direct", map[string]string{"agent_id": entry.AgentID}, "", 200)
	body := map[string]any{"conversation_id": conversation["id"], "mode": "chat", "input": map[string]string{"text": entry.Cards[0].Input}}
	key := service.NewID()
	assertMissing := func(response map[string]any) {
		t.Helper()
		if !strings.Contains(fmt.Sprint(response), "assistant_model_required") {
			t.Fatal(response)
		}
	}
	assertMissing(f.api(t, c, "POST", base+"/sessions", body, key, 422))
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM manager_sessions WHERE agent_id=$1`, entry.AgentID).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected message started a session", count, err)
	}
	official := f.createModel(t, c, "chat_completions", true)
	local := service.NewID()
	reviewExec(t, f.db, `INSERT INTO platform_model_profiles
 (id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,enabled,is_default,tested_at)
 VALUES($1,$2,'Local default','chat_completions','https://example.test/v1','local',1,true,true,'',1,'unknown',true,true,now())`, local, w)
	if v, err := f.assistant.Entry(t.Context(), w, owner); err != nil || v.AgentID != entry.AgentID || len(v.Cards) != len(entry.Cards) {
		t.Fatal("configuration must not hide the home cards or recreate the assistant", v, err)
	}
	chat := f.api(t, c, "POST", base+"/sessions", body, key, 201)["id"].(string)
	entryResponse := f.api(t, c, "GET", base+"/assistant", nil, "", 200)
	cards, ok := entryResponse["cards"].([]any)
	if !ok || len(cards) != len(entry.Cards) || entryResponse["agent_id"] != entry.AgentID {
		t.Fatal("an existing chat must not hide the home cards", entryResponse)
	}
	if _, present := entryResponse["experienced"]; present {
		t.Fatal("entry must not expose the removed completion marker")
	}
	var selected string
	if err := f.db.QueryRow(`SELECT configuration->>'model_profile_id' FROM manager_sessions WHERE id=$1`, chat).Scan(&selected); err != nil || selected != local {
		t.Fatal("workspace default should win", selected, err)
	}
	reviewExec(t, f.db, `UPDATE platform_model_profiles SET enabled=false,is_default=false WHERE id=$1`, local)
	reviewExec(t, f.db, `UPDATE platform_proxy_models SET enabled=false,is_default=false WHERE id=$1`, official)
	newBody := map[string]string{"text": "Retry after configuration"}
	assertMissing(f.api(t, c, "POST", base+"/sessions/"+chat+"/inputs", newBody, service.NewID(), 422))
	if err := f.db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE session_id=$1`, chat).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected message was queued", count, err)
	}

	// This fixture has no executor: mark the initial request settled before
	// retrying in the same chat, where platform default is now the fallback.
	reviewExec(t, f.db, `UPDATE manager_inputs SET status='canceled' WHERE session_id=$1`, chat)
	reviewExec(t, f.db, `UPDATE platform_proxy_models SET enabled=true,is_default=true WHERE id=$1`, official)
	f.api(t, c, "POST", base+"/sessions/"+chat+"/inputs", newBody, service.NewID(), 201)
	var payer string
	if err := f.db.QueryRow(`SELECT s.configuration->>'platform_model_id', k.payer_account_id
 FROM manager_sessions s JOIN platform_agent_model_keys k ON k.agent_id=s.agent_id WHERE s.id=$1`, chat).Scan(&selected, &payer); err != nil || selected != official || payer != owner {
		t.Fatal("platform fallback must bill the workspace owner", selected, payer, err)
	}
}
