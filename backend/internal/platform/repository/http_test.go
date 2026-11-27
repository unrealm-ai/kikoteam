package repository

import (
	"bytes"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPIdentityWorkspaceInviteLifecycle(t *testing.T) {
	db := workspaceTestDB(t)
	enableMailFixture(t, db, "")
	sink := &codeSink{}
	h := (&controller.Server{Auth: &service.AuthService{Store: &AuthStore{DB: db}, Mailer: sink, Secret: []byte("test-only-secret-test-only-secret")}, Workspaces: &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-secret-test-only-secret"), Store: NewWorkspaceStore(db), PublicURL: "https://kiko.example"}, CodeLoginEnabled: true, Origin: "https://kiko.example", SecureCookies: true}).Handler()
	request := func(method, path, body, cookie string) (*httptest.ResponseRecorder, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "https://kiko.example")
		r.Header.Set("X-CSRF-Protection", "1")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		if w.Code != 204 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return w, out
	}
	code, out := request("POST", "/api/v1/auth/email-codes", `{"email":"owner@example.test","purpose":"register"}`, "")
	if code.Code != 200 || out["debugCode"] != nil {
		t.Fatalf("code response %d %v", code.Code, out)
	}
	body, err := json.Marshal(map[string]any{"email": "owner@example.test", "name": "Owner", "password": "test-password", "challenge_id": out["challenge_id"], "code": sink.code})
	if err != nil {
		t.Fatal(err)
	}
	reg, user := request("POST", "/api/v1/auth/registrations", string(body), "")
	if reg.Code != 201 {
		t.Fatalf("register %d %v", reg.Code, user)
	}
	cookies := reg.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	cookie := cookies[0].String()
	created, ws := request("POST", "/api/v1/workspaces", `{"name":"Team","description":"Test"}`, cookie)
	if created.Code != 201 {
		t.Fatalf("create %d %v", created.Code, ws)
	}
	id := ws["id"].(string)
	linkResult, linkInfo := request("POST", "/api/v1/workspaces/"+id+"/invitations", `{"kind":"link"}`, cookie)
	if linkResult.Code != 201 || linkInfo["role"] != "member" || linkInfo["kind"] != "link" {
		t.Fatal("default link", linkResult.Code, linkInfo)
	}
	for _, invalid := range []string{`{"kind":"link","expires_in_days":0}`, `{"kind":"link","expires_in_days":181}`, `{"kind":"link","expires_in_days":1.5}`, `{"kind":"link","expires_in_days":null}`} {
		invalidResponse, _ := request("POST", "/api/v1/workspaces/"+id+"/invitations", invalid, cookie)
		if invalidResponse.Code != 400 {
			t.Fatal("invalid expiry accepted", invalid, invalidResponse.Code)
		}
	}
	invited, inv := request("POST", "/api/v1/workspaces/"+id+"/invitations", `{"email":"new@example.test","role":"member"}`, cookie)
	if invited.Code != 201 {
		t.Fatalf("invite %d %v", invited.Code, inv)
	}
	token := strings.Split(inv["link"].(string), "invitation_token=")[1]
	inspected, info := request("GET", "/api/v1/invitations/"+token, "", "")
	if inspected.Code != 200 || info["status"] != "valid" || info["contact_email"] != "owner@example.test" {
		t.Fatal(info)
	}
	if _, exists := info["email"]; exists {
		t.Fatal("recipient email leaked")
	}
	unknown, info := request("GET", "/api/v1/invitations/unknown", "", "")
	if unknown.Code != 200 || info["status"] != "unknown" || info["contact_email"] != nil {
		t.Fatal(info)
	}
	denied, info := request("POST", "/api/v1/invitations/"+token+"/accept", `{}`, cookie)
	if denied.Code != 403 {
		t.Fatalf("wrong recipient %d %v", denied.Code, info)
	}
	revoked, _ := request("POST", "/api/v1/workspaces/"+id+"/invitations/"+inv["id"].(string)+"/revoke", `{}`, cookie)
	if revoked.Code != 204 {
		t.Fatal(revoked.Code)
	}
	_, info = request("GET", "/api/v1/invitations/"+token, "", "")
	if info["status"] != "revoked" {
		t.Fatal(info)
	}
	r := httptest.NewRequest("POST", "/api/v1/workspaces", bytes.NewBufferString(`{"name":"Bad"}`))
	r.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	r = httptest.NewRequest("POST", "/api/v1/workspaces", strings.NewReader(`{"name":"x"}`+strings.Repeat(" ", 17000)))
	r.Header.Set("Cookie", cookie)
	r.Header.Set("X-CSRF-Protection", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("oversize accepted %d", w.Code)
	}
	r = httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Authorization", "Bearer something")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("mixed credentials allowed")
	}
}

func TestHTTPPasswordRegistrationWithoutSMTP(t *testing.T) {
	db := workspaceTestDB(t)
	auth := &service.AuthService{Store: &AuthStore{DB: db}, Mailer: &integration.SMTP{}, Secret: []byte("test-only-secret-test-only-secret")}
	ws := &service.WorkspaceService{EmailInvitationsEnabled: true, MailSecret: []byte("test-only-secret-test-only-secret"), Store: NewWorkspaceStore(db)}
	server := &controller.Server{Auth: auth, Workspaces: ws, Origin: "https://kiko.example"}
	h := server.Handler()
	req := func(method, path, body, cookie string) (*httptest.ResponseRecorder, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "https://kiko.example")
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return w, out
	}
	options, cfg := req("GET", "/api/v1/auth/options", "", "")
	if options.Code != 200 || cfg["password_registration_enabled"] != true || cfg["email_code_enabled"] != false || cfg["registration_verification_required"] != false {
		t.Fatal(cfg)
	}
	reg, body := req("POST", "/api/v1/auth/registrations", `{"method":"password","email":"local@example.test","name":"Local","password":"local-password"}`, "")
	if reg.Code != 201 {
		t.Fatalf("password registration %d %+v", reg.Code, body)
	}
	if body["user"].(map[string]any)["email_verified"] != false {
		t.Fatal("unverified email trusted")
	}
	cookie := reg.Result().Cookies()[0].String()
	me, body := req("GET", "/api/v1/me", "", cookie)
	if me.Code != 200 || body["user"].(map[string]any)["email_verified"] != false {
		t.Fatal(body)
	}
	created, body := req("POST", "/api/v1/workspaces", `{"name":"Local team"}`, cookie)
	if created.Code != 201 {
		t.Fatal(body)
	}
	path := "/api/v1/workspaces/" + body["id"].(string) + "/invitations"
	disabled, _ := req("POST", path, `{"kind":"email","email":"local@example.test"}`, cookie)
	if disabled.Code != 403 {
		t.Fatal("email capability not enforced", disabled.Code)
	}
	linkAllowed, _ := req("POST", path, `{"kind":"link"}`, cookie)
	if linkAllowed.Code != 201 {
		t.Fatal("link incorrectly disabled", linkAllowed.Code)
	}
	server.CodeLoginEnabled = true // Exercise legacy email invitation verification with email capability explicitly enabled.
	_, body = req("POST", "/api/v1/workspaces/"+body["id"].(string)+"/invitations", `{"email":"local@example.test","role":"member"}`, cookie)
	inviteToken := strings.Split(body["link"].(string), "invitation_token=")[1]
	denied, body := req("POST", "/api/v1/invitations/"+inviteToken+"/accept", `{}`, cookie)
	if denied.Code != 403 || body["error"].(map[string]any)["code"] != "email_verification_required" {
		t.Fatal(body)
	}
	dupe, body := req("POST", "/api/v1/auth/registrations", `{"method":"password","email":"local@example.test","name":"Attacker","password":"another-password"}`, "")
	if dupe.Code != 409 {
		t.Fatal(body)
	}
	login, body := req("POST", "/api/v1/auth/sessions", `{"method":"password","email":"local@example.test","password":"local-password"}`, "")
	if login.Code != 200 {
		t.Fatal(body)
	}
	auth.VerificationRequired = true
	forbidden, body := req("POST", "/api/v1/auth/registrations", `{"method":"password","email":"new@example.test","name":"Local","password":"local-password"}`, "")
	if forbidden.Code != 403 {
		t.Fatal(body)
	}
	_, cfg = req("GET", "/api/v1/auth/options", "", "")
	if cfg["password_registration_enabled"] != false || cfg["registration_verification_required"] != true {
		t.Fatal(cfg)
	}
}

