package repository

import (
	"context"
	"encoding/json"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWebGroupRuntimeScopeDoesNotGrantPrivateOrRevokedAccess(t *testing.T) {
	db, m, store, w, actor, employee, group := webMessageFixture(t)
	ctx := context.Background()
	policy := service.DefaultAgentUsage("")
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE platform_agents SET usage_config=$2 WHERE id=$1`, employee, raw)
	msg, _, err := m.PostWebGroupMessage(ctx, w, actor, group, "runtime-scope", manager.WebGroupPost{Text: "do task", MentionAgentIDs: []string{employee}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessWebGroupInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var conversation, session, input string
	if err = db.QueryRow(`SELECT s.conversation_id,i.session_id,i.id FROM manager_web_group_inbox n JOIN manager_inputs i ON i.id=n.input_id JOIN manager_sessions s ON s.id=i.session_id WHERE n.message_id=$1`, msg.ID).Scan(&conversation, &session, &input); err != nil {
		t.Fatal(err)
	}
	turn := service.NewID()
	exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')`, turn, w, session, input, service.NewID())
	exec(`UPDATE manager_sessions SET active_turn_id=$2 WHERE id=$1`, session, turn)
	exec(`UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1`, input, turn)
	identity := service.WebGroupExecution{ConversationID: conversation, InputID: input}
	check := func(g service.WebGroupExecution, a, emp string, running bool) error {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only test transaction.
		return (&modelTx{tx: tx, workspace: w}).AuthorizeWebGroupExecution(ctx, g, a, emp, running)
	}
	if err = check(identity, actor, employee, true); err != nil {
		t.Fatal("group bypass failed", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e := (&modelTx{tx: tx, workspace: w}).AuthorizeWebEmployee(ctx, actor, employee); e == nil {
		t.Fatal("group granted private access")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for name, g := range map[string]service.WebGroupExecution{"missing input": {ConversationID: conversation}, "other input": {ConversationID: conversation, InputID: service.NewID()}, "other conversation": {ConversationID: service.NewID(), InputID: input}} {
		t.Run(name, func(t *testing.T) {
			if check(g, actor, employee, true) == nil {
				t.Fatal("spoofed provenance accepted")
			}
		})
	}
	if check(identity, service.NewID(), employee, true) == nil || check(identity, actor, service.NewID(), true) == nil {
		t.Fatal("identity substitution accepted")
	}
	// Snapshot is the only input-free path and still requires real group access.
	packages := &service.PackageService{Store: NewPackageStore(db)}
	disableDefaultEmployeePackage(t, packages, w, actor, employee)
	rules := service.PackageConversationScope{
		Private: service.PackageConversationRule{Mode: "inherit_with_blacklist"},
		Group: service.PackageConversationRule{Mode: "allowlist", Enabled: true, Allow: []service.UsageSelector{
			{Channel: "web", Kind: "group", ID: group},
			{Channel: "web", Kind: "user", ID: actor},
		}},
	}
	pack, err := packages.Create(ctx, w, actor, employee, service.PackageCreate{Name: "Web group scoped", ConversationScope: &rules})
	if err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotCapabilities(service.WithWebGroupExecution(ctx, service.WebGroupExecution{ConversationID: conversation}), tx, w, actor, employee)
	if err != nil || len(snapshot.Packages) != 1 || snapshot.Packages[0].ID != pack.ID {
		t.Fatal("real group and actor did not authorize scoped package", err, snapshot.Packages)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run := service.WithWebGroupExecution(ctx, identity)
	runtime := &service.RuntimeService{Packages: packages}
	if got, e := runtime.Effective(run, w, actor, employee, &snapshot); e != nil || len(got) != 1 {
		t.Fatal("scoped package unavailable on its running group Input", e, got)
	}
	rules.Group.Enabled = false
	if _, e := packages.Update(ctx, w, actor, employee, pack.ID, service.PackagePatch{Version: pack.Version, ConversationScope: &rules}); e != nil {
		t.Fatal(e)
	}
	if got, e := runtime.Effective(run, w, actor, employee, &snapshot); e != nil || len(got) != 0 {
		t.Fatal("disabled group scene survived on a pinned package", e, got)
	}
	for _, change := range []struct {
		name, q, restore string
		args             []any
	}{
		{"left group", `UPDATE platform_web_group_members SET status='removed' WHERE group_id=$1 AND user_id=$2`, `UPDATE platform_web_group_members SET status='active' WHERE group_id=$1 AND user_id=$2`, []any{group, actor}},
		{"stopped", `UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1`, `UPDATE manager_sessions SET stop_revision=applied_stop_revision WHERE id=$1`, []any{session}},
		{"queued", `UPDATE manager_inputs SET status='queued' WHERE id=$1`, `UPDATE manager_inputs SET status='started' WHERE id=$1`, []any{input}},
		{"deny", `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,deny}',jsonb_build_array(jsonb_build_object('channel','web','kind','user','id',$2::text))) WHERE id=$1`, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,deny}','[]') WHERE id=$1 AND $2::text IS NOT NULL`, []any{employee, actor}},
	} {
		t.Run(change.name, func(t *testing.T) {
			exec(change.q, change.args...)
			defer exec(change.restore, change.args...)
			if check(identity, actor, employee, true) == nil {
				t.Fatal("revoked runtime allowed")
			}
		})
	}
	// Removing/re-adding the employee gives it a new group admission epoch.
	// Even snapshot admission without an Input must reject the old conversation.
	exec(`UPDATE platform_web_group_agents SET admission_epoch=admission_epoch+1 WHERE group_id=$1 AND agent_id=$2`, group, employee)
	if check(service.WebGroupExecution{ConversationID: conversation}, actor, employee, false) == nil {
		t.Fatal("stale conversation created snapshot after employee re-add")
	}

}
