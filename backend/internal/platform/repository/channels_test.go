package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelConfigurationPersistenceAndHubAPI(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	store := NewChannelStore(db)
	s := &service.ChannelService{Store: store, Secret: []byte(strings.Repeat("k", 32))}
	secret := "synthetic-feishu-secret"
	q := service.ChannelPut{AppID: "cli_test", AppSecret: &secret, History: service.ChannelHistory{Limit: 100}, Access: service.ChannelAccess{Mode: "allowlist"}}
	b, e := s.Put(ctx, w, actor, agent, q)
	if e != nil {
		t.Fatal(e)
	}
	if b.Generation != 1 || b.Revision != 1 {
		t.Fatal(b)
	}
	var cipher []byte
	if e = db.QueryRowContext(ctx, `SELECT credential_ciphertext FROM platform_channel_bindings WHERE id=$1`, b.ID).Scan(&cipher); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(cipher), secret) {
		t.Fatal("plaintext persisted")
	}
	revision, e := store.PendingNotification(ctx)
	if e != nil || revision < 1 {
		t.Fatal(revision, e)
	}
	hits := 0
	failNotification := true
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/internal/v1/channel-bindings/refresh" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("h", 32) {
			t.Error("bad refresh request")
		}
		if failNotification {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(202)
	}))
	defer hub.Close()
	n := &service.ChannelNotifier{Store: store, URL: hub.URL, Token: strings.Repeat("h", 32)}
	if e = n.Notify(ctx); e == nil {
		t.Fatal("failed notification accepted")
	}
	pending, e := store.PendingNotification(ctx)
	if e != nil || pending != revision {
		t.Fatal("lost pending notification", e)
	}
	failNotification = false
	if e = n.Notify(ctx); e != nil {
		t.Fatal(e)
	}
	if hits != 2 {
		t.Fatal(hits)
	}
	if e = n.Notify(ctx); e != nil || hits != 2 {
		t.Fatal("duplicate notify", e)
	}
	snapshot, e := s.Snapshot(ctx)
	if e != nil || len(snapshot.Items) != 1 || snapshot.Items[0].Credentials.AppSecret != secret {
		t.Fatal("snapshot", e)
	}
	a := service.ChannelApplied{Generation: 1, RulesVersion: 1, Revision: 1, Status: "connected", ObservedTenantID: "test-tenant", ObservedBotID: "ou_test_bot"}
	if e = s.Apply(ctx, b.ID, a); e != nil {
		t.Fatal(e)
	}
	a.ObservedTenantID = "wrong-tenant"
	if e = s.Apply(ctx, b.ID, a); e == nil {
		t.Fatal("replaced pinned identity")
	}
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	u, e := usage.Get(ctx, w, actor, agent)
	if e != nil {
		t.Fatal(e)
	}
	u.UsageConfig.Trigger.Keywords = []service.UsagePattern{{Text: "help", Channels: []string{"feishu"}}}
	if _, e = usage.Update(ctx, w, actor, agent, u); e != nil {
		t.Fatal(e)
	}
	b, e = s.Get(ctx, w, actor, agent)
	if e != nil {
		t.Fatal(e)
	}
	if b.Generation != 1 || b.RulesVersion != 2 || b.TenantID != "test-tenant" {
		t.Fatal("rule edit changed identity")
	}
	a.ObservedTenantID = "test-tenant"
	if e = s.Apply(ctx, b.ID, a); e == nil {
		t.Fatal("stale applied report")
	}
	q.Version = b.Revision
	q.AppSecret = &secret
	b, e = s.Put(ctx, w, actor, agent, q)
	if e != nil || b.Generation != 2 || b.TenantID != "test-tenant" || b.BotID != "ou_test_bot" {
		t.Fatal("rotation must preserve identity pin", e)
	}
	rotated := service.ChannelApplied{Generation: b.Generation, RulesVersion: b.RulesVersion, Revision: b.Revision, Status: "connected", ObservedTenantID: "foreign-tenant", ObservedBotID: "ou_test_bot"}
	if e = s.Apply(ctx, b.ID, rotated); e == nil {
		t.Fatal("secret rotation changed pinned tenant")
	}
	rotated.ObservedTenantID = "test-tenant"
	rotated.ObservedBotID = "ou_foreign_bot"
	if e = s.Apply(ctx, b.ID, rotated); e == nil {
		t.Fatal("secret rotation changed pinned bot")
	}
	rotated.ObservedBotID = "ou_test_bot"
	if e = s.Apply(ctx, b.ID, rotated); e != nil {
		t.Fatal("same identity rotation failed", e)
	}
	// Identity epoch is independent of ordinary credential/connection rotation.
	if b.IdentityEpoch != 1 {
		t.Fatal("secret rotation split identity")
	}
	q.Version = b.Revision
	q.AppID = "cli_other"
	b, e = s.Put(ctx, w, actor, agent, q)
	if e != nil || b.IdentityEpoch != 2 || b.TenantID != "" || b.BotID != "" {
		t.Fatal("app replacement failed isolation", e)
	}
	if e = s.Apply(ctx, b.ID, a); e == nil {
		t.Fatal("old application pinned new binding")
	}
	current := service.ChannelApplied{Generation: b.Generation, RulesVersion: b.RulesVersion, Revision: b.Revision, Status: "connected", ObservedTenantID: "other-tenant", ObservedBotID: "ou_other_bot"}
	if e = s.Apply(ctx, b.ID, current); e != nil {
		t.Fatal(e)
	}
	current.ObservedTenantID = "foreign-tenant"
	if e = s.Apply(ctx, b.ID, current); e == nil {
		t.Fatal("cross tenant repin accepted")
	}
	q.Version = b.Revision
	q.AppID = "cli_test"
	b, e = s.Put(ctx, w, actor, agent, q)
	if e != nil || b.IdentityEpoch != 3 {
		t.Fatal("switching back revived old epoch", e)
	}
	token := strings.Repeat("h", 32)
	mux := http.NewServeMux()
	controller.ChannelInternalRoutes(mux, s, token)
	for _, headers := range []map[string]string{{}, {"Authorization": "Bearer " + token, "Cookie": "a=b"}, {"Authorization": "Bearer " + token, "Origin": "https://example.test"}} {
		r := httptest.NewRequest("GET", "/api/internal/v1/channel-bindings", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != 401 || strings.Contains(out.Body.String(), secret) {
			t.Fatal(out.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/internal/v1/channel-bindings", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var got service.HubChannelSnapshot
	if e = json.Unmarshal(out.Body.Bytes(), &got); e != nil || got.Items[0].Credentials.AppSecret != secret {
		t.Fatal(e)
	}
}

func TestChannelConcurrentEnableAmbiguity(t *testing.T) {
	db, _, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	var model string
	if e := db.QueryRowContext(ctx, `SELECT model_profile_id FROM platform_agents WHERE id=$1`, agent).Scan(&model); e != nil {
		t.Fatal(e)
	}
	other, e := (&service.AgentService{Store: NewAgentStore(db)}).Create(ctx, w, actor, service.AgentCreate{Name: "Other", ModelProfileID: model})
	if e != nil {
		t.Fatal(e)
	}
	s := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("s", 32))}
	secret := "synthetic-shared-app-secret"
	q := service.ChannelPut{AppID: "cli_shared", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 100}, Access: service.ChannelAccess{Mode: "allowlist"}}
	results := make(chan error, 2)
	for _, id := range []string{agent, other.ID} {
		go func(id string) { _, e := s.Put(ctx, w, actor, id, q); results <- e }(id)
	}
	successes, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			successes++
		} else {
			var p *service.Error
			if errors.As(e, &p) && p.Code == "channel_app_binding_ambiguous" {
				conflicts++
			} else {
				t.Fatal(e)
			}
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
}

