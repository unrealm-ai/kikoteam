package repository

import (
	"context"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"sync"
	"testing"
)

func lifecycleFixture(binding, event, kind string, at int64) manager.ChannelLifecycle {
	return manager.ChannelLifecycle{BindingID: binding, Generation: 1, Revision: 1, TenantID: "tenant", ChatID: "group", EventID: event, EventType: kind, OccurredAtMS: at, MemberIDs: []string{}}
}
func TestManagerGroupLifecycleOrderingAndIsolation(t *testing.T) {
	db, _, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	send := func(v manager.ChannelLifecycle) {
		t.Helper()
		if _, err := s.ReceiveChannelLifecycle(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	state := func(want string) {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT state FROM manager_channel_groups WHERE binding_id=$1`, binding).Scan(&got); err != nil || got != want {
			t.Fatalf("state %s want %s: %v", got, want, err)
		}
	}
	added := lifecycleFixture(binding, "add", "bot_added", 1000)
	send(added)
	state("pending")
	duplicate, err := s.ReceiveChannelLifecycle(ctx, added)
	if err != nil || duplicate.Status != "duplicate" {
		t.Fatal(duplicate, err)
	}
	added.ChatID = "other"
	_, err = s.ReceiveChannelLifecycle(ctx, added)
	assertChannelError(t, err, "event_conflict")
	jobs, err := s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	send(lifecycleFixture(binding, "leave", "bot_removed", 2000))
	yes := true
	err = s.CompleteChannelGroupCheck(ctx, jobs[0].ID, manager.ChannelGroupCompletion{LeaseToken: jobs[0].LeaseToken, Present: &yes})
	assertChannelError(t, err, "lease_lost")
	jobs, err = s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	no := false
	if err = s.CompleteChannelGroupCheck(ctx, jobs[0].ID, manager.ChannelGroupCompletion{LeaseToken: jobs[0].LeaseToken, Present: &no}); err != nil {
		t.Fatal(err)
	}
	state("absent")
	send(lifecycleFixture(binding, "old-add", "bot_added", 1500))
	state("absent")
	bad := lifecycleFixture(binding, "other-tenant", "bot_added", 2500)
	bad.TenantID = "other"
	_, err = s.ReceiveChannelLifecycle(ctx, bad)
	assertChannelError(t, err, "binding_generation_stale")
	bad.TenantID = "tenant"
	bad.Generation = 2
	_, err = s.ReceiveChannelLifecycle(ctx, bad)
	assertChannelError(t, err, "binding_generation_stale")
	send(lifecycleFixture(binding, "disband", "chat_disbanded", 1800))
	state("disbanded")
	send(lifecycleFixture(binding, "readd", "bot_added", 3000))
	state("disbanded")
	jobs, err = s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}
func TestManagerGroupMemberEventsDoNotRemoveBot(t *testing.T) {
	db, _, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := lifecycleFixture(binding, "member-leave", "members_removed", 2000)
	v.MemberIDs = []string{"alice", "alice"}
	if _, err := s.ReceiveChannelLifecycle(ctx, v); err != nil {
		t.Fatal(err)
	}
	var groups int
	if err := db.QueryRow(`SELECT count(*) FROM manager_channel_groups`).Scan(&groups); err != nil || groups != 0 {
		t.Fatal(groups, err)
	}
	allowed := func(member string, want bool) {
		t.Helper()
		var got bool
		if err := db.QueryRow(`SELECT manager_channel_group_allowed($1,1,'tenant','group',$2)`, binding, member).Scan(&got); err != nil || got != want {
			t.Fatalf("%s allowed=%v: %v", member, got, err)
		}
	}
	allowed("alice", false)
	allowed("bob", true)
	v.EventID = "old-member-add"
	v.EventType = "members_added"
	v.OccurredAtMS = 1000
	if _, err := s.ReceiveChannelLifecycle(ctx, v); err != nil {
		t.Fatal(err)
	}
	allowed("alice", false)
	v.EventID = "equal-member-add"
	v.OccurredAtMS = 2000
	if _, err := s.ReceiveChannelLifecycle(ctx, v); err != nil {
		t.Fatal(err)
	}
	allowed("alice", false)
	v.EventID = "new-member-add"
	v.OccurredAtMS = 3000
	if _, err := s.ReceiveChannelLifecycle(ctx, v); err != nil {
		t.Fatal(err)
	}
	allowed("alice", true)
}
func TestManagerGroupVerificationRetryAndMessageRecovery(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := s.ReceiveChannelLifecycle(ctx, lifecycleFixture(binding, "leave", "bot_removed", 1000)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err = s.CompleteChannelGroupCheck(ctx, jobs[0].ID, manager.ChannelGroupCompletion{LeaseToken: jobs[0].LeaseToken}); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("retry should wait", jobs, err)
	}
	if _, err = db.Exec(`UPDATE manager_channel_groups SET retry_at=now()`); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	no := false
	if err = s.CompleteChannelGroupCheck(ctx, jobs[0].ID, manager.ChannelGroupCompletion{LeaseToken: jobs[0].LeaseToken, Present: &no}); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "new-message", "alice")
	v.ChatType = "group"
	v.ExternalChatID = "group"
	v.CreatedAtMS = 2000
	v.BotMentioned = true
	if _, err = s.ReceiveChannel(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_channel_inbox WHERE provider_event_id='new-message'`).Scan(&status); err != nil || status != "pending" {
		t.Fatal(status, err)
	}
	jobs, err = s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	yes := true
	if err = s.CompleteChannelGroupCheck(ctx, jobs[0].ID, manager.ChannelGroupCompletion{LeaseToken: jobs[0].LeaseToken, Present: &yes}); err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT status FROM manager_channel_inbox WHERE provider_event_id='new-message'`).Scan(&status); err != nil || status != "accepted" {
		t.Fatal(status, err)
	}
}

func TestManagerGroupRemovalCancelsQueuedDeliveries(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	v := inboundFixture(binding, "before-leave", "alice")
	v.ChatType = "group"
	v.ExternalChatID = "group"
	v.CreatedAtMS = 1000
	v.BotMentioned = true
	receipt, err := s.ReceiveChannel(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	deliveryID := manager.NewID()
	if _, err = db.Exec(`INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES($1,$2,$3,1,'fixed','notice')`, deliveryID, receipt.ID, binding); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.ClaimChannelDeliveries(ctx, 1, true)
	if err != nil || len(deliveries) != 1 {
		t.Fatal(deliveries, err)
	}
	if _, err = s.ReceiveChannelLifecycle(ctx, lifecycleFixture(binding, "leave", "bot_removed", 2000)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_channel_deliveries WHERE id=$1`, deliveryID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatal(status, err)
	}
	if _, err = s.AuthorizeChannelDelivery(ctx, deliveryID, deliveries[0].LeaseToken); err == nil {
		t.Fatal("revoked queued send authorized")
	}
	checks, err := s.ClaimChannelGroupChecks(ctx)
	if err != nil || len(checks) != 1 {
		t.Fatal(checks, err)
	}
	yes := true
	if err = s.CompleteChannelGroupCheck(ctx, checks[0].ID, manager.ChannelGroupCompletion{LeaseToken: checks[0].LeaseToken, Present: &yes}); err != nil {
		t.Fatal(err)
	}
	var inputAllowed bool
	if err = db.QueryRow(`SELECT manager_input_actor_allowed(input_id) FROM manager_channel_inbox WHERE id=$1`, receipt.ID).Scan(&inputAllowed); err != nil || inputAllowed {
		t.Fatalf("old accepted input regained authority: %v %v", inputAllowed, err)
	}
	// Late materialization from a pre-removal input also cannot be delivered.
	late := manager.NewID()
	if _, err = db.Exec(`INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES($1,$2,$3,1,'reply','old response')`, late, receipt.ID, binding); err != nil {
		t.Fatal(err)
	}
	deliveries, err = s.ClaimChannelDeliveries(ctx, 1, true)
	if err != nil || len(deliveries) != 1 {
		t.Fatal(deliveries, err)
	}
	_, err = s.AuthorizeChannelDelivery(ctx, late, deliveries[0].LeaseToken)
	assertChannelError(t, err, "access_revoked")
}

func TestManagerGroupLifecycleConcurrentDuplicates(t *testing.T) {
	db, _, s, _, _, _, binding := channelManagerFixture(t)
	db.SetMaxOpenConns(4) // Exercise contention without an unbounded burst of sockets.
	v := lifecycleFixture(binding, "concurrent", "bot_removed", 1000)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.ReceiveChannelLifecycle(context.Background(), v); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM manager_channel_group_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM manager_channel_groups`).Scan(&version); err != nil || version != 1 {
		t.Fatal(version, err)
	}
}
