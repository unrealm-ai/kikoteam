package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	controller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/imagegen"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestImageMigrationPreservesExistingReferralCredit(t *testing.T) {
	const prior = "000110_task_reported_progress.sql"
	db := workspaceTestDBThrough(t, prior)
	account := service.NewID()
	insertWorkspaceAccount(t, db, account, "referral-owner@example.test")
	reviewExec(t, db, "INSERT INTO platform_credit_wallets(account_id,balance_units,version) VALUES($1,100000000,1)", account)
	reviewExec(t, db, `INSERT INTO platform_credit_ledger(id,account_id,wallet_version,kind,amount_units,balance_after_units,biz_key)
		VALUES($1,$2,1,'referral_bonus',100000000,100000000,'referral:fixture')`, service.NewID(), account)
	applyTestMigrations(t, t.Context(), db, prior, "")
	ledger, err := (repo.Store{DB: db}).ListCredits(t.Context(), account, repo.Filter{Account: account}, "", 10, true)
	if err != nil || len(ledger.Items) != 1 || ledger.Summary["granted"] != "100.000000" || ledger.Summary["spent"] != "0.000000" {
		t.Fatal("migration changed an existing referral reward", ledger, err)
	}
}

func TestImageGenerationTaskBillingAndReplay(t *testing.T) {
	ctx := context.Background()
	db, sessions, browser, w, actor, employee, cookie := managerFixture(t)
	rt := runtimeCapabilityServices(t, db)
	catalog, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	reviewExec(t, db, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", actor)
	var pngData bytes.Buffer
	if e = png.Encode(&pngData, image.NewGray(image.Rect(0, 0, 1024, 1024))); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer test-only" {
			http.Error(out, "denied", http.StatusUnauthorized)
			return
		}
		var body struct {
			ResponseFormat string `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ResponseFormat != "b64_json" {
			t.Error("expected inline image request", err)
		}
		n := calls.Add(1)
		if n == 2 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if n == 5 {
			out.WriteHeader(http.StatusBadRequest)
			return
		}
		if n == 4 || n == 6 {
			out.WriteHeader(503)
			return
		}
		if err := json.NewEncoder(out).Encode(map[string]any{"data": []any{map[string]any{"b64_json": pngData.Bytes()}}}); err != nil {
			t.Error(err)
		}
	}))
	defer provider.Close()
	secret := strings.Repeat("i", 40)
	s := imagegen.Service{Store: repo.Store{DB: db}, Secrets: mp.Secrets{Root: []byte(secret)}, Root: t.TempDir(), HTTP: provider.Client()}
	old, e := s.Settings(ctx, actor)
	if e != nil || old.Enabled {
		t.Fatal(old, e)
	}
	on := true
	model, key, protocol := "fixture", "test-only", "openai_images_b64_json"
	p := imagegen.Patch{Version: &old.Version, Enabled: &on, BaseURL: &provider.URL, Model: &model, APIKey: &key, Provider: &protocol}
	if _, e = s.Save(ctx, actor, p); e == nil {
		t.Fatal("untested provider enabled")
	}
	result, e := s.Test(ctx, actor, p)
	if e != nil {
		t.Fatal(e)
	}
	p.Receipt = result.(map[string]any)["receipt"].(string)
	cfg, e := s.Save(ctx, actor, p)
	if e != nil || cfg.Provider != protocol {
		t.Fatal(cfg.Provider, e)
	}
	defaultProtocol := "openai_images"
	if _, e = s.Save(ctx, actor, imagegen.Patch{Version: &cfg.Version, Provider: &defaultProtocol}); e == nil {
		t.Fatal("untested response format change accepted")
	}
	store := sessions.Store.(*managerrepo.Store)
	store.SnapshotCapabilities = SnapshotCapabilities
	store.RuntimeRoot = s.Root
	store.WorkAssets = rt.Skills.Assets.(manager.WorkAssets)
	conversation, e := sessions.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: employee})
	if e != nil {
		t.Fatal(e)
	}
	req := manager.CreateSession{ConversationID: conversation.ID, Mode: "task"}
	req.Input = &struct {
		Text string `json:"text"`
	}{"test image"}
	task, _, e := sessions.CreateSession(ctx, w, actor, "image-generation", req)
	if e != nil {
		t.Fatal(e)
	}
	id := manager.DelegationIdentity{SessionID: task.ID, EngineID: service.NewID(), AssignmentID: service.NewID(), Generation: 1}
	if e = store.RegisterEngine(ctx, manager.Engine{ID: id.EngineID, HostID: "images", Endpoint: "http://127.0.0.1:1", Capacity: 1}); e != nil {
		t.Fatal(e)
	}
	turn, input := service.NewID(), *task.InitialInputID
	reviewExec(t, db, "INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) VALUES($1,$2,$3,$4,1,$5,1,'running')", turn, w, task.ID, input, id.AssignmentID)
	reviewExec(t, db, "UPDATE manager_inputs SET status='started',turn_id=$2 WHERE id=$1", input, turn)
	reviewExec(t, db, "UPDATE manager_sessions SET active_turn_id=$2,engine_id=$3,assignment_id=$4,generation=1 WHERE id=$1", task.ID, turn, id.EngineID, id.AssignmentID)
	mux := http.NewServeMux()
	(&controller.Server{Service: sessions, Images: &s, Engines: store, EngineToken: secret, ResolveModelKey: func(context.Context, string, string, manager.Configuration) (string, error) { return "model-test", nil }}).Routes(mux)
	q := url.Values{"engine_id": {id.EngineID}, "assignment_id": {id.AssignmentID}, "generation": {"1"}, "input_id": {input}}
	harness := httptest.NewRequest("GET", "/api/internal/v1/sessions/"+task.ID+"/harness-config?"+q.Encode(), nil)
	harness.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, harness)
	var h manager.HarnessConfig
	if json.Unmarshal(rec.Body.Bytes(), &h) != nil || h.ImageGenerationToken == "" || strings.Contains(rec.Body.String(), key) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	scope := imagegen.Scope{DelegationIdentity: id, Input: input, Version: h.Configuration.Capabilities.DefaultPlugins[0].VersionID}
	args := imagegen.Arguments{Prompt: "A leaf", Key: "leaf-1"}
	if _, e = s.Run(ctx, scope, args); e == nil || calls.Load() != 1 {
		t.Fatal("missing payer", e)
	}
	agents := service.AgentService{Store: &AgentStore{DB: db, EnsureKey: s.Secrets.EnsureKey}}
	admin := service.NewID()
	insertWorkspaceAccount(t, db, admin, "image-admin@example.test")
	reviewExec(t, db, "INSERT INTO platform_members(workspace_id,user_id,role,status) VALUES($1,$2,'admin','active')", w, admin)
	var deniedBinding *service.Error
	if _, e = agents.BindPayer(ctx, w, admin, employee, 1); !errors.As(e, &deniedBinding) || deniedBinding.Code != "workspace_forbidden" {
		t.Fatal("only the workspace owner may confirm the payer", e)
	}
	if _, e = agents.BindPayer(ctx, w, actor, employee, 1); e != nil {
		t.Fatal(e)
	}
	reviewExec(t, db, "UPDATE platform_credit_settings SET signup_bonus_units=1000000 WHERE id=1")
	if e = s.Transaction(ctx, func(tx *sql.Tx) error { return repo.Signup(ctx, tx, actor) }); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Grant(ctx, actor, actor, "first", "test", 1000000); e != nil {
		t.Fatal(e)
	}
	rpc := func(name string, arg any) *httptest.ResponseRecorder {
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": name, "params": arg})
		if err != nil {
			panic(err)
		}
		r := httptest.NewRequest("POST", "/mcp/platform-plugins/image-generation", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+h.ImageGenerationToken)
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	list := rpc("tools/list", map[string]any{})
	if list.Code != 200 || !strings.Contains(list.Body.String(), "generate_image") {
		t.Fatal(list.Code, list.Body.String())
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- rpc("tools/call", map[string]any{"name": "generate_image", "arguments": args}) }()
	select {
	case <-started:
	case out := <-done:
		t.Fatal(out.Code, out.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("no supplier dispatch")
	}
	// Never leave a blocked fixture on failure.
	unblock := true
	defer func() {
		if unblock {
			close(release)
		}
	}()
	replay, e := s.Run(ctx, scope, args)
	if e != nil || replay.Status != "dispatched" {
		t.Fatal(replay, e)
	}
	one := 1
	cfg, e = s.Save(ctx, actor, imagegen.Patch{Version: &cfg.Version, Concurrent: &one})
	if e != nil {
		t.Fatal(e)
	}
	competing := args
	competing.Key = "competing"
	var policy *service.Error
	if _, e = s.Run(ctx, scope, competing); !errors.As(e, &policy) || policy.Code != "concurrency_limit_reached" {
		t.Fatal("global concurrency limit", e)
	}
	price := "8"
	initialCap := 8
	cfg, e = s.Save(ctx, actor, imagegen.Patch{Version: &cfg.Version, Price: &price, Limit: &one, Concurrent: &initialCap})
	if e != nil {
		t.Fatal(e)
	}
	another := args
	another.Key = "leaf-2"
	if _, e = s.Run(ctx, scope, another); !errors.As(e, &policy) || policy.Code != "daily_limit_reached" || calls.Load() != 2 {
		t.Fatal("daily quota", e)
	}
	close(release)
	unblock = false
	out := <-done
	if out.Code != 200 || !strings.Contains(out.Body.String(), "settled") {
		t.Fatal(out.Code, out.Body.String())
	}
	got, e := s.Run(ctx, scope, args)
	if e != nil || got.Billing != "settled" || got.Price != "5.000000" || got.ID != replay.ID || calls.Load() != 2 {
		t.Fatal(got, e)
	}
	data, e := manager.ReadWorkFile(s.Root, w, employee, conversation.ID, "", *task.TaskID, got.Path)
	if e != nil || !bytes.Equal(data, pngData.Bytes()) {
		t.Fatal("working copy", e)
	}
	published, e := store.WorkTool(ctx, manager.WorkScope{Identity: id, InputID: input}, "artifact_publish", manager.WorkArguments{Path: got.Path})
	if e != nil {
		t.Fatal(e)
	}
	artifact := published.(manager.Artifact)
	if _, _, err := sessions.TaskArtifact(ctx, w, actor, *task.TaskID, artifact.ID); err == nil {
		t.Fatal("artifact was visible before turn completion")
	}
	reviewExec(t, db, "UPDATE manager_turns SET status='completed',finished_at=now() WHERE id=$1", turn)
	detail, e := store.Task(ctx, w, actor, *task.TaskID)
	if e != nil || len(detail.Images) != 1 || detail.Images[0].ID != artifact.ID {
		t.Fatal("published task image listing", e)
	}
	for _, check := range []struct {
		workspace string
		download  bool
		status    int
	}{{w, false, 200}, {w, true, 200}, {service.NewID(), false, 404}} {
		path := "/api/v1/workspaces/" + check.workspace + "/tasks/" + *task.TaskID + "/artifacts/" + artifact.ID + "/content"
		if check.download {
			path += "?download=1"
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Cookie", cookie)
		rr := httptest.NewRecorder()
		browser.ServeHTTP(rr, r)
		if rr.Code != check.status {
			t.Fatal("artifact authorization", rr.Code, rr.Body.String())
		}
		if check.status == 200 && (!bytes.Equal(rr.Body.Bytes(), pngData.Bytes()) || strings.HasPrefix(rr.Header().Get("Content-Disposition"), "attachment") != check.download) {
			t.Fatal("incorrect artifact content")
		}
	}
	// Continue testing replay and subsequent requests under the original fixture turn.
	reviewExec(t, db, "UPDATE manager_turns SET status='running',finished_at=NULL WHERE id=$1", turn)
	working := filepath.Join(s.Root, "workspaces", w, "projects", "conv-"+conversation.ID, got.Path)
	if e = os.Remove(working); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Run(ctx, scope, args); e != nil {
		t.Fatal("restore copy", e)
	}
	if restored, e := os.ReadFile(working); e != nil || !bytes.Equal(restored, pngData.Bytes()) {
		t.Fatal("recovered original", e)
	}
	var balance int64
	if e = db.QueryRow("SELECT balance_units FROM platform_credit_wallets WHERE account_id=$1", actor).Scan(&balance); e != nil || balance != -3000000 {
		t.Fatal(balance, e)
	}
	var denied *service.Error
	if _, e = s.Run(ctx, scope, another); !errors.As(e, &denied) || denied.Code != "insufficient_credits" {
		t.Fatal(e)
	}
	changed := args
	changed.Prompt = "different"
	if _, e = s.Run(ctx, scope, changed); e == nil {
		t.Fatal("same-key parameter conflict accepted")
	}
	two := 5
	cfg, e = s.Save(ctx, actor, imagegen.Patch{Version: &cfg.Version, Limit: &two})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Grant(ctx, actor, actor, "more", "test", 20000000); e != nil {
		t.Fatal(e)
	}
	got, e = s.Run(ctx, scope, another)
	if e != nil || got.Price != "8.000000" || got.Billing != "settled" {
		t.Fatal(got, e)
	}
	unknown := args
	unknown.Key = "unknown"
	if _, e = s.Run(ctx, scope, unknown); e == nil {
		t.Fatal("upstream 503 accepted")
	}
	pending, e := s.Run(ctx, scope, unknown)
	if e != nil || pending.Status != "pending_review" || pending.Charged != nil {
		t.Fatal(pending, e)
	}
	if e = manager.SaveImageOriginal(s.Root, w, pending.ID, pngData.Bytes()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Resolve(ctx, actor, pending.ID, "close_no_charge", "try to close"); e == nil {
		t.Fatal("recoverable original closed without charge")
	}
	got, e = s.Resolve(ctx, actor, pending.ID, "reconcile", "fixture original found")
	if e != nil || got.Billing != "settled" {
		t.Fatal(got, e)
	}
	declined := args
	declined.Key = "rejected"
	if _, e = s.Run(ctx, scope, declined); e == nil {
		t.Fatal("known supplier failure")
	}
	zero, e := s.Run(ctx, scope, declined)
	if e != nil || zero.Status != "failed" || zero.Billing != "no_charge" || zero.Charged == nil || *zero.Charged != "0.000000" {
		t.Fatal(zero, e)
	}
	unavailable := args
	unavailable.Key = "unrecoverable"
	if _, e = s.Run(ctx, scope, unavailable); e == nil {
		t.Fatal("unknown supplier failure")
	}
	waiting, e := s.Run(ctx, scope, unavailable)
	if e != nil || waiting.Status != "pending_review" {
		t.Fatal(waiting, e)
	}
	zero, e = s.Resolve(ctx, actor, waiting.ID, "close_no_charge", "Supplier confirmed no original")
	if e != nil || zero.Billing != "no_charge" {
		t.Fatal(zero, e)
	}
	if _, e = s.Resolve(ctx, actor, waiting.ID, "reconcile", "late reply"); e == nil {
		t.Fatal("terminal record reopened")
	}
	bills, e := s.List(ctx, actor, imagegen.Filter{Account: actor}, "", 2)
	if e != nil {
		t.Fatal(e)
	}
	listing := bills.(map[string]any)
	if len(listing["items"].([]imagegen.Usage)) != 2 || listing["next_cursor"] == "" {
		t.Fatal("missing usage cursor")
	}
	if _, e = s.List(ctx, actor, imagegen.Filter{Account: actor}, listing["next_cursor"].(string), 2); e != nil {
		t.Fatal("usage continuation", e)
	}
	// Upgrading the catalog preserves the currently executing turn and its admin price.
	previous := scope.Version
	catalog.Revision++
	for i, p := range catalog.Plugins {
		if p.Key == imagegen.Key {
			catalog.Plugins[i].Description += " Revised."
		}
	}
	if e = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	pinned, _, e := store.HarnessConfig(ctx, id.EngineID, id.SessionID, id.AssignmentID, id.Generation, input)
	if e != nil || pinned.Configuration.Capabilities.DefaultPlugins[0].VersionID != previous {
		t.Fatal("active turn changed", e)
	}
	var next service.ExecutionCapabilities
	if e = s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		next, err = SnapshotCapabilities(ctx, tx, w, actor, employee)
		return err
	}); e != nil || len(next.DefaultPlugins) != 1 || next.DefaultPlugins[0].VersionID == previous {
		t.Fatal("new admission missed version", e)
	}
	cfg, e = s.Settings(ctx, actor)
	if e != nil || cfg.Price != "8.000000" || !cfg.Enabled {
		t.Fatal("upgrade reset operator settings", e)
	}
	off := false
	if _, e = s.Save(ctx, actor, imagegen.Patch{Version: &cfg.Version, Enabled: &off}); e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(ctx, db, catalog, rt.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	unknown.Key = "disabled"
	if _, e = s.Run(ctx, scope, unknown); e == nil || calls.Load() != 6 {
		t.Fatal("disabled plugin", e)
	}
	if e = s.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow("SELECT count(*) FROM platform_credit_ledger WHERE kind='image_charge'").Scan(&count); e != nil || count != 3 {
		t.Fatal(count, e)
	}
	// Settle a representative pre-existing chat usage against the very same wallet.
	modelID, usageID := service.NewID(), service.NewID()
	reviewExec(t, db, `INSERT INTO platform_proxy_models(id,name,protocol,base_url,upstream_model_id,input_price_units,output_price_units,updated_by)
	VALUES($1,'Chat fixture','chat_completions','https://example.invalid/v1','fixture',1000000,0,$2)`, modelID, actor)
	reviewExec(t, db, `INSERT INTO platform_model_usage(id,workspace_id,agent_id,payer_account_id,model_id,price_snapshot,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,status,deadline_at)
	VALUES($1,$2,$3,$4,$5,'{"input_units":"1000000","output_units":"0","cache_read_units":"0","cache_write_units":"0"}',1000000,0,0,0,'usage_recorded',now())`, usageID, w, employee, actor, modelID)
	if e = s.Settle(ctx, usageID); e != nil {
		t.Fatal(e)
	}
	page, e := s.ListCredits(ctx, actor, repo.Filter{Account: actor}, "", 30, true)
	if e != nil || page.Summary["spent"] != "22.000000" || page.Summary["granted"] != "22.000000" {
		t.Fatal("wallet totals", page.Summary, e)
	}
	ledger, e := s.ListCredits(ctx, actor, repo.Filter{Account: actor, Kind: "image_charge"}, "", 30, true)
	if e != nil || len(ledger.Items) != 3 || !bytes.Contains(ledger.Items[0], []byte("credits_per_image")) {
		t.Fatal("image ledger", e)
	}
	if e = s.Transaction(ctx, func(tx *sql.Tx) error { return repo.RewardReferral(ctx, tx, actor, admin) }); e != nil {
		t.Fatal(e)
	}
	page, e = s.ListCredits(ctx, actor, repo.Filter{Account: actor}, "", 30, true)
	if e != nil || page.Summary["spent"] != "22.000000" || page.Summary["granted"] != "122.000000" {
		t.Fatal("image charges and referral rewards must retain their accounting categories", page.Summary, e)
	}
	reviewExec(t, db, "UPDATE platform_agents SET enabled=false WHERE id=$1", employee)
	if _, e = s.Run(ctx, scope, args); e == nil {
		t.Fatal("revoked result read")
	}
}
