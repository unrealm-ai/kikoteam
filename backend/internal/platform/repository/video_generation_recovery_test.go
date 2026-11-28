package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	mr "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/videogen"
)

func TestVideoGenerationAdmissionAndRecovery(t *testing.T) {
	ctx := t.Context()
	store, _, workspace, actor, employee, trigger, credential := triggerFixture(t)
	db := store.DB
	resources := runtimeCapabilityServices(t, db)
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(ctx, db, catalog, resources.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	var submissions atomic.Int32
	var unknown atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			submissions.Add(1)
			if unknown.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
		}
		if err := json.NewEncoder(w).Encode(map[string]string{"id": "job-test", "status": "failed"}); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	svc := videogen.Service{Store: repo.Store{DB: db}, Secrets: mp.Secrets{Root: []byte("abcdefghijklmnopqrstuvwxyz123456")}, Root: t.TempDir(), HTTP: upstream.Client(), Notify: mr.NotifyVideo}
	reviewExec(t, db, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", actor)
	plugin := service.CatalogID("plugin", videogen.Key)
	cipher, err := svc.Secrets.Seal("video-provider", plugin, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `INSERT INTO platform_video_generation_configs(plugin_id,version,base_url,upstream_model_id,credential_ciphertext,unit_price_units,max_concurrent_generations,workspace_daily_seconds,created_by)VALUES($1,1,$2,'MiniMax-H3',$3,2000000,1,5,$4)`, plugin, upstream.URL, cipher, actor)
	reviewExec(t, db, "UPDATE platform_default_plugin_settings SET enabled=true,version=1,active_config_version=1 WHERE plugin_id=$1", plugin)
	store.SnapshotCapabilities = SnapshotCapabilities
	task, err := signedTrigger(t, store, trigger, credential, manager.TriggerCommand{Version: 1, EventID: "video-scope", Action: "create_task", Description: "generate video"})
	if err != nil {
		t.Fatal(err)
	}
	id := manager.DelegationIdentity{SessionID: task.SessionID, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if err = store.RegisterEngine(ctx, manager.Engine{ID: id.EngineID, HostID: "video-scope", Endpoint: "http://127.0.0.1:1", Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	turn, input := service.NewID(), task.InputID
	reviewExec(t, db, "INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status)VALUES($1,$2,$3,$4,1,$5,1,'running')", turn, workspace, task.SessionID, input, id.AssignmentID)
	reviewExec(t, db, "UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1", input, turn)
	reviewExec(t, db, "UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1", task.SessionID, turn, id.EngineID, id.AssignmentID)
	cfg, _, err := store.HarnessConfig(ctx, id.EngineID, task.SessionID, id.AssignmentID, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	pins := cfg.Configuration.Capabilities.DefaultPlugins
	if len(pins) != 1 || pins[0].Handler != "video_generation" {
		t.Fatal("missing video pin", pins)
	}
	scope := videogen.Scope{DelegationIdentity: id, Input: input, Version: pins[0].VersionID}
	args := videogen.Arguments{Prompt: "A single leaf", Ratio: "portrait", Seconds: 4, Key: "original"}
	wantCode := func(callErr error, code string) {
		t.Helper()
		var failure *service.Error
		if !errors.As(callErr, &failure) || failure.Code != code {
			t.Fatalf("want %s, got %v", code, callErr)
		}
	}
	_, err = svc.Run(ctx, scope, args)
	wantCode(err, "payer_binding_required")
	agents := service.AgentService{Store: &AgentStore{DB: db, EnsureKey: svc.Secrets.EnsureKey}}
	if _, err = agents.BindPayer(ctx, workspace, actor, employee, 1); err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, "UPDATE platform_credit_settings SET signup_bonus_units=1000000 WHERE id=1")
	if err = svc.Transaction(ctx, func(tx *sql.Tx) error { return repo.Signup(ctx, tx, actor) }); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Run(ctx, scope, args)
	if err != nil || first.Status != "queued" {
		t.Fatal(first, err)
	}
	changed := args
	changed.Seconds = 3
	_, err = svc.Run(ctx, scope, changed)
	wantCode(err, "idempotency_conflict")
	another := args
	another.Key = "second"
	_, err = svc.Run(ctx, scope, another)
	wantCode(err, "concurrency_limit_reached")
	version := int64(1)
	limit := 2
	price := "5"
	if _, err = svc.Save(ctx, actor, videogen.Patch{Version: &version, Concurrent: &limit, Price: &price}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Run(ctx, scope, another)
	wantCode(err, "daily_limit_reached")
	again, err := svc.Run(ctx, scope, args)
	if err != nil || again.ID != first.ID || again.Price != "2.000000" || submissions.Load() != 1 {
		t.Fatal(again, err, submissions.Load())
	}
	// A replacement worker polls the previously saved upstream ID. Failure releases the quota.
	replacement := videogen.Service{Store: svc.Store, Secrets: svc.Secrets, Root: svc.Root, HTTP: upstream.Client(), Notify: mr.NotifyVideo}
	if err = replacement.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	again, err = svc.Run(ctx, scope, args)
	if err != nil || again.Status != "failed" || again.Billing != "no_charge" {
		t.Fatal(again, err)
	}
	var allowed bool
	if err = db.QueryRowContext(ctx, "SELECT manager_input_actor_allowed(id) AND manager_input_admission_allowed(id) FROM manager_inputs WHERE video_generation_id=$1", first.ID).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("trigger completion authorization: allowed=%t err=%v", allowed, err)
	}
	unknown.Store(true)
	pending, err := svc.Run(ctx, scope, another)
	if err != nil || pending.Status != "pending_review" {
		t.Fatal(pending, err)
	}
	if _, err = svc.Run(ctx, scope, another); err != nil || submissions.Load() != 2 {
		t.Fatal("unknown request resubmitted", err)
	}
	wantCode(svc.Resolve(ctx, actor, pending.ID, videogen.Resolution{Action: "reconcile", Reason: "No upstream ID"}), "generation_state_conflict")
	reviewExec(t, db, "UPDATE manager_sessions SET stop_revision=stop_revision+1 WHERE id=$1", task.SessionID)
	if err = svc.Resolve(ctx, actor, pending.ID, videogen.Resolution{Action: "close_no_charge", Reason: "Confirmed no recoverable output"}); err != nil {
		t.Fatal(err)
	}
	if err = replacement.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var notifications, charges int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM manager_inputs WHERE session_id=$1 AND video_generation_id IS NOT NULL", task.SessionID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM platform_credit_ledger WHERE kind='video_charge'").Scan(&charges); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT manager_input_actor_allowed(id) OR manager_input_admission_allowed(id) FROM manager_inputs WHERE video_generation_id=$1", first.ID).Scan(&allowed); err != nil || allowed {
		t.Fatalf("stopped trigger completion: allowed=%t err=%v", allowed, err)
	}
	if notifications != 1 || charges != 0 {
		t.Fatalf("notifications=%d, charges=%d", notifications, charges)
	}
	_, err = svc.Run(ctx, scope, args)
	wantCode(err, "capability_forbidden")
}
