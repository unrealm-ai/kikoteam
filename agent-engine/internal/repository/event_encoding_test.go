package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestReplaceJSONNUL(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{`{"value":"plain"}`, `{"value":"plain"}`},
		{`{"\u0000":["\u0000\u0000","\u0001","\u2603"]}`, `{"\ufffd":["\ufffd\ufffd","\u0001","\u2603"]}`},
		{`["\\u0000","\\\u0000","\\\\u0000","\\\\\u0000"]`, `["\\u0000","\\\ufffd","\\\\u0000","\\\\\ufffd"]`},
		{`["\"\u0000","\n\u0000","\t\\u0000"]`, `["\"\ufffd","\n\ufffd","\t\\u0000"]`},
	} {
		data := []byte(c.input)
		replaceJSONNUL(data)
		if got := string(data); got != c.want {
			t.Errorf("replaceJSONNUL(%s) = %s, want %s", c.input, got, c.want)
		}
	}
}

func TestExecutionPersistsNULToolResultAndCompletion(t *testing.T) {
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	triggerInsert(t, db, fixtureID(20), 1, true, "user")
	turn, err := store.Claim(ctx, work, fixtureID(30))
	if err != nil {
		t.Fatal(err)
	}
	if turn == nil {
		t.Fatal("fixture input not claimed")
	}
	toolID := fixtureID(31)
	// A PDF read as text can yield real NULs, unlike the literal six-character sequence.
	const raw = "PDF\x00data" + `\u0000`
	output := map[string]any{"content": []any{map[string]string{"text": raw, "key\x00": "kept"}}}
	if err := store.Event(ctx, work, *turn, "agent.tool_use", map[string]any{"tool_use_id": toolID, "status": "running", "input": json.RawMessage(`{"path":"a\u0000b"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Event(ctx, work, *turn, "agent.tool_result", map[string]any{"tool_use_id": toolID, "status": "completed", "output": output}); err != nil {
		t.Fatal(err)
	}
	// This later result must carry on emitting normally, and marshal errors must not be swallowed.
	if err := store.Event(ctx, work, *turn, "agent.tool_result", map[string]any{"tool_use_id": fixtureID(32), "status": "completed", "output": make(chan int)}); err == nil {
		t.Fatal("invalid event unexpectedly persisted")
	}
	if err := store.Event(ctx, work, *turn, "agent.tool_result", map[string]any{"tool_use_id": fixtureID(32), "status": "completed", "output": "next result"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, work, *turn, "completed", "", "summary "+raw); err != nil {
		t.Fatal(err)
	}
	if got := output["content"].([]any)[0].(map[string]string)["text"]; got != raw {
		t.Fatalf("tool data mutated in memory: %q", got)
	}
	assertPersistedExecutionNUL(t, db, *turn, toolID)
}

func assertPersistedExecutionNUL(t *testing.T, db *sql.DB, turn Turn, toolID string) {
	t.Helper()
	const stored = "PDF�data" + `\u0000`
	var path, output, keyValue, toolStatus, next string
	if err := db.QueryRow(`SELECT event #>> '{input,path}' FROM manager_session_events WHERE event->>'tool_use_id'=$1 AND event->>'type'='agent.tool_use'`, toolID).Scan(&path); err != nil || path != "a�b" {
		t.Fatalf("persisted tool input path=%q err=%v", path, err)
	}
	if err := db.QueryRow(`SELECT event #>> '{output,content,0,text}', (event #> '{output,content,0}')->>'key�', event->>'status' FROM manager_session_events WHERE event->>'tool_use_id'=$1 AND event->>'type'='agent.tool_result'`, toolID).Scan(&output, &keyValue, &toolStatus); err != nil || output != stored || keyValue != "kept" || toolStatus != "completed" {
		t.Fatalf("persisted tool result output=%q key_value=%q status=%q err=%v", output, keyValue, toolStatus, err)
	}
	if err := db.QueryRow(`SELECT event->>'output' FROM manager_session_events WHERE event->>'tool_use_id'=$1 AND event->>'type'='agent.tool_result'`, fixtureID(32)).Scan(&next); err != nil || next != "next result" {
		t.Fatalf("persisted next tool result=%q err=%v", next, err)
	}
	var status, summary, inputStatus, eventSummary string
	if err := db.QueryRow(`SELECT t.status, t.result_summary, i.status FROM manager_turns t JOIN manager_inputs i ON i.id=t.input_id WHERE t.id=$1`, turn.ID).Scan(&status, &summary, &inputStatus); err != nil || status != "completed" || summary != "summary "+stored || inputStatus != "processed" {
		t.Fatalf("persisted turn status=%q summary=%q input_status=%q err=%v", status, summary, inputStatus, err)
	}
	if err := db.QueryRow(`SELECT event->>'result_summary' FROM manager_session_events WHERE event->>'turn_id'=$1 AND event->>'type'='turn.updated' AND event->>'status'='completed'`, turn.ID).Scan(&eventSummary); err != nil || eventSummary != summary {
		t.Fatalf("persisted turn event summary=%q err=%v", eventSummary, err)
	}
}
