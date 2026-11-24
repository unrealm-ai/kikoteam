package humanconfirmation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanMCPStore struct {
	calls []string
	scope service.HumanRuntimeScope
}

func (s *humanMCPStore) check(name string, scope service.HumanRuntimeScope) (any, error) {
	s.calls = append(s.calls, name)
	s.scope = scope
	return map[string]bool{"called": true}, nil
}

func (s *humanMCPStore) HumanChatList(_ context.Context, scope service.HumanRuntimeScope, _ service.HumanListQuery) (any, error) {
	return s.check("list", scope)
}

func (s *humanMCPStore) HumanTaskTool(_ context.Context, scope service.HumanRuntimeScope, op, _ string, _ *service.HumanQuestion) (any, error) {
	return s.check(op, scope)
}

func (s *humanMCPStore) HumanChatSubmission(_ context.Context, scope service.HumanRuntimeScope, _, _ string, answer *service.HumanAnswer) (any, error) {
	if answer == nil {
		return s.check("receipt", scope)
	}
	return s.check("submit", scope)
}

type humanMCPFixture struct {
	mux                *http.ServeMux
	store              *humanMCPStore
	input, catalog     string
	chatAuth, taskAuth string
	delegationCatalog  string
	identity           service.DelegationIdentity
}

func newHumanMCPFixture(t *testing.T) humanMCPFixture {
	t.Helper()
	secret := strings.Repeat("t", 40)
	f := humanMCPFixture{
		mux: http.NewServeMux(), store: &humanMCPStore{}, input: service.NewID(),
		identity: service.DelegationIdentity{
			SessionID: service.NewID(), EngineID: service.NewID(),
			AssignmentID: service.NewID(), Generation: 1,
		},
	}
	if err := Register(f.mux, f.store, secret); err != nil {
		t.Fatal(err)
	}
	var err error
	f.chatAuth, err = service.SignHumanMCP(secret, "chat", f.input, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	f.taskAuth, err = service.SignHumanMCP(secret, "task", f.input, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	grant := service.ChatToolCatalogGrant{Identity: f.identity, InputID: f.input, Family: service.ChatToolCatalogHuman}
	f.catalog, err = service.SignChatToolCatalog(secret, grant)
	if err != nil {
		t.Fatal(err)
	}
	grant.Family = service.ChatToolCatalogDelegation
	f.delegationCatalog, err = service.SignChatToolCatalog(secret, grant)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f humanMCPFixture) request(path, payload string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

const listMCP = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
const chatMCP = "/mcp/human-confirmations/chat"
const taskMCP = "/mcp/human-confirmations/task"
const stableMCP = chatMCP + "?catalog=stable"

func (f humanMCPFixture) catalogHeaders() map[string]string {
	return map[string]string{"X-Kiko-Chat-Catalog": f.catalog, "X-Kiko-Catalog-Input": f.input}
}

func (f humanMCPFixture) authHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token, "X-Kiko-Human-Input": f.input}
}

func TestStableHumanMCPCatalogAdvertisesExactlyTheAuthorizedSchemas(t *testing.T) {
	f := newHumanMCPFixture(t)
	tools := func(response *httptest.ResponseRecorder) []json.RawMessage {
		t.Helper()
		var reply struct {
			Result struct {
				Tools []json.RawMessage
			}
		}
		if err := json.Unmarshal(response.Body.Bytes(), &reply); response.Code != http.StatusOK || err != nil || len(reply.Result.Tools) != 3 {
			t.Fatalf("invalid catalog response HTTP %d: %s (%v)", response.Code, response.Body, err)
		}
		return reply.Result.Tools
	}
	authorized := tools(f.request(chatMCP, listMCP, f.authHeaders(f.chatAuth)))
	advertised := tools(f.request(stableMCP, listMCP, f.catalogHeaders()))
	stableAuthorized := tools(f.request(stableMCP, listMCP, f.authHeaders(f.chatAuth)))
	if !reflect.DeepEqual(authorized, advertised) || !reflect.DeepEqual(authorized, stableAuthorized) {
		t.Fatal("catalog-only tool schemas differ from authorized chat schemas")
	}
	if len(f.store.calls) != 0 {
		t.Fatal("discovery touched the confirmation store")
	}
}

func TestStableHumanMCPCatalogDeniesAllCallsWithoutStore(t *testing.T) {
	f := newHumanMCPFixture(t)
	requestID := service.NewID()
	calls := []struct{ name, args, storeCall string }{
		{"list_pending_human_confirmations", "{}", "list"},
		{"submit_human_confirmation", fmt.Sprintf(`{"request_id":%q,"operationId":"fixture","version":"1","decision":"approve","questionDigest":%q,"association":"current_message","answers":[]}`, requestID, strings.Repeat("a", 64)), "submit"},
		{"get_human_confirmation_receipt", fmt.Sprintf(`{"request_id":%q,"operationId":"fixture"}`, requestID), "receipt"},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, c.name, c.args)
			before := len(f.store.calls)
			response := f.request(stableMCP, payload, f.catalogHeaders())
			var reply struct {
				Result struct {
					IsError           bool
					StructuredContent struct {
						Error struct{ Code string }
					}
				}
			}
			if err := json.Unmarshal(response.Body.Bytes(), &reply); response.Code != http.StatusOK || err != nil || !reply.Result.IsError ||
				reply.Result.StructuredContent.Error.Code != "human_tool_forbidden" || len(f.store.calls) != before {
				t.Fatalf("catalog-only call reached authority or failed to produce typed denial: HTTP %d %s (%v)", response.Code, response.Body, err)
			}
			response = f.request(stableMCP, payload, f.authHeaders(f.chatAuth))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"called":true`) ||
				len(f.store.calls) != before+1 || f.store.calls[before] != c.storeCall ||
				f.store.scope.InputID != f.input || f.store.scope.Identity != f.identity {
				t.Fatalf("valid current chat identity did not use original path: HTTP %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestStableHumanMCPCatalogCannotReplaceAuthorization(t *testing.T) {
	f := newHumanMCPFixture(t)
	for _, tc := range []struct {
		name, path string
		override   map[string]string
	}{
		{"legacy chat path", chatMCP, nil},
		{"task path", taskMCP + "?catalog=stable", nil},
		{"wrong input", stableMCP, map[string]string{"X-Kiko-Catalog-Input": service.NewID()}},
		{"wrong family", stableMCP, map[string]string{"X-Kiko-Chat-Catalog": f.delegationCatalog}},
		{"invalid catalog", stableMCP, map[string]string{"X-Kiko-Chat-Catalog": "forged"}},
		{"mixed invalid authorization", stableMCP, map[string]string{"Authorization": "Bearer forged"}},
		{"mixed empty authorization", stableMCP, map[string]string{"Authorization": ""}},
		{"browser origin", stableMCP, map[string]string{"Origin": "https://fixture.example"}},
		{"browser cookie", stableMCP, map[string]string{"Cookie": "fixture=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := f.catalogHeaders()
			for k, v := range tc.override {
				headers[k] = v
			}
			if res := f.request(tc.path, listMCP, headers); res.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected discovery authorization HTTP %d: %s", res.Code, res.Body)
			}
		})
	}
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_human_confirmation_scopes","arguments":{}}}`
	if res := f.request(taskMCP, payload, f.authHeaders(f.taskAuth)); res.Code != http.StatusOK ||
		!strings.Contains(res.Body.String(), `"called":true`) || !reflect.DeepEqual(f.store.calls, []string{"recipients"}) {
		t.Fatalf("original task authorization failed HTTP %d: %s", res.Code, res.Body)
	}
}

func TestStableHumanMCPCatalogRejectsDuplicateAuthorization(t *testing.T) {
	f := newHumanMCPFixture(t)
	r := httptest.NewRequest(http.MethodPost, stableMCP, strings.NewReader(listMCP))
	for k, v := range f.authHeaders(f.chatAuth) {
		r.Header.Set(k, v)
	}
	r.Header.Add("Authorization", "Bearer different")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || len(f.store.calls) != 0 {
		t.Fatal("ambiguous Authorization should not gain any human tool authority")
	}
}
