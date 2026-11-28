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
	var tickStart time.Time
	if err = db.QueryRow(`SELECT clock_timestamp()`).Scan(&tickStart); err != nil {
		t.Fatal(err)
	}
	due := tickStart.UTC().Truncate(time.Minute).Add(-time.Minute)
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
	var next, tickEnd time.Time
	if err = db.QueryRow(`SELECT next_due,clock_timestamp() FROM manager_triggers WHERE id=$1`, b.ID).Scan(&next, &tickEnd); err != nil {
		t.Fatal(err)
	}
	// The winning tick schedules the first whole minute after its database
	// clock sample. The remaining contenders may finish in the next minute.
	if !next.Equal(next.Truncate(time.Minute)) || !next.After(tickStart) || next.After(tickEnd.Truncate(time.Minute).Add(time.Minute)) {
		t.Fatalf("next due %v outside the tick interval %v..%v", next, tickStart, tickEnd)
	}
	if _, err = db.Exec(`UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, b.ID, due); err != nil {
		t.Fatal(err)
	}
	q.RecoveryStartedAt = tickEnd
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
	history, err := s.TriggerEvents(ctx, w, actor, agent, b.ID, time.Time{}, "", 20)
	if err != nil || len(history) != 2 || history[0].Status != "rejected" || history[0].Reason != "missed_while_offline" ||
		history[0].ScheduledFor == nil || !history[0].ScheduledFor.Equal(q.ExpectedDue) || history[0].Receipt != nil ||
		history[1].Status != "accepted" || history[1].Receipt == nil || history[1].Receipt.TaskID == "" {
		t.Fatal(history, err)
	}

}

func TestBusinessScheduleEditPauseAndDeniedOccurrence(t *testing.T) {
	db, m, _, w, actor, agent, _ := managerFixture(t)
	store := m.Store.(*managerrepo.Store)
	ctx := t.Context()
	config := func(cron string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"cron": cron, "description": "test work"})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write := manager.TriggerWrite{Kind: "schedule", Enabled: true, Config: config("0 9 * * *"), AllowedActions: []string{"create_task"}}
	trigger, err := store.SaveTrigger(ctx, w, actor, agent, "", write)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	tick := manager.TriggerScheduleTick{Version: trigger.Version, ExpectedDue: due, RecoveryStartedAt: due.Add(-time.Minute)}
	reviewExec(t, db, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, trigger.ID, due)
	write.Version, write.Enabled = trigger.Version, false
	paused, err := store.SaveTrigger(ctx, w, actor, agent, trigger.ID, write)
	if err != nil || paused.Enabled || paused.NextDue != nil {
		t.Fatal(paused, err)
	}
	result, err := store.TickTriggerSchedule(ctx, trigger.ID, tick)
	if err != nil || result.Status != "stale" {
		t.Fatal(result, err)
	}
	write.Version, write.Enabled, write.Config = paused.Version, true, config("30 16 * * *")
	resumed, err := store.SaveTrigger(ctx, w, actor, agent, trigger.ID, write)
	if err != nil {
		t.Fatal(err)
	}
	poll, err := store.PollTriggerSchedules(ctx, 100)
	if err != nil || len(poll.Items) != 1 || !poll.Items[0].NextDue.After(resumed.UpdatedAt) {
		t.Fatal(poll, err)
	}
	result, err = store.TickTriggerSchedule(ctx, trigger.ID, tick)
	if err != nil || result.Status != "stale" {
		t.Fatal(result, err)
	}
	// A disabled employee must not leave a permanently denied tick at the head of the queue.
	reviewExec(t, db, `UPDATE platform_agents SET enabled=false WHERE id=$1`, agent)
	reviewExec(t, db, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, trigger.ID, due)
	tick.Version = resumed.Version
	result, err = store.TickTriggerSchedule(ctx, trigger.ID, tick)
	if err != nil || result.Status != "skipped" || result.Receipt != nil {
		t.Fatal(result, err)
	}
	history, err := store.TriggerEvents(ctx, w, actor, agent, trigger.ID, time.Time{}, "", 20)
	if err != nil || len(history) != 1 || history[0].Reason != "agent_disabled" {
		t.Fatal(history, err)
	}
	var tasks int
	var next time.Time
	if err = db.QueryRow(`SELECT next_due,(SELECT count(*) FROM manager_tasks) FROM manager_triggers WHERE id=$1`, trigger.ID).Scan(&next, &tasks); err != nil || tasks != 0 || !next.After(due) {
		t.Fatal(next, tasks, err)
	}
}
