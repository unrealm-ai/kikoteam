package repository

import (
	"context"
	"database/sql"
	"errors"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
)

type contactFake struct{}

func (contactFake) Search(context.Context, string, string, string) (service.WecomContactSearch, error) {
	return service.WecomContactSearch{Items: []service.WecomContact{{SearchUserID: "search-A", DisplayName: "Alice", DepartmentPaths: []string{"研发部"}}, {SearchUserID: "search-B", DisplayName: "Bob", DepartmentPaths: []string{}}}}, nil
}
func wecomContactFixture(t *testing.T) (*sql.DB, *service.WecomContactsService, service.ChannelBinding, string) {
	t.Helper()
	db, channels, _, feishu, actor, _ := directoryFixture(t)
	secret := "synthetic-bot-secret"
	b, e := channels.PutProvider(context.Background(), feishu.WorkspaceID, actor, feishu.AgentID, "wecom", service.ChannelPut{AppID: "bot_contacts", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE platform_channel_bindings SET external_tenant_id=app_id,bot_open_id=app_id,identity_verified_generation=connection_generation WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	b, e = channels.GetProvider(context.Background(), b.WorkspaceID, actor, b.AgentID, "wecom")
	if e != nil {
		t.Fatal(e)
	}
	s := &service.WecomContactsService{Channels: channels, Store: &WecomContactStore{DB: db}, Provider: contactFake{}}
	return db, s, b, actor
}
func contactStart(t *testing.T, s *service.WecomContactsService, b service.ChannelBinding, actor, user string) service.WecomVerification {
	t.Helper()
	ctx := context.Background()
	if _, e := s.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Alice"); e != nil {
		t.Fatal(e)
	}
	v, e := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, user, service.NewID())
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Code) != 6 {
		t.Fatal(v)
	}
	return v
}
func contactInbound(b service.ChannelBinding, code, sender string) manager.ChannelInbound {
	return manager.ChannelInbound{BindingID: b.ID, ConnectionGeneration: b.Generation, ConfigRevision: b.Revision, RulesVersion: b.RulesVersion, ProviderEventID: service.NewID(), ProviderMessageID: service.NewID(), ExternalTenantID: b.AppID, ExternalChatID: "direct-" + sender, ChatType: "direct", SenderID: sender, SenderIDType: "user_id", Text: code, MessageType: "text", BotID: b.AppID, ProviderDirected: true, ControlEligible: true}
}
func TestWecomContactAdminRelayMapsOnlyTrustedDirectAndNeverInputs(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, s, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	// Quoted/mixed messages cannot complete the control flow.
	in := contactInbound(b, v.Code, "callback-A")
	in.QuotedText = "原始消息"
	if _, e := store.ReceiveChannel(ctx, in); e != nil {
		t.Fatal(e)
	}
	in = contactInbound(b, v.Code, "callback-A")
	in.MessageType = "mixed"
	if _, e := store.ReceiveChannel(ctx, in); e != nil {
		t.Fatal(e)
	}
	status, e := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false)
	if e != nil || status.Status != "pending" {
		t.Fatal(status, e)
	}
	in = contactInbound(b, "验证码 "+v.Code, "callback-A")
	if got, receiveErr := store.ReceiveChannel(ctx, in); receiveErr != nil || got.ControlReply != manager.WecomControlVerified {
		t.Fatal("verification reply", got.ControlReply, receiveErr)
	}
	if got, receiveErr := store.ReceiveChannel(ctx, in); receiveErr != nil || got.ControlReply != "" {
		t.Fatal("provider replay must not send another reply", got.ControlReply, receiveErr)
	}
	status, e = s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false)
	if e != nil || status.Status != "verified" || status.CallbackUserID != "callback-A" || status.Code != "" {
		t.Fatal(status, e)
	}
	results, e := s.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Alice")
	if e != nil || results.Items[0].CallbackUserID != "callback-A" || results.Items[0].SearchUserID != "search-A" {
		t.Fatal(results, e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM manager_channel_inbox WHERE provider_event_id=$1`, in.ProviderEventID).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	var ciphertext []byte
	if e = db.QueryRow(`SELECT code_ciphertext FROM platform_wecom_verifications WHERE id=$1`, v.ID).Scan(&ciphertext); e != nil || len(ciphertext) != 0 {
		t.Fatal("used code retained", e)
	}
}

func TestWecomRetiredCodeRecognitionEndsAfter72Hours(t *testing.T) {
	for _, state := range []struct {
		status string
		clock  string
		reply  manager.WecomControlReply
	}{
		{"verified", "completed_at", manager.WecomControlUsed},
		{"cancelled", "completed_at", manager.WecomControlRevoked},
		{"pending", "expires_at", manager.WecomControlExpired},
	} {
		t.Run(state.status, func(t *testing.T) {
			db, s, b, actor := wecomContactFixture(t)
			ctx := context.Background()
			v := contactStart(t, s, b, actor, "search-A")
			store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
			switch state.status {
			case "verified":
				if got, err := store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); err != nil || got.ControlReply != manager.WecomControlVerified {
					t.Fatal("verification reply", got.ControlReply, err)
				}
			case "cancelled":
				if _, err := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			setRetired := func(hours int) {
				t.Helper()
				if _, err := db.Exec(`UPDATE platform_wecom_verifications SET `+state.clock+`=now()-make_interval(hours=>$2) WHERE id=$1`, v.ID, hours); err != nil {
					t.Fatal(err)
				}
			}
			setRetired(71)
			in := contactInbound(b, v.Code, "callback-A")
			if got, err := store.ReceiveChannel(ctx, in); err != nil || got.ControlReply != state.reply {
				t.Fatal("retired code reply", got.ControlReply, err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM manager_channel_inbox WHERE provider_event_id=$1`, in.ProviderEventID).Scan(&count); err != nil || count != 0 {
				t.Fatal("retired code reached inbox", count, err)
			}
			setRetired(72)
			if got, err := store.ReceiveChannel(ctx, in); err != nil || got.ControlReply != "" {
				t.Fatal("same callback replay must remain silent", got.ControlReply, err)
			}
			fresh := contactInbound(b, v.Code, "callback-A")
			if got, err := store.ReceiveChannel(ctx, fresh); err != nil || got.ControlReply != "" {
				t.Fatal("outside-window message must have no verification reply", got.ControlReply, err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM manager_channel_inbox WHERE provider_event_id=$1`, fresh.ProviderEventID).Scan(&count); err != nil || count != 1 {
				t.Fatal("outside-window message did not follow ordinary ingress", count, err)
			}
			var mappings int
			if err := db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings WHERE verification_id=$1`, v.ID).Scan(&mappings); err != nil || (mappings == 1) != (state.status == "verified") {
				t.Fatal("retired message altered mapping", mappings, err)
			}
		})
	}
}

