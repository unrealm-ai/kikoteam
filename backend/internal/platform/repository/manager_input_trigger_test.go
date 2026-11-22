package repository

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestManagerInputTriggerWriteReadRoundtrip(t *testing.T) {
	db, s, mux, w, actor, agent, cookie := managerFixture(t)
	ctx := context.Background()
	conversation, err := s.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	chat, _, err := s.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: conversation.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	for _, tc := range []struct {
		name, kind string
		trigger    *bool
		want       bool
	}{
		{"default", "", nil, true}, {"system silent", "system", &disabled, false}, {"confirmation silent", "human_confirmation", &disabled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := manager.NewID()
			err = s.Store.WithSession(ctx, manager.Target{Workspace: w, Actor: actor, Session: chat.ID}, func(tx manager.SessionTx) error {
				input, e := tx.InsertInput(manager.Input{ID: id, SessionID: chat.ID, SubmittedBy: actor, Text: "fixture", Status: "queued", DeliveryMode: "steer", SourceKind: tc.kind, TriggerAgentLoop: tc.trigger, CreatedAt: time.Now().UTC()})
				if e != nil {
					return e
				}
				if input.TriggerAgentLoop == nil || *input.TriggerAgentLoop != tc.want {
					t.Fatalf("write default mismatch %+v", input)
				}
				return tx.Event("input.updated", &id, nil, manager.InputFields(input))
			})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/workspaces/" + w + "/sessions/" + chat.ID + "/inputs/" + id
			request := httptest.NewRequest("GET", path, nil)
			request.Header.Set("Cookie", cookie)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			var actual map[string]any
			if err = json.Unmarshal(response.Body.Bytes(), &actual); err != nil || response.Code != 200 {
				t.Fatal(response.Code, response.Body.String(), err)
			}
			kind := tc.kind
			if kind == "" {
				kind = "user"
			}
			if actual["trigger_agent_loop"] != tc.want || actual["source_kind"] != kind {
				t.Fatalf("public read lost fields %+v", actual)
			}
			var eventTrigger bool
			if err = db.QueryRowContext(ctx, `SELECT (event->>'trigger_agent_loop')::boolean FROM manager_session_events WHERE session_id=$1 AND event->>'input_id'=$2 AND event->>'type'='input.updated' ORDER BY seq DESC LIMIT 1`, chat.ID, id).Scan(&eventTrigger); err != nil || eventTrigger != tc.want {
				t.Fatal("event lost flag", eventTrigger, err)
			}
		})
	}
}

func TestManagerSilentInputsDoNotReserveEngine(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	conversation, err := m.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := m.CreateSession(ctx, w, actor, "", manager.CreateSession{ConversationID: conversation.ID, Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	silent := false
	err = m.Store.WithSession(ctx, manager.Target{Workspace: w, Actor: actor, Session: session.ID}, func(tx manager.SessionTx) error {
		_, e := tx.InsertInput(manager.Input{ID: manager.NewID(), SessionID: session.ID, SubmittedBy: actor, Text: "reminder", SourceKind: "system", TriggerAgentLoop: &silent, CreatedAt: time.Now().UTC()})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	store := m.Store.(*managerrepo.Store)
	if err = store.RegisterEngine(ctx, manager.Engine{ID: manager.NewID(), HostID: "silent-test", Endpoint: "http://127.0.0.1:1", Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var assigned bool
	if err = db.QueryRow(`SELECT engine_id IS NOT NULL FROM manager_sessions WHERE id=$1`, session.ID).Scan(&assigned); err != nil || assigned {
		t.Fatal(assigned, err)
	}
	if _, _, err = m.SubmitInput(ctx, w, actor, session.ID, "trigger", "start"); err != nil {
		t.Fatal(err)
	}
	if err = store.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT engine_id IS NOT NULL FROM manager_sessions WHERE id=$1`, session.ID).Scan(&assigned); err != nil || !assigned {
		t.Fatal(assigned, err)
	}
}