func TestHTTPPasswordLoginVerifiesLegacyEmailWithoutReset(t *testing.T) {
	db := workspaceTestDB(t)
	enableMailFixture(t, db, "")
	sink := &codeSink{}
	auth := &service.AuthService{Store: &AuthStore{DB: db}, Mailer: sink, Secret: []byte("test-only-login-verification-secret")}
	const origin = "https://kiko.example"
	h := (&controller.Server{Auth: auth, Origin: origin}).Handler()
	const email, password = "legacy@example.test", "legacy-password"
	account, previousToken, err := auth.RegisterWithPassword(t.Context(), email, "Legacy", password, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_mail_settings SET verification_required=true WHERE singleton`)
	call := func(path string, body map[string]string, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, origin+"/api/v1/"+path, bytes.NewReader(data))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Protection", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("status = %d, want %d: %s", w.Code, status, w.Body)
		}
		if status != http.StatusOK && w.Header().Get("Set-Cookie") != "" {
			t.Fatal("issued session before verification")
		}
		return w
	}
	invalid := call("auth/sessions", map[string]string{"email": email, "method": "password", "password": "wrong-password"}, 401)
	if !strings.Contains(invalid.Body.String(), "invalid_credentials") || sink.code != "" {
		t.Fatal("wrong password did not fail without an email")
	}
	call("auth/email-codes", map[string]string{"email": email, "purpose": "verify_login"}, 400)
	pending := call("auth/sessions", map[string]string{"email": email, "method": "password", "password": password}, 202)
	var receipt service.ChallengeReceipt
	if err = json.Unmarshal(pending.Body.Bytes(), &receipt); err != nil || receipt.ID == "" || receipt.ResendAfter != 60 {
		t.Fatalf("invalid challenge receipt: %v", err)
	}
	code := sink.code
	if code == "" || strings.Contains(pending.Body.String(), "\"code\"") {
		t.Fatal("unexpected code delivery or exposure")
	}
	request := map[string]string{"email": email, "method": "code", "code": code, "challenge_id": receipt.ID}
	call("auth/sessions", request, 400)
	request["method"], request["password"] = "password", password
	request["code"] = "000000"
	if code == "000000" {
		request["code"] = "999999"
	}
	call("auth/sessions", request, 400)
	before, err := auth.Me(t.Context(), previousToken)
	if err != nil || before.EmailVerified {
		t.Fatalf("invalid code changed account state: %v", err)
	}
	request["code"] = code
	ok := call("auth/sessions", request, 200)
	cookies := ok.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing authenticated cookie")
	}
	verified, err := auth.Me(t.Context(), cookies[0].Value)
	if err != nil || verified.ID != account.ID || !verified.EmailVerified {
		t.Fatalf("new session is not verified: %v", err)
	}
	if _, err = auth.Me(t.Context(), previousToken); err == nil {
		t.Fatal("old session survived verification")
	}
	call("auth/sessions", request, 400)
	call("auth/sessions", map[string]string{"email": email, "method": "password", "password": password}, 200)
}
