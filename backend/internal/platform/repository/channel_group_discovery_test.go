package repository

import (
	"context"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

// Provider is represented by an in-memory verified page; no Feishu account is contacted.
func TestFeishuCandidateCacheScopeAndFences(t *testing.T) {
	db, _, _, binding, actor, _ := directoryFixture(t)
	ctx := context.Background()
	cache := &ChannelGroupCache{DB: db}
	store := &managerrepo.Store{DB: db}
	page := service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_verified", Name: "Verified group"}}}
	got, err := cache.PublishGroups(ctx, binding, actor, page)
	if err != nil || len(got.Items) != 1 {
		t.Fatal(got, err)
	}
	candidate := got.Items[0].ID
	if candidate == "oc_verified" {
		t.Fatal("provider ID exposed as trusted candidate")
	}
	target, err := store.ResolveTriggerGroupCandidate(ctx, binding.WorkspaceID, actor, binding.AgentID, manager.TriggerGroupResolve{Provider: "feishu", CandidateID: candidate})
	if err != nil {
		t.Fatal(err)
	}
	var correct bool
	if err = db.QueryRow(`SELECT c.system_owned AND c.created_by IS NULL AND c.channel_created_by IS NULL AND c.channel_tenant_id=$2 AND c.channel_binding_id=$3 AND c.external_chat_id='oc_verified' AND s.system_owned FROM manager_conversations c JOIN manager_sessions s ON s.conversation_id=c.id WHERE c.id=$1`, target.ConversationID, binding.TenantID, binding.ID).Scan(&correct); err != nil || !correct {
		t.Fatal(correct, err)
	}
	ws, err := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(ctx, actor, "Other candidate workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct{ workspace, agent, provider, id string }{{ws.ID, binding.AgentID, "feishu", candidate}, {binding.WorkspaceID, service.NewID(), "feishu", candidate}, {binding.WorkspaceID, binding.AgentID, "wecom", candidate}, {binding.WorkspaceID, binding.AgentID, "feishu", service.NewID()}} {
		if _, err = store.ResolveTriggerGroupCandidate(ctx, q.workspace, actor, q.agent, manager.TriggerGroupResolve{Provider: q.provider, CandidateID: q.id}); err == nil {
			t.Fatal("foreign/forged candidate resolved", q)
		}
	}
	for _, column := range []string{"connection_generation", "config_revision", "identity_epoch"} {
		reviewExec(t, db, `UPDATE platform_channel_bindings SET `+column+`=`+column+`+1 WHERE id=$1`, binding.ID)
		if _, err = cache.PublishGroups(ctx, binding, actor, page); err == nil {
			t.Fatal("stale provider fetch published", column)
		}
		if _, err = store.ResolveTriggerGroupCandidate(ctx, binding.WorkspaceID, actor, binding.AgentID, manager.TriggerGroupResolve{Provider: "feishu", CandidateID: candidate}); err == nil {
			t.Fatal("stale candidate resolved", column)
		}
		reviewExec(t, db, `UPDATE platform_channel_bindings SET `+column+`=`+column+`-1 WHERE id=$1`, binding.ID)
	}
	reviewExec(t, db, `UPDATE platform_channel_group_candidates SET expires_at=now()-interval '1 second' WHERE id=$1`, candidate)
	if _, err = store.ResolveTriggerGroupCandidate(ctx, binding.WorkspaceID, actor, binding.AgentID, manager.TriggerGroupResolve{Provider: "feishu", CandidateID: candidate}); err == nil {
		t.Fatal("expired candidate resolved")
	}
	refreshed, err := cache.PublishGroups(ctx, binding, actor, page)
	if err != nil || refreshed.Items[0].ID != candidate {
		t.Fatal("refresh lost candidate identity", refreshed, err)
	}
	reviewExec(t, db, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, binding.WorkspaceID, actor)
	if _, err = cache.PublishGroups(ctx, binding, actor, page); err == nil {
		t.Fatal("revoked admin published delayed fetch")
	}
	var count int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_inputs)+(SELECT count(*) FROM manager_channel_inbox)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("candidate fabricated input", count, err)
	}
}
