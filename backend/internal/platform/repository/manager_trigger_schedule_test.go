package repository

import (
	"context"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"sync"
	"testing"
	"time"
)

func TestTriggerScheduleAtomicOccurrenceAndRecovery(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	s := m.Store.(*managerrepo.Store)
	ctx := context.Background()
	raw := json.RawMessage(`{"cron":"* * * * *","timezone":"Asia/Shanghai","description":"scheduled work","businessKey":{"mode":"empty"}}`)
	b, err := s.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "schedule", Enabled: true, Config: raw, AllowedActions: []string{"create_task"}})
	if err != nil {
		t.Fatal(err)
	}
	poll, err := s.PollTriggerSchedules(ctx, 100)
	if err != nil || len(poll.Items) != 1 || poll.Items[0].NextDue.IsZero() {
		t.Fatal(poll, err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if _, err = db.Exec(`UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due); err != nil {
		t.Fatal(err)
	}
	q := manager.TriggerScheduleTick{Version: b.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.TickTriggerSchedule(ctx, b.ID, q); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var events, tasks, spaces int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM manager_trigger_events),(SELECT count(*) FROM manager_tasks),(SELECT count(*) FROM manager_task_spaces)`).Scan(&events, &tasks, &spaces); err != nil || events != 1 || tasks != 1 || spaces != 1 {
		t.Fatal(events, tasks, spaces, err)
	}
	var next time.Time
	if err = db.QueryRow(`SELECT next_due FROM manager_triggers WHERE id=$1`, b.ID).Scan(&next); err != nil || !next.After(time.Now()) {
		t.Fatal(next, err)
	}
	if _, err = db.Exec(`UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due); err != nil {
		t.Fatal(err)
	}
	q.RecoveryStartedAt = time.Now().UTC()
	result, err := s.TickTriggerSchedule(ctx, b.ID, q)
	if err != nil || result.Receipt == nil || !result.Receipt.Duplicate {
		t.Fatal("registered occurrence failed recovery", result, err)
	}
	if _, err = db.Exec(`UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	q.ExpectedDue = due.Add(-time.Minute)
	result, err = s.TickTriggerSchedule(ctx, b.ID, q)
	if err != nil || result.Status != "skipped" {
		t.Fatal("unregistered offline occurrence caught up", result, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM manager_tasks`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatal("offline skip created task", tasks, err)
	}
}
