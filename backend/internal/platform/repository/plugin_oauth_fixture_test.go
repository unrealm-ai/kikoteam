package repository

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// Two accounts expose the same tool and different data. The OAuth provider
// validates the client, redirect, PKCE verifier and intended MCP resource.
type oauthLedger struct {
	*httptest.Server
	mu               sync.Mutex
	codes            map[string]url.Values
	tokens           map[string]string
	refresh          map[string]string
	last             string
	reads, refreshes int
}

func newOAuthLedger(t *testing.T) *oauthLedger {
	f := &oauthLedger{codes: map[string]url.Values{}, tokens: map[string]string{}, refresh: map[string]string{}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}
func (f *oauthLedger) counts() (int, int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads, f.refreshes, f.last
}
func (f *oauthLedger) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resource := f.URL + "/ledger"
	w.Header().Set("Content-Type", "application/json")
	send := func(v any) {
		if e := json.NewEncoder(w).Encode(v); e != nil {
			http.Error(w, "encoding failed", http.StatusInternalServerError)
		}
	}
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/ledger":
		send(map[string]any{"resource": resource, "authorization_servers": []string{f.URL}})
		return
	case "/.well-known/oauth-authorization-server":
		send(map[string]any{"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token", "revocation_endpoint": f.URL + "/revoke", "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
		return
	case "/authorize":
		q := r.URL.Query()
		account, e := r.Cookie("ledger_login")
		if e != nil || (account.Value != "team" && account.Value != "private") || q.Get("code_challenge_method") != "S256" || q.Get("resource") != resource || q.Get("client_id") != "kiko" || q.Get("scope") != "ledger.read" {
			w.WriteHeader(400)
			return
		}
		redirect, e := url.Parse(q.Get("redirect_uri"))
		if e != nil {
			w.WriteHeader(400)
			return
		}
		code := service.NewID()
		q.Set("account", account.Value)
		f.codes[code] = q
		params := redirect.Query()
		params.Set("code", code)
		params.Set("iss", f.URL)
		params.Set("state", q.Get("state"))
		redirect.RawQuery = params.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
		return
	case "/token", "/revoke":
		id, key, ok := r.BasicAuth()
		if !ok || id != "kiko" || key != "fixture-app-secret" {
			w.WriteHeader(401)
			return
		}
		if e := r.ParseForm(); e != nil {
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/revoke" {
			delete(f.refresh, r.Form.Get("token"))
			delete(f.tokens, r.Form.Get("token"))
			send(map[string]any{})
			return
		}
		var who string
		if r.Form.Get("resource") == resource {
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				c := r.Form.Get("code")
				q := f.codes[c]
				delete(f.codes, c)
				v := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(v[:]) == q.Get("code_challenge") && r.Form.Get("redirect_uri") == q.Get("redirect_uri") {
					who = q.Get("account")
				}
			case "refresh_token":
				k := r.Form.Get("refresh_token")
				who = f.refresh[k]
				delete(f.refresh, k)
				f.refreshes++
			}
		}
		if who == "" {
			w.WriteHeader(400)
			send(map[string]string{"error": "invalid_grant"})
			return
		}
		access, refresh := service.NewID(), service.NewID()
		f.tokens[access] = who
		f.refresh[refresh] = who
		send(map[string]any{"access_token": access, "token_type": "Bearer", "refresh_token": refresh, "expires_in": 3600, "scope": "ledger.read"})
		return
	case "/ledger":
		who := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if who == "" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			ID     json.RawMessage
			Method string
			Params struct{ Name string }
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "ledger", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "latest_receipt", "description": "查询当前业务账户最新的对账回执编号", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			if req.Params.Name != "latest_receipt" {
				w.WriteHeader(400)
				return
			}
			f.reads++
			f.last = who + "-" + service.NewID()[:8]
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": f.last}}}
		default:
			w.WriteHeader(400)
			return
		}
		send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		return
	default:
		w.WriteHeader(404)
	}
}