func TestWecomRetiredNumberReusePreservesMapping(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, s, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	candidate := service.WecomVerification{ID: service.NewID(), SearchUserID: "search-B", ExpiresAt: v.ExpiresAt, CodeHash: v.CodeHash, Ciphertext: []byte("test-only")}
	contacts := WecomContactStore{DB: db}
	if _, err := contacts.IssueWecomVerification(ctx, b, actor, candidate); err == nil {
		t.Fatal("number reused while active")
	}
	if got, err := store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); err != nil || got.ControlReply != manager.WecomControlVerified {
		t.Fatal(got.ControlReply, err)
	}
	if _, err := contacts.IssueWecomVerification(ctx, b, actor, candidate); err == nil {
		t.Fatal("number reused while old code is still recognized")
	}
	if _, err := db.Exec(`UPDATE platform_wecom_verifications SET completed_at=now()-interval '72 hours' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := contacts.IssueWecomVerification(ctx, b, actor, candidate); err != nil {
		t.Fatal("number could not be reused after retention", err)
	}
	if got, err := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false); err != nil || got.Status != "verified" || got.CallbackUserID != "callback-A" {
		t.Fatal("existing mapping was damaged", got.Status, err)
	}
}
func TestWecomContactVerificationIdempotencyCancelScopeAndConflict(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, s, b, actor, "search-A")
	again, e := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-A", v.ID)
	if e != nil || again.Code != v.Code {
		t.Fatal(again, e)
	}
	if _, e = s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-B", v.ID); e == nil {
		t.Fatal("idempotency conflict admitted")
	}
	cancelled, e := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, true)
	if e != nil || cancelled.Status != "cancelled" || cancelled.Code != "" {
		t.Fatal(cancelled, e)
	}
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	if _, e = store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	v = contactStart(t, s, b, actor, "search-A")
	if _, e = store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	other := contactStart(t, s, b, actor, "search-B")
	if _, e = store.ReceiveChannel(ctx, contactInbound(b, other.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	other, e = s.Get(ctx, b.WorkspaceID, actor, b.AgentID, other.ID, false)
	if e != nil || other.Status != "conflict" {
		t.Fatal(other, e)
	}
	if _, e = db.Exec(`UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false); e == nil {
		t.Fatal("old generation exposed")
	}
}
func TestWecomContactRequiresSearchAndExpiryAndRateLimit(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-A", service.NewID()); e == nil {
		t.Fatal("unsearched person admitted")
	}
	v := contactStart(t, s, b, actor, "search-A")
	var sealed []byte
	if e := db.QueryRow(`SELECT code_ciphertext FROM platform_wecom_verifications WHERE id=$1`, v.ID).Scan(&sealed); e != nil || strings.Contains(string(sealed), v.Code) {
		t.Fatal("plaintext secret stored", e)
	}
	if _, e := db.Exec(`UPDATE platform_wecom_verifications SET expires_at=now()-interval '1 minute' WHERE id=$1`, v.ID); e != nil {
		t.Fatal(e)
	}
	expired, e := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false)
	if e != nil || expired.Status != "expired" || expired.Code != "" {
		t.Fatal(expired, e)
	}
	v = contactStart(t, s, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	guess := "999999"
	if v.Code == guess {
		guess = "999998"
	}
	for range 11 {
		if _, e = store.ReceiveChannel(ctx, contactInbound(b, guess, "callback-A")); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	status, e := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false)
	if e != nil || status.Status != "pending" {
		t.Fatal("brute force limit failed", status, e)
	}
}

