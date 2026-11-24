package taskdelegation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type catalogDelegationStore struct {
	manager.DelegationStore
	manager.ArtifactStore
	scope                manager.DelegationScope
	calls, artifactCalls int
}

func (s *catalogDelegationStore) DelegationSource(context.Context, manager.DelegationIdentity) (manager.DelegationScope, error) {
	s.calls++
	return s.scope, nil
}

func (s *catalogDelegationStore) SelectArtifacts(_ context.Context, scope manager.DelegationScope, ids []string) (any, error) {
	if scope.Channel == nil {
		return nil, manager.Fail("artifact_forbidden", 403, "missing channel")
	}
	s.artifactCalls++
	return map[string]any{"artifact_ids": ids}, nil
}

func TestStableDelegationCatalogPreservesScope(t *testing.T) {
	secret := strings.Repeat("s", 32)
	id := manager.DelegationIdentity{SessionID: manager.NewID(), EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
	input := manager.NewID()
	token, err := manager.SignDelegation(secret, id)
	if err != nil {
		t.Fatal(err)
	}
	store := &catalogDelegationStore{}
	h, err := Handler(&manager.Delegation{Store: store, Secret: secret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	type reply struct {
		Result struct {
			Tools      []json.RawMessage `json:"tools"`
			IsError    bool              `json:"isError"`
			Structured map[string]any    `json:"structuredContent"`
		} `json:"result"`
	}
	request := func(path, authorization, grant, grantInput, raw string, status int) reply {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation"+path, strings.NewReader(raw))
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			r.Header.Set("Authorization", "Bearer "+authorization)
		}
		r.Header.Set("X-Kiko-Chat-Catalog", grant)
		r.Header.Set("X-Kiko-Catalog-Input", grantInput)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != status {
			t.Fatalf("%s: status=%d: %s", path, out.Code, out.Body)
		}
		var v reply
		if status == 200 {
			if err := json.Unmarshal(out.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
		}
		return v
	}
	const list = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	const call = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_delegation_create_session","arguments":{"instruction":"test only"}}}`
	for _, isChannel := range []bool{false, true} {
		store.scope = manager.DelegationScope{}
		if isChannel {
			store.scope.Channel = &manager.ChannelActor{ID: manager.NewID(), Generation: 1}
		}
		ordinary := request("?catalog=stable", token, "", "", list, 200).Result.Tools
		store.scope.ReportOnly = true
		report := request("?catalog=stable&mode=report", token, "", "", list, 200).Result.Tools
		legacy := request("?mode=report", token, "", "", list, 200).Result.Tools
		wantLegacy := 0
		if isChannel {
			wantLegacy = 1
		}
		if len(ordinary) == 0 || !reflect.DeepEqual(ordinary, report) || len(legacy) != wantLegacy {
			t.Fatalf("catalog mismatch: ordinary=%d report=%d legacy=%d", len(ordinary), len(report), len(legacy))
		}
		if isChannel {
			callArtifact := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"artifact_select","arguments":{"artifact_ids":["` + manager.NewID() + `"]}}}`
			before := store.artifactCalls
			result := request("?catalog=stable&mode=report", token, "", "", callArtifact, 200).Result
			if result.IsError || result.Structured["artifact_ids"] == nil || store.artifactCalls != before+1 {
				t.Fatal("authorized channel report artifact call was not routed to the existing store")
			}
		}
		store.scope.ReportOnly = false // A committed user steer must not widen the report endpoint.
		before := store.calls
		denied := request("?catalog=stable&mode=report", token, "", "", call, 200)
		if !denied.Result.IsError || store.calls != before+1 || !catalogDenied(denied.Result.Structured) {
			t.Fatal("report-only request reached Manager call")
		}
		grant, err := manager.SignChatToolCatalog(secret, manager.ChatToolCatalogGrant{Identity: id, InputID: input, Family: manager.ChatToolCatalogDelegation, Channel: isChannel})
		if err != nil {
			t.Fatal(err)
		}
		store.calls = 0
		catalogOnly := request("?catalog=stable", "", grant, input, list, 200)
		if !reflect.DeepEqual(ordinary, catalogOnly.Result.Tools) || store.calls != 0 {
			t.Fatal("schema-only grant does not list the scoped stable catalog")
		}
		denied = request("?catalog=stable", "", grant, input, call, 200)
		if !denied.Result.IsError || store.calls != 0 || !catalogDenied(denied.Result.Structured) {
			t.Fatal("schema-only grant unexpectedly reached Manager")
		}
		request("", "", grant, input, list, 401)
		request("?catalog=stable", "", grant, manager.NewID(), list, 401)
		request("?catalog=stable", "invalid", grant, input, list, 401)
	}
}

func catalogDenied(result map[string]any) bool {
	err, _ := result["error"].(map[string]any)
	return err["code"] == "user_authorization_required"
}

func TestStableDelegationCatalogRejectsExplicitAmbiguousAuthorization(t *testing.T) {
	for _, auth := range [][]string{{""}, {"Bearer invalid"}, {"Bearer one", "Bearer two"}} {
		r := httptest.NewRequest(http.MethodPost, "/mcp/task-delegation?catalog=stable", nil)
		for _, value := range auth {
			r.Header.Add("Authorization", value)
		}
		a, err := authorizeToolAccess(t.Context(), r, &manager.Delegation{})
		if err == nil || a.schemaOnly {
			t.Fatal("invalid Authorization must not permit a catalog-only fallback")
		}
	}
}
