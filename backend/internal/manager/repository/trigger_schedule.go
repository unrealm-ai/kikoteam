package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"time"
)

func (s *Store) PollTriggerSchedules(ctx context.Context, limit int) (service.TriggerSchedulePoll, error) {
	out := service.TriggerSchedulePoll{Items: []service.TriggerScheduleItem{}}
	if limit < 1 || limit > 100 {
		return out, service.Fail("invalid_argument", 400, "批量无效")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&out.ServerTime); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM manager_triggers WHERE enabled AND kind='schedule' ORDER BY next_due NULLS FIRST,id LIMIT $1`, limit)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked below.
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	for _, id := range ids {
		item, e := s.initializeTriggerSchedule(ctx, id)
		if e != nil {
			return out, e
		}
		if item != nil {
			out.Items = append(out.Items, *item)
		}
	}
	return out, nil
}
func (s *Store) lockSchedule(ctx context.Context, tx *sql.Tx, id string) (service.Trigger, error) {
	var agent string
	if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM manager_triggers WHERE id=$1`, id).Scan(&agent); err != nil {
		return service.Trigger{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE id=$1 FOR SHARE`, agent).Scan(&agent); err != nil {
		return service.Trigger{}, err
	}
	return scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE id=$1 FOR UPDATE`, id))
}
func (s *Store) initializeTriggerSchedule(ctx context.Context, id string) (*service.TriggerScheduleItem, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, err := s.lockSchedule(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !b.Enabled || b.Kind != "schedule" {
		return nil, nil
	}
	if b.NextDue == nil {
		cfg, e := service.ParseTriggerSchedule(b.Config)
		if e != nil {
			return nil, e
		}
		at, e := service.NextTriggerSchedule(cfg.Cron, cfg.Timezone, b.UpdatedAt)
		if e != nil {
			return nil, e
		}
		b.NextDue = &at
		if _, err = tx.ExecContext(ctx, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, id, at); err != nil {
			return nil, err
		}
	}
	out := &service.TriggerScheduleItem{ID: id, Version: b.Version, NextDue: *b.NextDue}
	return out, tx.Commit()
}
func scheduleEventID(id string, version int64, at time.Time) string {
	raw, err := json.Marshal([]any{id, version, at.UnixMilli()})
	if err != nil {
		panic(err)
	} // Primitive values are always JSON-serializable.
	sum := sha256.Sum256(raw)
	return "schedule-" + hex.EncodeToString(sum[:])
}
func (s *Store) TickTriggerSchedule(ctx context.Context, id string, q service.TriggerScheduleTick) (service.TriggerScheduleResult, error) {
	out := service.TriggerScheduleResult{Status: "stale"}
	if !service.ValidID(id) || q.Version < 1 || q.ExpectedDue.IsZero() || q.RecoveryStartedAt.IsZero() {
		return out, service.Fail("invalid_argument", 400, "调度身份无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, err := s.lockSchedule(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if !b.Enabled || b.Kind != "schedule" || b.Version != q.Version || b.NextDue == nil || !b.NextDue.Equal(q.ExpectedDue) {
		return out, nil
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return out, err
	}
	if q.RecoveryStartedAt.After(now) {
		return out, service.Fail("invalid_argument", 400, "恢复时刻不能晚于服务器时间")
	}
	if b.NextDue.After(now) {
		out.Status = "not_due"
		return out, nil
	}
	cfg, err := service.ParseTriggerSchedule(b.Config)
	if err != nil {
		return out, err
	}
	next, err := service.NextTriggerSchedule(cfg.Cron, cfg.Timezone, now)
	if err != nil {
		return out, err
	}
	eventID := scheduleEventID(id, b.Version, *b.NextDue)
	var recorded bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_trigger_events WHERE trigger_id=$1 AND event_id=$2)`, id, eventID).Scan(&recorded); err != nil {
		return out, err
	}
	out.Status = "skipped"
	if !b.NextDue.Before(q.RecoveryStartedAt) || recorded {
		key, e := service.TriggerScheduleKey(cfg.BusinessKey, *b.NextDue, cfg.Timezone)
		if e != nil {
			return out, e
		}
		command := service.TriggerCommand{Version: 1, EventID: eventID, Action: cfg.Action, Description: cfg.Description, BusinessKey: key}
		raw, e := json.Marshal(command)
		if e != nil {
			return out, e
		}
		hash := sha256.Sum256(raw)
		acceptance := service.TriggerAcceptance{TriggerID: id, BindingVersion: b.Version, EventID: eventID, BodyHash: hex.EncodeToString(hash[:]), Command: command, OccurredAt: *b.NextDue}
		var receipt service.TriggerReceipt
		if cfg.Action == "send_message" {
			receipt, e = s.acceptScheduledReminderTx(ctx, tx, b, acceptance)
		} else {
			receipt, e = s.acceptTriggerTx(ctx, tx, b, acceptance)
		}
		if e != nil {
			return out, e
		}
		out.Status = "accepted"
		out.Receipt = &receipt
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_triggers SET next_due=$2 WHERE id=$1`, id, next); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