func TestChannelPublicConfigurationHTTP(t *testing.T) {
	db, _, _, w, actor, agent, cookie := managerFixture(t)
	channels := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("p", 32)), CredentialProbe: manualCredentialProbe{}}
	api := (&controller.Server{Channels: channels, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	path := "/api/v1/workspaces/" + w + "/agents/" + agent + "/channels/feishu"
	call := func(method, body string, csrf bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
		}
		out := httptest.NewRecorder()
		api.ServeHTTP(out, r)
		return out
	}
	if out := call("GET", "", false); out.Code != 404 {
		t.Fatal("missing", out.Code, out.Body.String())
	}
	body := `{"version":0,"app_id":"cli_public","app_secret":"synthetic-http-secret","enabled":false}`
	if out := call("PUT", body, false); out.Code != 403 {
		t.Fatal("CSRF", out.Code, out.Body.String())
	}
	receipt, err := channels.TestCredentials(context.Background(), w, actor, agent, "feishu", service.ChannelCredentialTest{AppID: "cli_public", AppSecret: "synthetic-http-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var saveBody map[string]any
	if err = json.Unmarshal([]byte(body), &saveBody); err != nil {
		t.Fatal(err)
	}
	saveBody["test_receipt"] = receipt.Receipt
	encodedBody, err := json.Marshal(saveBody)
	if err != nil {
		t.Fatal(err)
	}
	body = string(encodedBody)
	out := call("PUT", body, true)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "synthetic-http-secret") || strings.Contains(out.Body.String(), "credential_ciphertext") {
		t.Fatal("secret disclosed")
	}
	var b service.ChannelBinding
	if e := json.Unmarshal(out.Body.Bytes(), &b); e != nil {
		t.Fatal(e)
	}
	if !b.History.Enabled || !b.Access.GroupMembersEnabled || b.Applied.Status != "pending" {
		t.Fatal("saved semantics", b)
	}
	if out := call("PUT", body, true); out.Code != 409 {
		t.Fatal("stale version", out.Code)
	}
	assignDifferentTestCreator(t, db, w, agent)
	if _, e := db.ExecContext(context.Background(), `UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, w, actor); e != nil {
		t.Fatal(e)
	}
	if out := call("GET", "", false); out.Code != 403 {
		t.Fatal("member can read credential settings", out.Code)
	}
}
