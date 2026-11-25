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

type feishuGroupListFunc func(context.Context, string, string, string, string) (service.ChannelGroupPage, error)

func (f feishuGroupListFunc) List(ctx context.Context, app, secret, tenant, cursor string) (service.ChannelGroupPage, error) {
	return f(ctx, app, secret, tenant, cursor)
}

func TestFeishuLiveGroupDiscoveryNoCacheAndObservedFallback(t *testing.T) {
	db, channels, _, b, actor, _ := directoryFixture(t)
	ctx := t.Context()
	reviewExec(t, db, `INSERT INTO platform_channel_observed_chats(binding_id,identity_epoch,external_tenant_id,external_chat_id,display_name)
 VALUES($1,$2,$3,'oc_observed','Historical group')`, b.ID, b.IdentityEpoch, b.TenantID)
	cache := &ChannelGroupCache{DB: db}
	var providerPage service.ChannelGroupPage
	var providerErr error
	d := &service.ChannelGroupDiscovery{Channels: channels, Store: cache, Secret: []byte("test-secret-long-enough-for-proofs"), Provider: feishuGroupListFunc(func(context.Context, string, string, string, string) (service.ChannelGroupPage, error) {
		return providerPage, providerErr
	})}
	selector := NewGroupSelectorStore(db)
	providerPage = service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_live", Name: "Live group"}}, NextCursor: "next"}
	out, err := d.Discover(ctx, b.WorkspaceID, actor, b.AgentID, "")
	if err != nil || out.Source != "provider" || out.NextCursor != "next" || len(out.Items) != 1 || out.Items[0].SelectionToken == "" {
		t.Fatal(out, err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM platform_channel_group_candidates WHERE binding_id=$1`, b.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("provider page was cached", n, err)
	}
	token := out.Items[0].SelectionToken
	if _, err = d.Select(ctx, b.WorkspaceID, actor, b.AgentID, token+"x"); err == nil {
		t.Fatal("accepted forged proof")
	}
	if _, err = d.Select(ctx, b.WorkspaceID, service.NewID(), b.AgentID, token); err == nil {
		t.Fatal("accepted another actor's proof")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision+1 WHERE id=$1`, b.ID)
	if _, err = d.Select(ctx, b.WorkspaceID, actor, b.AgentID, token); err == nil {
		t.Fatal("accepted pre-change proof")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision-1 WHERE id=$1`, b.ID)
	pick, err := d.Select(ctx, b.WorkspaceID, actor, b.AgentID, token)
	if err != nil || len(pick.Items) != 1 || pick.Items[0].Selection == nil || pick.Items[0].Selection.Ref.ExternalID != "oc_live" {
		t.Fatal(pick, err)
	}
	refs := []service.SelectionRef{pick.Items[0].Selection.Ref}
	if _, err = selector.ResolveGroups(ctx, b.WorkspaceID, actor, b.AgentID, refs); err != nil {
		t.Fatal(err)
	}
	providerPage = service.ChannelGroupPage{}
	out, err = d.Discover(ctx, b.WorkspaceID, actor, b.AgentID, "")
	if err != nil || out.Source != "provider" || len(out.Items) != 0 {
		t.Fatal("successful empty list fallback", out, err)
	}
	providerErr = service.Fail("provider_down", 503, "fixture")
	out, err = d.Discover(ctx, b.WorkspaceID, actor, b.AgentID, "")
	if err != nil || out.Source != "observed" || len(out.Items) != 0 {
		t.Fatal("failure did not request fallback", out, err)
	}
	groups, _, err := selector.SearchGroups(ctx, b.WorkspaceID, actor, b.AgentID, service.GroupSelectionQuery{Provider: "feishu", Limit: 20}, service.GroupSelectionBoundary{})
	if err != nil || len(groups) != 1 || groups[0].Ref.ExternalID != "oc_observed" {
		t.Fatal("fallback mixed cached or unobserved results", groups, err)
	}
	store := &managerrepo.Store{DB: db}
	if _, err = store.ResolveTriggerGroupCandidate(ctx, b.WorkspaceID, actor, b.AgentID, manager.TriggerGroupResolve{Provider: "feishu", CandidateID: groups[0].Ref.ID}); err != nil {
		t.Fatal("observed fallback could not resolve a trigger target", err)
	}
	reviewExec(t, db, `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, b.WorkspaceID, actor)
	if _, err = d.Discover(ctx, b.WorkspaceID, actor, b.AgentID, ""); err == nil {
		t.Fatal("fallback accepted revoked access")
	}
}