func TestWecomVerificationIssuanceLimitIs200PerRollingHour(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO platform_wecom_verifications
 (id,binding_id,identity_epoch,connection_generation,search_user_id,created_by,code_hash,code_ciphertext,status,expires_at,completed_at)
 SELECT gen_random_uuid(),$1,$2,$3,'seed-'||n,$4,'retired:seed-'||n,'','cancelled',now()+interval '1 day',now()
 FROM generate_series(1,199) AS n`, b.ID, b.IdentityEpoch, b.Generation, actor); err != nil {
		t.Fatal(err)
	}
	twoHundredth := contactStart(t, s, b, actor, "search-A")
	requestID := service.NewID()
	var failure *service.Error
	if _, err := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-B", requestID); !errors.As(err, &failure) || failure.Status != 429 || failure.Code != "wecom_verification_rate_limited" {
		t.Fatal("201st issuance must be limited", err)
	}
	if retry, err := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-A", twoHundredth.ID); err != nil || retry.ID != twoHundredth.ID || retry.Code != twoHundredth.Code {
		t.Fatal("idempotent replay must not consume issuance quota", err)
	}
	if _, err := db.Exec(`UPDATE platform_wecom_verifications SET created_at=now()-interval '61 minutes' WHERE binding_id=$1 AND search_user_id='seed-1'`, b.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := s.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-B", requestID); err != nil || next.ID != requestID {
		t.Fatal("issuance must resume when a prior code leaves the rolling hour", err)
	}
}

func TestWecomContactHTTPAdminCSRFAndNoStore(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	api := (&controller.Server{Channels: s.Channels, WecomContacts: s, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	base := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/channels/wecom/contacts"
	request := func(method, path, body string, auth, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			r.Header.Set("Cookie", "kiko_session=runtime-fixture-session")
		}
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", base+"/search?q=Alice", "", false, false); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", base+"/search?q=Alice", "", true, false); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	body := `{"search_user_id":"search-A","request_id":"` + service.NewID() + `"}`
	if w := request("POST", base+"/verifications", body, true, false); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("POST", base+"/verifications", body, true, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "ciphertext") || strings.Contains(w.Body.String(), "code_hash") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e := db.Exec(`UPDATE platform_members SET role='member' WHERE workspace_id=$1 AND user_id=$2`, b.WorkspaceID, actor); e != nil {
		t.Fatal(e)
	}
	if w = request("GET", base+"/search?q=Alice", "", true, false); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWecomContactCrossBindingAndEpochNeverReuseMapping(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, s, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	// An identity epoch change invalidates even a current-generation challenge.
	if _, e := db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	b.IdentityEpoch++
	if _, e := store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if _, e := s.Get(ctx, b.WorkspaceID, actor, b.AgentID, v.ID, false); e == nil {
		t.Fatal("old epoch verification exposed")
	}
}

type contactProviderFunc func(context.Context, string, string, string) (service.WecomContactSearch, error)

func (f contactProviderFunc) Search(ctx context.Context, a, b, c string) (service.WecomContactSearch, error) {
	return f(ctx, a, b, c)
}
func TestWecomContactLateProviderResponseCannotCrossCredentialGeneration(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	s.Provider = contactProviderFunc(func(ctx context.Context, a, secret, q string) (service.WecomContactSearch, error) {
		if _, e := db.ExecContext(ctx, `UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, b.ID); e != nil {
			return service.WecomContactSearch{}, e
		}
		return contactFake{}.Search(ctx, a, secret, q)
	})
	if _, e := s.Search(context.Background(), b.WorkspaceID, actor, b.AgentID, "Alice"); e == nil {
		t.Fatal("late source accepted")
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM platform_wecom_contacts`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestWecomContactConcurrentCodeConsumptionMapsAtMostOnce(t *testing.T) {
	db, s, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, s, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(s.Channels.Secret)}
	results := make(chan error, 2)
	for _, sender := range []string{"callback-A", "callback-B"} {
		go func(sender string) {
			_, e := store.ReceiveChannel(ctx, contactInbound(b, v.Code, sender))
			results <- e
		}(sender)
	}
	for range 2 {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
