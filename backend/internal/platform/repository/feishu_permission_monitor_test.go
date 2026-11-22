package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestFeishuPermissionMigrationReconcilesExistingAccess(t *testing.T) {
	db, models, workspace, actor := modelFixtureThrough(t, "000056_human_requests.sql")
	model := saveTestModel(t, models, workspace, actor)
	agent, binding := service.NewID(), service.NewID()
	if _, err := db.Exec(`INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch,usage_config)
		VALUES($1,$2,'existing QR',$3,1,1,1,'{"access":{"mode":"allowlist","group_members_enabled":true}}')`, agent, workspace, model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,enabled,connection_generation,rules_version,config_revision,app_id,credential_ciphertext,trigger,history,access)
		VALUES($1,$2,$3,'feishu',true,2,3,4,'cli_fixture',''::bytea,'{"mention":false}','{"enabled":true,"limit":55}','{"mode":"all"}')`, binding, workspace, agent); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000057_channel_permission_observations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var changed bool
	err = db.QueryRow(`SELECT b.access->>'mode'='allowlist' AND (b.access->>'group_members_enabled')::boolean
		AND b.config_revision=5 AND b.rules_version=3 AND b.history->>'limit'='55' AND b.trigger->>'mention'='false'
		AND c.connection_generation=2 FROM platform_channel_bindings b JOIN platform_feishu_permission_checks c ON c.binding_id=b.id
		WHERE b.id=$1`, binding).Scan(&changed)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
}

func TestFeishuPermissionObservationsFollowCredentialGeneration(t *testing.T) {
	_, channels, store, binding, actor, _ := directoryFixture(t)
	ctx := context.Background()
	provider := &setupProvider{ready: true}
	setup := service.FeishuSetupService{Channels: channels, Provider: provider}
	// The fixture has a different bot/tenant. Read-only evidence for an
	// unexpected installation must not make permissions ready.
	if worked, err := setup.CheckNextBinding(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	read := func() service.ChannelBinding {
		t.Helper()
		value, err := channels.ObservedProvider(ctx, binding.WorkspaceID, actor, binding.AgentID, "feishu")
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if observed := read(); observed.PermissionDiagnostics == nil || observed.PermissionDiagnostics.Ready ||
		observed.PermissionDiagnostics.ErrorCode != "feishu_identity_mismatch" || len(observed.PermissionGroups) == 0 {
		t.Fatal("current permission diagnostic was not published", observed.PermissionDiagnostics)
	}
	if _, err := store.DB.Exec(`UPDATE platform_feishu_permission_checks SET next_check_at=now() WHERE binding_id=$1`, binding.ID); err != nil {
		t.Fatal(err)
	}
	oldClaim, err := store.ClaimFeishuPermissionCheck(ctx)
	if err != nil || oldClaim == nil {
		t.Fatal(oldClaim, err)
	}
	current := read()
	secret := "rotated-synthetic-secret"
	if _, err = channels.PutProvider(ctx, binding.WorkspaceID, actor, binding.AgentID, "feishu",
		service.ChannelPut{Version: current.Revision, AppID: current.AppID, AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}}); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishFeishuPermissionCheck(ctx, *oldClaim, service.FeishuDiagnostics{Ready: true}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.PermissionDiagnostics != nil || got.Generation == oldClaim.Binding.Generation {
		t.Fatal("old credential evidence survived rotation", got.PermissionDiagnostics)
	}
	if worked, err := setup.CheckNextBinding(ctx); err != nil || !worked || read().PermissionDiagnostics == nil {
		t.Fatal(worked, err)
	}
}
