package repository

import (
	"context"
	"database/sql"
	"testing"
)

func TestCompletedChatMessageUpdatesConversationRecency(t *testing.T) {
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	triggerInsert(t, db, fixtureID(20), 1, true, "user")
	turn, err := store.Claim(ctx, work, fixtureID(30))
	if err != nil || turn == nil {
		t.Fatal(turn, err)
	}
	fields := func(status string, content any) map[string]any {
		return map[string]any{"status": status, "content": content}
	}
	content := []map[string]string{{"type": "text", "text": "reply"}}
	if err := store.Event(ctx, work, *turn, "agent.message", fields("interrupted", content)); err != nil {
		t.Fatal(err)
	}
	if err := store.Event(ctx, work, *turn, "agent.message", fields("completed", make(chan int))); err == nil {
		t.Fatal("invalid reply was committed")
	}
	var recency sql.NullTime
	if err := db.QueryRow(`SELECT last_message_at FROM manager_conversations WHERE id=$1`, work.ConversationID).Scan(&recency); err != nil || recency.Valid {
		t.Fatalf("non-completed replies changed recency: %v, %v", recency, err)
	}
	if err := store.Event(ctx, work, *turn, "agent.message", fields("completed", content)); err != nil {
		t.Fatal(err)
	}
	var matches bool
	if err := db.QueryRow(`SELECT c.last_message_at=e.created_at
 FROM manager_conversations c JOIN manager_session_events e ON e.session_id=$2
 WHERE c.id=$1 AND e.event->>'type'='agent.message' AND e.event->>'status'='completed'`, work.ConversationID, work.SessionID).Scan(&matches); err != nil || !matches {
		t.Fatalf("completed reply and recency must be committed together: %v, %v", matches, err)
	}
}
