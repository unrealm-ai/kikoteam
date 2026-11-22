package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	repo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestConfirmationExpiryStopAndConcurrentAnswers(t *testing.T) {
	db, svc, _, w, actor, agent, _ := managerFixture(t)
	ctx := context.Background()
	c, err := svc.CreateConversation(ctx, w, actor, manager.CreateConversation{AgentID: agent})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := svc.CreateSession(ctx, w, actor, "first", manager.CreateSession{ConversationID: c.ID, Mode: "task", Input: &struct {
		Text string `json:"text"`
	}{Text: "question"}})
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewID()
	if _, err = db.Exec(`INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at) VALUES($1,$2,$3,$4,1,$5,1,'interrupted',now())`, turn, w, task.ID, *task.InitialInputID, manager.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE manager_inputs SET turn_id=$2,status='processed',finished_at=now() WHERE id=$1`, *task.InitialInputID, turn); err != nil {
		t.Fatal(err)
	}
	seed := func(expires *time.Time) string {
		id := manager.NewID()
		actions, err := json.Marshal([]manager.RequiredAction{{Type: "confirmation", RequestID: id, TurnID: turn, Title: "报告", Message: "是否继续？", ExpiresAt: expires}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE manager_sessions SET required_actions=$2 WHERE id=$1`, task.ID, actions); err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkUnavailable := func(id string) {
		yes := true
		_, _, err := svc.AnswerConfirmation(ctx, w, actor, task.ID, manager.NewID(), manager.Confirmation{RequestID: id, Confirmed: &yes})
		var e *manager.Error
		if !errors.As(err, &e) || e.Code != "confirmation_unavailable" {
			t.Fatal(err)
		}
	}
	expired := time.Now().Add(-time.Minute)
	id := seed(&expired)
	checkUnavailable(id)
	if err = (&repo.Store{DB: db}).Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := svc.Store.Session(ctx, w, actor, task.ID)
	if err != nil || len(session.Execution.RequiredActions) != 0 {
		t.Fatal(session.Execution, err)
	}
	id = seed(nil)
	if _, err = svc.ControlSession(ctx, w, actor, task.ID, false); err != nil {
		t.Fatal(err)
	}
	checkUnavailable(id)
	id = seed(nil)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, choice := range []bool{true, false} {
		wg.Go(func() {
			_, _, err := svc.AnswerConfirmation(ctx, w, actor, task.ID, manager.NewID(), manager.Confirmation{RequestID: id, Confirmed: &choice})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var ok, conflict int
	for err := range results {
		if err == nil {
			ok++
			continue
		}
		var e *manager.Error
		if errors.As(err, &e) && e.Code == "confirmation_conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
