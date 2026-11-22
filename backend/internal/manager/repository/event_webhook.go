package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func eventConfig(raw json.RawMessage) service.EventHookConfig {
	var c service.EventHookConfig
	if json.Unmarshal(raw, &c) != nil {
		return service.EventHookConfig{}
	}
	return c
}
func changedHookProtocol(old, updated json.RawMessage) bool {
	a, z := eventConfig(old), eventConfig(updated)
	return a.Adapter != z.Adapter || a.Source != z.Source
}

func validateEventConfig(q service.TriggerWrite) error {
	var c struct {
		service.EventHookConfig
		// Older clients send this field back when editing or enabling a hook.
		LegacyRepositoryID int64 `json:"repositoryId,omitempty"`
	}
	if strictJSON(q.Config, &c) != nil || !slices.Equal(q.AllowedActions, []string{"create_task"}) || strings.TrimSpace(c.Prompt) == "" || strings.ContainsRune(c.Prompt, 0) || utf8.RuneCountInString(c.Prompt) > 4000 {
		return service.Fail("invalid_trigger_configuration", 400, "请填写有效的处理说明（最多 4000 字）")
	}
	if slices.Contains([]string{"custom", "github", "gitlab", "gitlab_token"}, c.Source) {
		return nil
	}
	return service.Fail("invalid_trigger_configuration", 400, "通知来源无效")
}

func (s *Store) ReceiveNativeEvent(ctx context.Context, id string, r service.EventRequest, now time.Time) (out service.TriggerReceipt, ping bool, err error) {
	denied := service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	if !service.ValidID(id) {
		return out, false, denied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, err := lockTriggerAcceptance(ctx, tx, id)
	if err != nil {
		return out, false, hookLookupError(err)
	}
	c := eventConfig(b.Config)
	if b.Kind != "webhook" || c.Adapter != "event" {
		return out, false, denied
	}
	ok, err := s.matchTriggerCredential(ctx, tx, id, func(secret []byte) bool { return r.Matches(c.Source, secret, now) })
	if err != nil {
		return out, false, err
	}
	if !ok {
		return out, false, denied
	}
	err = finishHookIngress(ctx, tx, b, func() error {
		p, delivery, handshake, e := r.Payload(c.Source)
		if e != nil {
			return e
		}
		ping = handshake
		if !ping && !b.Enabled {
			return service.Fail("trigger_disabled", 403, "触发器已停用")
		}
		if _, e = tx.ExecContext(ctx, `UPDATE manager_triggers SET last_verified_at=now() WHERE id=$1`, id); e != nil || ping {
			return e
		}
		out, e = s.acceptNativeEvent(ctx, tx, b, c.Prompt, p, delivery, now)
		return e
	})
	return out, ping && err == nil, err
}

func (s *Store) acceptNativeEvent(ctx context.Context, tx *sql.Tx, b service.Trigger, prompt string, p service.EventPayload, delivery string, now time.Time) (service.TriggerReceipt, error) {
	var out service.TriggerReceipt
	digest := fmt.Sprintf("%x", sha256.Sum256(p.Body))
	var key string
	if delivery != "" {
		err := tx.QueryRowContext(ctx, `SELECT e.event_id FROM manager_webhook_deliveries d JOIN manager_trigger_events e ON e.id=d.event_id WHERE d.trigger_id=$1 AND d.delivery_id=$2`, b.ID, delivery).Scan(&key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	recordDelivery := delivery != "" && key == ""
	if key == "" && p.Source == "github" {
		// Only the original body is signed. Replacing a delivery header cannot create a second task.
		err := tx.QueryRowContext(ctx, `SELECT event_id FROM manager_trigger_events WHERE trigger_id=$1 AND body_hash=$2 AND received_at>=now()-interval '24 hours' ORDER BY received_at DESC LIMIT 1`, b.ID, digest).Scan(&key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	if key == "" {
		key = service.NewID()
	}
	cmd := service.TriggerCommand{Version: 1, EventID: key, Action: "create_task", Description: prompt}
	out, err := s.acceptTriggerTx(ctx, tx, b, service.TriggerAcceptance{TriggerID: b.ID, BindingVersion: b.Version, EventID: key, BodyHash: digest, Command: cmd, Native: &p, OccurredAt: now})
	if err == nil && recordDelivery {
		_, err = tx.ExecContext(ctx, `INSERT INTO manager_webhook_deliveries(trigger_id,delivery_id,event_id) VALUES($1,$2,$3)`, b.ID, delivery, out.EventID)
	}
	return out, err
}
