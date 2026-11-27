package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestAgentUsageDefaultCASAndAuthorization(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, owner)
	agents := &service.AgentService{Store: NewAgentStore(db)}
	a, err := agents.Create(ctx, w, owner, service.AgentCreate{Name: "Usage employee", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	api := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	v, err := api.Get(ctx, w, owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	access := v.UsageConfig.Access
	if access.Mode != "all" || !access.GroupMembersEnabled || len(access.Allow) != 0 || len(access.Deny) != 0 || v.Version != 1 {
		t.Fatal(v)
	}
	if _, err = api.Get(ctx, w, service.NewID(), a.ID); err == nil {
		t.Fatal("nonmember read policy")
	}
	if _, err = api.Update(ctx, w, service.NewID(), a.ID, v); err == nil {
		t.Fatal("nonmember wrote policy")
	}
	v.UsageConfig.Trigger.Keywords = []service.UsagePattern{{Text: "help", Channels: []string{"web", "feishu"}}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := api.Update(ctx, w, owner, a.ID, v); results <- e }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("CAS winners", successes)
	}
	got, err := api.Get(ctx, w, owner, a.ID)
	if err != nil || got.Version != 2 {
		t.Fatal(got, err)
	}
	var admission, version, config int64
	if err = db.QueryRow(`SELECT admission_epoch,version,config_version FROM platform_agents WHERE id=$1`, a.ID).Scan(&admission, &version, &config); err != nil || admission != 1 || version != 2 || config != 1 {
		t.Fatal(admission, version, config, err)
	}
	got.UsageConfig.Access.GroupMembersEnabled = !got.UsageConfig.Access.GroupMembersEnabled
	got, err = api.Update(ctx, w, owner, a.ID, got)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT admission_epoch FROM platform_agents WHERE id=$1`, a.ID).Scan(&admission); err != nil || admission != 2 {
		t.Fatal("access change did not fence", admission, err)
	}
	got.UsageConfig.Access.Deny = append(got.UsageConfig.Access.Deny, service.UsageSelector{Channel: "web", Kind: "user", ID: service.NewID()})
	if _, err = api.Update(ctx, w, owner, a.ID, got); err == nil {
		t.Fatal("unknown workspace member accepted")
	}
}

func TestAgentUsageExternalSelectorsPinnedAndStaleRetained(t *testing.T) {
	db, _, _, w, owner, agent, binding := channelManagerFixture(t)
	ctx := context.Background()
	api := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	var epoch int64
	if err := db.QueryRow(`SELECT identity_epoch FROM platform_channel_bindings WHERE id=$1`, binding).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_channel_directory_state(binding_id,identity_epoch,connection_generation,external_tenant_id,full_snapshot,expires_at) VALUES($1,$2,1,'tenant',true,now()+interval '1 hour');`, binding, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_channel_directory_users(binding_id,user_id,display_name,status,department_ids,memberships_known) VALUES($1,'ou_known','Known','active','[]',true)`, binding); err != nil {
		t.Fatal(err)
	}
	v, err := api.Get(ctx, w, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	rule := service.UsageSelector{Channel: "feishu", Kind: "user", ID: "ou_known", BindingID: binding, IdentityEpoch: epoch}
	v.UsageConfig.Access.Mode = "allowlist"
	v.UsageConfig.Access.Allow = []service.UsageSelector{rule}
	saved, err := api.Update(ctx, w, owner, agent, v)
	if err != nil {
		t.Fatal(err)
	}
	var yes bool
	if err = db.QueryRow(`SELECT access->'allow_ids' ? 'ou_known' FROM platform_channel_bindings WHERE id=$1`, binding).Scan(&yes); err != nil || !yes {
		t.Fatal("projection missing", yes, err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	saved, err = api.Update(ctx, w, owner, agent, saved)
	if err != nil {
		t.Fatal("stale display cannot be retained", err)
	}
	if err = db.QueryRow(`SELECT access->'allow_ids' ? 'ou_known' FROM platform_channel_bindings WHERE id=$1`, binding).Scan(&yes); err != nil || yes {
		t.Fatal("stale identity grant resurrected", yes, err)
	}
	saved.UsageConfig.Access.Allow[0].IdentityEpoch++
	if _, err = api.Update(ctx, w, owner, agent, saved); err == nil {
		t.Fatal("old directory granted new app epoch")
	}
	saved.UsageConfig.Access.Allow[0] = rule
	saved.UsageConfig.Access.Allow[0].BindingID = service.NewID()
	if _, err = api.Update(ctx, w, owner, agent, saved); err == nil {
		t.Fatal("foreign binding accepted")
	}
}

func TestAgentUsageAdminRevocationSerializesWithWrite(t *testing.T) {
	db, _, _, w, owner, agent, _ := channelManagerFixture(t)
	// This sender's role is the only grant; the creator has an independent grant.
	assignDifferentTestCreator(t, db, w, agent)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := &AgentUsageStore{DB: db}
	v, err := store.ReadUsage(ctx, w, owner, agent)
	if err != nil {
		t.Fatal(err)
	}
	// A privilege change already in flight must finish before write authorization.
	revoke, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoke.Rollback() }() //nolint:errcheck // Explicit commit owns success.
	if _, err = revoke.ExecContext(ctx, `UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, owner); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, e := store.ReplaceUsage(ctx, w, owner, agent, v); result <- e }()
	select {
	case e := <-result:
		t.Fatalf("write did not wait for pending revocation: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if err = revoke.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("revoked admin wrote usage")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var version int64
	if err = db.QueryRowContext(ctx, `SELECT usage_version FROM platform_agents WHERE id=$1`, agent).Scan(&version); err != nil || version != v.Version {
		t.Fatal("unauthorized write changed version", version, err)
	}
	// Conversely, once an authorized operation owns the member SHARE lock,
	// revocation cannot commit in its gap between authorization and agent write.
	if _, err = db.ExecContext(ctx, `UPDATE platform_members SET role='owner' WHERE workspace_id=$1 AND user_id=$2`, w, owner); err != nil {
		t.Fatal(err)
	}
	authorized, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = authorized.Rollback() }() //nolint:errcheck // Read-only test transaction.
	if err = LockEmployeeManagement(ctx, authorized, w, agent, owner); err != nil {
		t.Fatal(err)
	}
	competing, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = competing.Rollback() }() //nolint:errcheck // Intentionally aborted lock-timeout transaction.
	if _, err = competing.ExecContext(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err = competing.ExecContext(ctx, `UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, owner); err == nil {
		t.Fatal("revocation crossed authorized write lock")
	}
}
