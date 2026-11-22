package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"os"
	"sync"
	"testing"
	"time"
)

type modelProbeStub struct{}

func (modelProbeStub) Probe(_ context.Context, _ service.ProbeInput) (service.ProbeResult, error) {
	return service.ProbeResult{Text: service.ProbeCase{Status: "passed", Output: "OK"}, Image: service.ProbeCase{Status: "unknown", Issue: "timeout"}}, nil
}
func modelFixture(t *testing.T) (*sql.DB, *service.ModelService, string, string) {
	return modelFixtureThrough(t, "")
}

func modelFixtureThrough(t *testing.T, lastMigration string) (*sql.DB, *service.ModelService, string, string) {
	t.Helper()
	db := workspaceTestDBThrough(t, lastMigration)
	owner := service.NewID()
	insertWorkspaceAccount(t, db, owner, "models@example.test")
	ws := &service.WorkspaceService{Store: NewWorkspaceStore(db)}
	w, e := ws.Create(context.Background(), owner, "Models", "")
	if e != nil {
		t.Fatal(e)
	}
	s := &service.ModelService{Store: NewModelStore(db), Prober: modelProbeStub{}, Secret: []byte("test-model-secret-at-least-thirty-two-bytes"), NormalizeEndpoint: func(_ string, url string) (string, error) { return url, nil }}
	return db, s, w.ID, owner
}
func configForTest() service.ModelConfig {
	key := "test-private-api-key"
	return service.ModelConfig{Name: "Mock", Protocol: "chat_completions", BaseURL: "https://models.example.test/v1", ModelID: "mock", APIKey: &key}
}
func saveTestModel(t *testing.T, s *service.ModelService, ws, actor string) service.ModelProfile {
	t.Helper()
	ctx := context.Background()
	c := configForTest()
	r, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Save(ctx, ws, actor, "", service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPostgresModelReceiptConcurrentSaveAndSecretIsolation(t *testing.T) {
	db, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	c := configForTest()
	r, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c})
	if e != nil {
		t.Fatal(e)
	}
	req := service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt}
	results := make(chan service.ModelProfile, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); p, e := s.Save(ctx, ws, actor, "", req); results <- p; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for p := range results {
		if id != "" && p.ID != id {
			t.Fatal("duplicate model for same receipt")
		}
		id = p.ID
		if p.Version != 1 {
			t.Fatal("duplicate version")
		}
	}
	var count int
	var ciphertext []byte
	if e = db.QueryRow(`SELECT count(*) FROM platform_model_profiles`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, e)
	}
	if e = db.QueryRow(`SELECT credential FROM platform_model_profiles WHERE id=$1`, id).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(ciphertext, []byte(*c.APIKey)) {
		t.Fatal("plaintext credential persisted")
	}
	p, e := s.Get(ctx, ws, actor, id)
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encoded, []byte(*c.APIKey)) || bytes.Contains(encoded, []byte("credential")) {
		t.Fatal("secret in public model JSON")
	}
	var versions int
	if e = db.QueryRow(`SELECT count(*) FROM platform_model_profile_versions WHERE profile_id=$1`, id).Scan(&versions); e != nil || versions != 1 {
		t.Fatal("bad versions", e)
	}
}
func TestPostgresModelsReceiptsBindConfigActorScopeAndExpiry(t *testing.T) {
	_, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	c := configForTest()
	r, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c})
	if e != nil {
		t.Fatal(e)
	}
	changed := c
	changed.ModelID = "different"
	if _, e = s.Save(ctx, ws, actor, "", service.ModelSaveRequest{ModelConfig: changed, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("changed model accepted")
	}
	changed = c
	key := "different-key"
	changed.APIKey = &key
	if _, e = s.Save(ctx, ws, actor, "", service.ModelSaveRequest{ModelConfig: changed, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("changed key accepted")
	}
	s.Now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, e = s.Save(ctx, ws, actor, "", service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("expired receipt accepted")
	}
	s.Now = nil
	outsider := service.NewID()
	if _, e = s.Save(ctx, ws, outsider, "", service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("outsider accepted")
	}
}
func TestPostgresModelVersionsRevokeAndRetainedKey(t *testing.T) {
	db, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	p := saveTestModel(t, s, ws, actor)
	c := configForTest()
	c.APIKey = nil
	c.Name = "Renamed"
	r, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c, ProfileID: p.ID, Version: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	updated, e := s.Save(ctx, ws, actor, p.ID, service.ModelSaveRequest{ModelConfig: c, Version: p.Version, TestReceipt: r.Receipt})
	if e != nil {
		t.Fatal(e)
	}
	if !updated.HasKey || updated.Version != 2 {
		t.Fatal("retained key/version", updated.Version)
	}
	old, e := s.Change(ctx, ws, actor, p.ID, "revoke-key", updated.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	if old.HasKey || old.Enabled {
		t.Fatal("revoke retained key or enabled")
	}
	if _, e = s.Change(ctx, ws, actor, p.ID, "status", old.Version, true); e == nil {
		t.Fatal("revoke bypassed test by enabling")
	}
	if _, e = s.Save(ctx, ws, actor, p.ID, service.ModelSaveRequest{ModelConfig: c, Version: p.Version, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("old receipt after revoke accepted")
	}
	var v int
	if e = db.QueryRow(`SELECT count(*) FROM platform_model_profile_versions WHERE profile_id=$1`, p.ID).Scan(&v); e != nil || v != 3 {
		t.Fatal("immutable history missing", v, e)
	}
}
func TestPostgresModelCrossWorkspaceForeignKeysAndRollback(t *testing.T) {
	db, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	p := saveTestModel(t, s, ws, actor)
	wsvc := &service.WorkspaceService{Store: NewWorkspaceStore(db)}
	other, e := wsvc.Create(ctx, actor, "Other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, other.ID, actor, p.ID); !errors.Is(e, service.ErrWorkspaceMissing) {
		t.Fatalf("cross workspace read %v", e)
	}
	_, e = db.Exec(`INSERT INTO platform_model_profile_versions(workspace_id,profile_id,version,name,description,protocol,base_url,model_id,vision_status,enabled,tested_at) VALUES($1,$2,99,'bad','','chat_completions','https://example.test','test','unknown',true,now())`, other.ID, p.ID)
	if e == nil {
		t.Fatal("cross workspace version FK accepted")
	}
	e = NewModelStore(db).WithWorkspace(ctx, ws, func(tx service.ModelTx) error {
		copy := p
		copy.Version++
		copy.Name = "should rollback"
		if e := tx.Save(ctx, copy, false); e != nil {
			return e
		}
		return errors.New("abort")
	})
	if e == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	actual, e := s.Get(ctx, ws, actor, p.ID)
	if e != nil || actual.Name != p.Name {
		t.Fatal("transaction rollback failed", e)
	}
}

func TestPostgresModelsConcurrentVersionAndDefaults(t *testing.T) {
	db, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	p := saveTestModel(t, s, ws, actor)
	c := configForTest()
	c.APIKey = nil
	c.Name = "first edit"
	a, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c, ProfileID: p.ID, Version: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	other := c
	other.Name = "second edit"
	b, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: other, ProfileID: p.ID, Version: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, req := range []service.ModelSaveRequest{{ModelConfig: c, Version: p.Version, TestReceipt: a.Receipt}, {ModelConfig: other, Version: p.Version, TestReceipt: b.Receipt}} {
		wg.Add(1)
		go func(r service.ModelSaveRequest) {
			defer wg.Done()
			_, err := s.Save(ctx, ws, actor, p.ID, r)
			results <- err
		}(req)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same version writers succeeded %d", success)
	}
	current, e := s.Get(ctx, ws, actor, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	selected, e := s.Change(ctx, ws, actor, p.ID, "set-default", current.Version, false)
	if e != nil || !selected.IsDefault {
		t.Fatal("set default", e)
	}
	p2 := saveTestModel(t, s, ws, actor)
	if _, e = s.Change(ctx, ws, actor, p2.ID, "set-default", p2.Version, false); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM platform_model_profiles WHERE workspace_id=$1 AND is_default`, ws).Scan(&count); e != nil || count != 1 {
		t.Fatal("multiple defaults", count, e)
	}
	first, e := s.Get(ctx, ws, actor, p.ID)
	if e != nil || first.IsDefault || first.Version != selected.Version {
		t.Fatal("default clearing mutated model content", e)
	}
}

func TestPostgresModelsMemberReadOnlyAndReceiptOwnership(t *testing.T) {
	db, s, ws, actor := modelFixture(t)
	ctx := context.Background()
	member, admin := service.NewID(), service.NewID()
	insertWorkspaceAccount(t, db, member, "member@example.test")
	insertWorkspaceAccount(t, db, admin, "admin@example.test")
	if _, e := db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'admin')`, ws, member, admin); e != nil {
		t.Fatal(e)
	}
	p := saveTestModel(t, s, ws, actor)
	if _, e := s.Get(ctx, ws, member, p.ID); e != nil {
		t.Fatal("member read forbidden", e)
	}
	c := configForTest()
	if _, e := s.Test(ctx, ws, member, service.ModelTestRequest{ModelConfig: c}); e == nil {
		t.Fatal("member probe allowed")
	}
	r, e := s.Test(ctx, ws, actor, service.ModelTestRequest{ModelConfig: c})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Save(ctx, ws, admin, "", service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("other admin used owner receipt")
	}
	if _, e = db.Exec(`UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, ws, actor); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Save(ctx, ws, actor, "", service.ModelSaveRequest{ModelConfig: c, TestReceipt: r.Receipt}); e == nil {
		t.Fatal("revoked member saved")
	}
}

func TestPostgresModelMigrationPreservesVersionFourData(t *testing.T) {
	db := workspaceTestDBThrough(t, "000004_invitation_email_outbox.sql")
	actor := service.NewID()
	insertWorkspaceAccount(t, db, actor, "old@example.test")
	w, e := (&service.WorkspaceService{Store: NewWorkspaceStore(db)}).Create(context.Background(), actor, "Existing workspace", "")
	if e != nil {
		t.Fatal(e)
	}
	sql, e := os.ReadFile("../../../../event-hub/database/postgres/migrations/000005_platform_models.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(sql)); e != nil {
		t.Fatal(e)
	}
	var name string
	if e = db.QueryRow(`SELECT name FROM platform_workspaces WHERE id=$1`, w.ID).Scan(&name); e != nil || name != "Existing workspace" {
		t.Fatal("old workspace changed", e)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM platform_accounts WHERE id=$1`, actor).Scan(&n); e != nil || n != 1 {
		t.Fatal("old account lost", e)
	}
}

type modelProbeFunc func(context.Context, service.ProbeInput) (service.ProbeResult, error)

func (f modelProbeFunc) Probe(ctx context.Context, in service.ProbeInput) (service.ProbeResult, error) {
	return f(ctx, in)
}

func TestPostgresModelCredentialDestinationAndInputSafety(t *testing.T) {
	_, s, ws, actor := modelFixture(t)
	p := saveTestModel(t, s, ws, actor)
	calls := 0
	s.Prober = modelProbeFunc(func(ctx context.Context, in service.ProbeInput) (service.ProbeResult, error) {
		calls++
		return (modelProbeStub{}).Probe(ctx, in)
	})
	c := configForTest()
	c.APIKey = nil
	c.BaseURL = "https://another.example.test/v1"
	if _, e := s.Test(context.Background(), ws, actor, service.ModelTestRequest{ModelConfig: c, ProfileID: p.ID, Version: p.Version}); e == nil {
		t.Fatal("old credential sent to new destination")
	}
	if calls != 0 {
		t.Fatal("prober called before destination validation")
	}
	empty := ""
	c.APIKey = &empty
	if _, e := s.Test(context.Background(), ws, actor, service.ModelTestRequest{ModelConfig: c, ProfileID: p.ID, Version: p.Version}); e != nil {
		t.Fatal("explicit unauthenticated replacement rejected", e)
	}
	for _, url := range []string{"http://example.test", "http://127.0.0.1:1"} {
		c.BaseURL = url
		_, e := s.Test(context.Background(), ws, actor, service.ModelTestRequest{ModelConfig: c})
		var typed *service.Error
		if !errors.As(e, &typed) || typed.Status != 400 {
			t.Fatal("bad URL not 400", e)
		}
	}
	c = configForTest()
	nul := "a\x00b"
	c.APIKey = &nul
	_, e := s.Test(context.Background(), ws, actor, service.ModelTestRequest{ModelConfig: c})
	var typed *service.Error
	if !errors.As(e, &typed) || typed.Status != 400 {
		t.Fatal("NUL key not 400", e)
	}
	if calls != 1 {
		t.Fatal("invalid inputs caused outbound request", calls)
	}
}

func TestPostgresModelRevalidatesVersionAfterProbe(t *testing.T) {
	_, s, ws, actor := modelFixture(t)
	p := saveTestModel(t, s, ws, actor)
	s.Prober = modelProbeFunc(func(ctx context.Context, in service.ProbeInput) (service.ProbeResult, error) {
		if _, e := s.Change(ctx, ws, actor, p.ID, "status", p.Version, false); e != nil {
			return service.ProbeResult{}, e
		}
		return (modelProbeStub{}).Probe(ctx, in)
	})
	if _, e := s.Test(context.Background(), ws, actor, service.ModelTestRequest{ModelConfig: configForTest(), ProfileID: p.ID, Version: p.Version}); e == nil {
		t.Fatal("receipt issued after concurrent model change")
	}
}

func TestPostgresModelProbeLeaseContentionTakeoverAndLateRelease(t *testing.T) {
	db, _, ws, actor := modelFixture(t)
	ctx := context.Background()
	s1, s2 := NewModelStore(db), NewModelStore(db)
	one, two := service.NewID(), service.NewID()
	lease := 50 * time.Second
	claim := func(s *ModelStore, id string) error {
		return s.WithWorkspace(ctx, ws, func(tx service.ModelTx) error { return tx.ClaimProbe(ctx, actor, id, lease) })
	}
	if e := claim(s1, one); e != nil {
		t.Fatal(e)
	}
	if e := claim(s2, two); e == nil {
		t.Fatal("second backend acquired active lease")
	}
	if _, e := db.Exec(`UPDATE platform_model_probe_leases SET expires_at=now()-interval '1 second' WHERE workspace_id=$1 AND actor_id=$2`, ws, actor); e != nil {
		t.Fatal(e)
	}
	if e := claim(s2, two); e != nil {
		t.Fatal("expired lease not acquired", e)
	}
	if e := s1.WithWorkspace(ctx, ws, func(tx service.ModelTx) error { return tx.ReleaseProbe(ctx, actor, one) }); e != nil {
		t.Fatal(e)
	}
	var actual string
	if e := db.QueryRow(`SELECT claim_id FROM platform_model_probe_leases WHERE workspace_id=$1 AND actor_id=$2`, ws, actor).Scan(&actual); e != nil || actual != two {
		t.Fatal("old release deleted new claim", e)
	}
	if e := s2.WithWorkspace(ctx, ws, func(tx service.ModelTx) error { return tx.ReleaseProbe(ctx, actor, two) }); e != nil {
		t.Fatal(e)
	}
	if e := claim(s1, one); e != nil {
		t.Fatal("release prevented next claim", e)
	}
}
func TestPostgresModelProbeRateLimitSharedAcrossInstances(t *testing.T) {
	db, _, ws, actor := modelFixture(t)
	ctx := context.Background()
	s1, s2 := NewModelStore(db), NewModelStore(db)
	for i := 0; i < 10; i++ {
		s := s1
		if i%2 == 0 {
			s = s2
		}
		if e := s.AllowProbe(ctx, ws, actor); e != nil {
			t.Fatal(e)
		}
	}
	if e := s2.AllowProbe(ctx, ws, actor); e == nil {
		t.Fatal("rate limit not shared")
	}
}

type waitingModelProbe struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (p *waitingModelProbe) Probe(ctx context.Context, _ service.ProbeInput) (service.ProbeResult, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return service.ProbeResult{}, ctx.Err()
	case <-p.release:
	}
	return service.ProbeResult{Text: service.ProbeCase{Status: "passed", Output: "OK"}, Image: service.ProbeCase{Status: "unsupported", Issue: "unsupported"}}, nil
}
func TestPostgresModelProbeTwoServicesOneOutboundAttempt(t *testing.T) {
	db, s1, ws, actor := modelFixture(t)
	p := &waitingModelProbe{started: make(chan struct{}, 2), release: make(chan struct{})}
	s1.Prober = p
	s2 := *s1
	s2.Store = NewModelStore(db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := service.ModelTestRequest{ModelConfig: configForTest()}
	done := make(chan error, 1)
	go func() { _, e := s1.Test(ctx, ws, actor, req); done <- e }()
	select {
	case <-p.started:
	case <-ctx.Done():
		t.Fatal("first probe not started")
	}
	_, e := s2.Test(ctx, ws, actor, req)
	var business *service.Error
	if !errors.As(e, &business) || business.Code != "model_probe_in_progress" {
		t.Errorf("second probe not rejected: %v", e)
	}
	close(p.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	calls := p.calls
	p.mu.Unlock()
	if calls != 1 {
		t.Fatalf("outbound calls %d", calls)
	}
	if _, e = s2.Test(ctx, ws, actor, req); e != nil {
		t.Fatal("completed probe did not release lease", e)
	}
}
