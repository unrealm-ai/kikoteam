package repository

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	pr "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestHostedAssistantProvisioningBothWorkspacePaths(t *testing.T) {
	ctx := t.Context()
	db := workspaceTestDB(t)
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
	secrets := ps.Secrets{Root: []byte("assistant-test-root-key-at-least-32-bytes")}
	store := &HostedAssistantStore{DB: db, EnsureKey: secrets.EnsureKey}
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db)}
	makeWorkspace := func(name string) service.Workspace {
		t.Helper()
		out, err := ws.Create(ctx, owner, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := makeWorkspace("No assistant")
	off, err := store.ReadAdmin(ctx, owner)
	if err != nil || off.Enabled {
		t.Fatal(off, err)
	}
	on := true
	if _, err = store.SaveAdmin(ctx, owner, service.AssistantSettingsPatch{ExpectedVersion: off.Version, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	manual := makeWorkspace("Manual")
	newOwner, personal := service.NewID(), service.NewID()
	auth := &AuthStore{DB: db, InitCredits: pr.Signup}
	if err = auth.RegisterPassword(ctx, service.Account{ID: newOwner, Email: "assistant-new@example.test", Name: "New Owner", PasswordHash: "fixture"}, service.Workspace{ID: personal, Name: "Personal"}, "synthetic-session-hash", time.Now().Add(time.Hour)); err != nil {
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
	if v, err := store.Entry(ctx, before.ID, owner); err != nil || v.AgentID != "" {
		t.Fatal("existing workspace got assistant", v, err)
	}
	for w, user := range map[string]string{manual.ID: owner, personal: newOwner} {
		entry, err := store.Entry(ctx, w, user)
		if err != nil || entry.AgentID == "" || len(entry.Cards) != 3 || entry.Experienced {
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
		var creator, payer string
		if err = db.QueryRow(`SELECT a.creator_account_id,k.payer_account_id FROM platform_agents a
 JOIN platform_agent_model_keys k ON k.agent_id=a.id WHERE a.id=$1 AND a.platform_model_id=$2`, entry.AgentID, model).Scan(&creator, &payer); err != nil || creator != user || payer != user {
			t.Fatal(creator, payer, err)
		}
		out, err := (&service.PackageService{Store: NewPackageStore(db)}).List(ctx, w, user, entry.AgentID, 1, 20)
		if err != nil || len(out.Items) != 1 || out.Items[0].AssistantToolSet != "demo" {
			t.Fatal(out, err)
		}
		for _, p := range out.Items {
			if !p.ConversationScope.Private.Enabled || p.ConversationScope.Group.Enabled || p.TriggerScope.Enabled == nil || *p.TriggerScope.Enabled {
				t.Fatal(p)
			}
		}
	}
	member := service.NewID()
	insertWorkspaceAccount(t, db, member, "assistant-member@example.test")
	if _, err = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, manual.ID, member); err != nil {
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
	original := store.EnsureKey
	store.EnsureKey = func(context.Context, *sql.Tx, string, string) error { return errors.New("test retry") }
	if _, err = store.ProvisionNext(ctx); err == nil {
		t.Fatal("failed key creation did not cause retry")
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM platform_agents WHERE workspace_id=$1", retry.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial assistant created", count, err)
	}
	store.EnsureKey = original
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
	if v, err := store.Entry(ctx, noModel.ID, owner); err != nil || v.AgentID != "" || v.Pending {
		t.Fatal("should skip rather than fall back to a workspace model", v, err)
	}
	if _, err = store.ReadAdmin(ctx, member); err == nil {
		t.Fatal("ordinary member may inspect platform switch")
	}

}
