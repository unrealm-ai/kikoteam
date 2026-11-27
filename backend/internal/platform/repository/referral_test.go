package repository

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	mprepo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestReferralHTTPAndAtomicRewards(t *testing.T) {
	db := workspaceTestDB(t)
	ctx := t.Context()
	store := &ReferralStore{DB: db}
	sink := &codeSink{}
	auth := &service.AuthService{
		Store:  &AuthStore{DB: db, InitCredits: mprepo.Signup, RewardReferral: mprepo.RewardReferral},
		Mailer: sink, Secret: []byte("test-referral-signup-mail-secret"),
	}
	const origin = "https://kiko.example.test"
	h := (&controller.Server{Auth: auth, Referrals: store, Origin: origin}).Handler()
	req := func(method, path, cookie, body string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+"/api/v1"+path, strings.NewReader(body))
		if csrf {
			r.Header.Set("X-CSRF-Protection", "1")
			r.Header.Set("Origin", origin)
		}
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	call := func(method, path, cookie string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := req(method, path, cookie, string(payload), true)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	register := func(email, code, method, otp, challenge string, want int) (service.Account, string) {
		t.Helper()
		body := map[string]string{"email": email, "name": "Test Account", "password": "test-long-password", "method": method}
		if code != "" {
			body["referral_code"] = code
		}
		if method == "code" {
			body["code"], body["challenge_id"] = otp, challenge
		}
		w := call(http.MethodPost, "/auth/registrations", "", body, want)
		if want != http.StatusCreated {
			return service.Account{}, ""
		}
		var res struct{ User service.Account }
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		cookie := w.Result().Cookies()[0]
		return res.User, cookie.Name + "=" + cookie.Value
	}
	assertReward := func(account string, count, units int64) {
		t.Helper()
		var got, total int64
		err := db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(amount_units),0) FROM platform_credit_ledger
 WHERE account_id=$1 AND kind='referral_bonus'`, account).Scan(&got, &total)
		if err != nil || got != count || total != units {
			t.Fatalf("reward count=%d units=%d: %v", got, total, err)
		}
	}
	assertNoAttribution := func(id string) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform_referral_attributions WHERE new_account_id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected referral attribution count=%d: %v", count, err)
		}
	}
	reset := func(email string) {
		t.Helper()
		c, err := auth.RequestCode(ctx, email, "reset_password", "reset:"+email)
		if err != nil {
			t.Fatal(err)
		}
		call(http.MethodPost, "/auth/password-reset", "", map[string]string{"email": email, "code": sink.code, "challenge_id": c.ID, "new_password": "another-long-password"}, http.StatusNoContent)
	}

	admin, adminCookie := register("admin@example.test", "", "password", "", "", 201)
	enableMailFixture(t, db, admin.ID)
	inviter, cookie := register("inviter@example.test", "", "password", "", "", 201)
	const settingsPath = "/platform/referral-settings"
	const linkPath = "/referrals/me"
	call(http.MethodGet, linkPath, "", nil, 401)
	call(http.MethodGet, settingsPath, cookie, nil, 403)
	w := call(http.MethodPost, linkPath, cookie, nil, 200)
	if w.Body.String() != "{\"enabled\":false}\n" {
		t.Fatalf("default must be disabled: %s", w.Body)
	}
	on := map[string]any{"expected_version": 0, "enabled": true}
	call(http.MethodPatch, settingsPath, cookie, on, 403)
	if w = req(http.MethodPatch, settingsPath, adminCookie, `{"expected_version":0,"enabled":true}`, false); w.Code != 403 {
		t.Fatalf("CSRF: %s", w.Body)
	}
	call(http.MethodPatch, settingsPath, adminCookie, on, 200)
	call(http.MethodPatch, settingsPath, adminCookie, on, 409)
	var first service.ReferralLink
	w = call(http.MethodGet, linkPath, cookie, nil, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || !first.Enabled || first.Code != "" {
		t.Fatalf("link status before generation: %+v %v", first, err)
	}

	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- req(http.MethodPost, linkPath, cookie, "{}", true)
		}()
	}
	wg.Wait()
	close(results)
	var code string
	for result := range results {
		var l service.ReferralLink
		if err := json.Unmarshal(result.Body.Bytes(), &l); result.Code != 200 || err != nil || !l.Enabled || l.Code == "" || (code != "" && code != l.Code) {
			t.Fatalf("concurrent link creation: %d %s", result.Code, result.Body)
		}
		code = l.Code
	}

	otpEmail := "verified@example.test"
	challenge, err := auth.RequestCode(ctx, otpEmail, "register", "otp")
	if err != nil {
		t.Fatal(err)
	}
	otp := sink.code
	signup, _ := register(otpEmail, code, "code", otp, challenge.ID, 201)
	if !signup.EmailVerified {
		t.Fatal("code registration must verify email")
	}
	register(otpEmail, code, "code", otp, challenge.ID, 400)
	assertReward(inviter.ID, 1, 100*proxy.Units)
	assertReward(admin.ID, 0, 0)

	var owner, inviteMembership int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE w.personal_owner_id=$1 AND m.role='owner'),
 count(*) FILTER (WHERE w.personal_owner_id=$2) FROM platform_members m
 JOIN platform_workspaces w ON w.id=m.workspace_id WHERE m.user_id=$1`, signup.ID, inviter.ID).Scan(&owner, &inviteMembership); err != nil || owner != 1 || inviteMembership != 0 {
		t.Fatalf("personal workspace owner=%d invited membership=%d: %v", owner, inviteMembership, err)
	}
	call(http.MethodPost, "/auth/sessions", "", map[string]string{"email": inviter.Email, "method": "password", "password": "test-long-password"}, 200)
	assertNoAttribution(inviter.ID)
	register(inviter.Email, code, "password", "", "", 409)
	pending, _ := register("pending@example.test", code, "password", "", "", 201)
	if pending.EmailVerified {
		t.Fatal("password registration must not establish mailbox ownership")
	}
	assertReward(inviter.ID, 1, 100*proxy.Units)
	reset(pending.Email)
	assertReward(inviter.ID, 2, 200*proxy.Units)
	reviewExec(t, db, `UPDATE platform_email_challenges SET created_at=now()-interval '61 seconds' WHERE email=$1`, pending.Email)
	reset(pending.Email)
	assertReward(inviter.ID, 2, 200*proxy.Units)

	last, _ := register("before-close@example.test", code, "password", "", "", 201)
	call(http.MethodPatch, settingsPath, adminCookie, map[string]any{"expected_version": 1, "enabled": false}, 200)
	w = call(http.MethodGet, linkPath, cookie, nil, 200)
	if w.Body.String() != "{\"enabled\":false}\n" {
		t.Fatalf("closed activity returned a code: %s", w.Body)
	}
	closed, _ := register("while-closed@example.test", code, "password", "", "", 201)
	assertNoAttribution(closed.ID)
	call(http.MethodPatch, settingsPath, adminCookie, map[string]any{"expected_version": 2, "enabled": true}, 200)
	reset(closed.Email)
	reset(last.Email)
	assertReward(inviter.ID, 2, 200*proxy.Units)
	w = call(http.MethodPost, linkPath, cookie, nil, 200)
	if !strings.Contains(w.Body.String(), code) {
		t.Fatalf("reopening rotated the code: %s", w.Body)
	}
	unknown, _ := register("unknown-code@example.test", service.NewID(), "password", "", "", 201)
	assertNoAttribution(unknown.ID)
	reviewExec(t, db, `UPDATE platform_referral_links SET revoked_at=now() WHERE code=$1`, code)
	revoked, _ := register("revoked-code@example.test", code, "password", "", "", 201)
	assertNoAttribution(revoked.ID)
	assertReward(inviter.ID, 2, 200*proxy.Units)

	var newLink service.ReferralLink
	w = call(http.MethodPost, linkPath, adminCookie, nil, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &newLink); err != nil || newLink.Code == "" {
		t.Fatalf("admin referral link: %v", err)
	}
	legacy, _ := register("verify-at-login@example.test", newLink.Code, "password", "", "", 201)
	assertReward(admin.ID, 0, 0)
	reviewExec(t, db, `UPDATE platform_mail_settings SET verification_required=true WHERE singleton`)
	login := map[string]string{"method": "password", "email": legacy.Email, "password": "test-long-password"}
	w = call(http.MethodPost, "/auth/sessions", "", login, 202)
	var receipt service.ChallengeReceipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.ID == "" {
		t.Fatalf("login verification receipt: %v", err)
	}
	assertReward(admin.ID, 0, 0)
	login["code"], login["challenge_id"] = sink.code, receipt.ID
	call(http.MethodPost, "/auth/sessions", "", login, 200)
	assertReward(admin.ID, 1, 100*proxy.Units)
	call(http.MethodPost, "/auth/sessions", "", login, 400)
	assertReward(admin.ID, 1, 100*proxy.Units)
}
