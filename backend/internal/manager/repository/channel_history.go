package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"sort"
	"unicode/utf8"
)

// The caller holds the Conversation row lock. Reserve one immutable interval
// per authorized event before provider I/O. Successful Input admission, not
// model execution or a native context receipt, advances the boundary.
func prepareChannelHistory(ctx context.Context, tx *sql.Tx, inbox string, config []byte, provider, messageType string) (bool, error) {
	var h struct {
		Enabled bool `json:"enabled"`
		Limit   int  `json:"limit"`
	}
	if e := json.Unmarshal(config, &h); e != nil {
		return false, e
	}
	var status string
	e := tx.QueryRowContext(ctx, `SELECT status FROM manager_channel_history_jobs WHERE inbox_id=$1`, inbox).Scan(&status)
	if e == nil {
		return status != "completed", nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	if h.Limit < 1 || h.Limit > 100 {
		return false, service.Fail("history_configuration_invalid", 409, "历史配置无效")
	}
	var conversation, message, previous string
	var at, previousAt int64
	if e = tx.QueryRowContext(ctx, `SELECT conversation_id,provider_message_id,trigger_created_at_ms FROM manager_channel_inbox WHERE id=$1`, inbox).Scan(&conversation, &message, &at); e != nil {
		return false, e
	}
	e = tx.QueryRowContext(ctx, `SELECT trigger_message_id,trigger_created_at_ms FROM manager_channel_history_boundaries WHERE conversation_id=$1 FOR UPDATE`, conversation).Scan(&previous, &previousAt)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	var reason string
	switch {
	case previousAt > at:
		reason = "out_of_order_trigger"
	case at <= 0:
		reason = "missing_trigger_time"
	case !h.Enabled:
		reason = "history_disabled"
	case provider == "wecom" && messageType == "mixed":
		// The current WeCom history reader can anchor only text records.
		reason = "wecom_mixed_history_anchor_unavailable"
	}
	var snapshot any
	status = "pending"
	if reason != "" {
		status = "completed"
		data, e := json.Marshal(service.ChannelHistorySnapshot{Status: "unavailable", Items: []service.ChannelHistoryItem{}, ErrorCode: reason})
		if e != nil {
			return false, e
		}
		snapshot = data
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO manager_channel_history_jobs(id,inbox_id,requested_limit,previous_trigger_message_id,previous_trigger_created_at_ms,status,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7)`, service.NewID(), inbox, h.Limit, previous, previousAt, status, snapshot)
	if e != nil {
		return false, e
	}
	if status == "completed" {
		return false, nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='history',updated_at=now() WHERE id=$1`, inbox)
	return true, e
}

// channelHistoryItems returns the frozen snapshot only while its configuration
// and preceding Input boundary still match. The caller holds the session /
// conversation transaction lock; provider I/O never runs inside this transaction.
func channelHistoryItems(ctx context.Context, tx *sql.Tx, inbox string) ([]service.ChannelHistoryItem, error) {
	var enabled bool
	var limit int
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE((b.history->>'enabled')::boolean,false) AND b.config_revision=n.config_revision AND b.rules_version=n.rules_version,COALESCE((b.history->>'limit')::integer,0) FROM manager_channel_inbox n JOIN platform_channel_bindings b ON b.id=n.binding_id WHERE n.id=$1`, inbox).Scan(&enabled, &limit); e != nil {
		return nil, e
	}
	if !enabled {
		return nil, nil
	}
	var raw []byte
	var previous, current string
	e := tx.QueryRowContext(ctx, `SELECT j.snapshot,j.previous_trigger_message_id,COALESCE(h.trigger_message_id,'') FROM manager_channel_history_jobs j JOIN manager_channel_inbox n ON n.id=j.inbox_id LEFT JOIN manager_channel_history_boundaries h ON h.conversation_id=n.conversation_id WHERE j.inbox_id=$1 AND j.status='completed'`, inbox).Scan(&raw, &previous, &current)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if previous != current {
		return nil, service.Fail("history_boundary_changed", 409, "群历史边界已变化")
	}
	var snapshot service.ChannelHistorySnapshot
	if e = json.Unmarshal(raw, &snapshot); e != nil {
		return nil, e
	}
	if snapshot.Status == "unavailable" {
		return nil, nil
	}
	if len(snapshot.Items) > limit {
		return nil, service.Fail("invalid_argument", 400, "历史条数超限")
	}
	if len(snapshot.Items) == 0 || snapshot.Items[0].RecordKey == "" {
		sort.SliceStable(snapshot.Items, func(i, j int) bool { return snapshot.Items[i].CreatedAtMS < snapshot.Items[j].CreatedAtMS })
	}
	return snapshot.Items, nil
}

const historySelect = `j.id,j.lease_token,n.binding_id,n.connection_generation,n.config_revision,n.rules_version,n.external_chat_id,n.thread_id,n.provider_message_id,n.trigger_created_at_ms,j.requested_limit,n.external_tenant_id,j.previous_trigger_message_id,j.previous_trigger_created_at_ms,(SELECT provider FROM platform_channel_bindings WHERE id=n.binding_id),n.text`

func scanHistory(row scanner) (service.ChannelHistoryJob, error) {
	var j service.ChannelHistoryJob
	var provider string
	e := row.Scan(&j.ID, &j.LeaseToken, &j.BindingID, &j.ConnectionGeneration, &j.ConfigRevision, &j.RulesVersion, &j.ChatID, &j.ThreadID, &j.TriggerMessageID, &j.TriggerCreatedAtMS, &j.Limit, &j.ExternalTenantID, &j.PreviousTriggerMessageID, &j.PreviousTriggerCreatedAtMS, &provider, &j.TriggerText)
	j.Strategy = "since_last_trigger"
	if provider == "wecom" {
		j.Strategy = "wecom_recent"
	} else {
		j.TriggerText = ""
	}
	return j, e
}
func (s *Store) ClaimChannelHistory(ctx context.Context, limit int) ([]service.ChannelHistoryJob, error) {
	if limit < 1 || limit > 10 {
		return nil, service.Fail("invalid_argument", 400, "历史批量无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	if _, e = tx.ExecContext(ctx, `WITH expired AS (UPDATE manager_channel_history_jobs SET status='completed',snapshot='{"status":"unavailable","items":[],"error_code":"history_deadline"}' WHERE status<>'completed' AND deadline<now() RETURNING inbox_id) UPDATE manager_channel_inbox n SET status='pending',updated_at=now() FROM expired WHERE n.id=expired.inbox_id AND n.status='history'`); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `WITH picked AS (SELECT id FROM manager_channel_history_jobs WHERE deadline>now() AND (status='pending' OR (status='leased' AND lease_until<now())) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1), updated AS(UPDATE manager_channel_history_jobs j SET status='leased',lease_token=gen_random_uuid(),lease_until=LEAST(deadline,now()+interval '30 seconds') FROM picked WHERE j.id=picked.id RETURNING j.*) SELECT `+historySelect+` FROM updated j JOIN manager_channel_inbox n ON n.id=j.inbox_id`, limit)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked before commit.
	out := []service.ChannelHistoryJob{}
	for rows.Next() {
		j, e := scanHistory(rows)
		if e != nil {
			return nil, errors.Join(e, rows.Close())
		}
		out = append(out, j)
	}
	if e = rows.Err(); e != nil {
		return nil, errors.Join(e, rows.Close())
	}
	if e = rows.Close(); e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
func (s *Store) AuthorizeChannelHistory(ctx context.Context, id, lease string) (service.ChannelHistoryJob, error) {
	var out service.ChannelHistoryJob
	if !service.ValidID(id) || !service.ValidID(lease) {
		return out, service.Fail("invalid_argument", 400, "历史租约无效")
	}
	out, e := scanHistory(s.DB.QueryRowContext(ctx, `SELECT `+historySelect+` FROM manager_channel_history_jobs j JOIN manager_channel_inbox n ON n.id=j.inbox_id JOIN platform_channel_bindings b ON b.id=n.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE j.id=$1 AND j.lease_token=$2 AND j.status='leased' AND j.lease_until>now() AND j.deadline>now() AND n.status='history' AND n.chat_type='group' AND n.trigger_created_at_ms>0 AND a.enabled AND COALESCE((b.history->>'enabled')::boolean,false) AND j.requested_limit<=COALESCE((b.history->>'limit')::integer,0) AND manager_channel_actor_allowed(n.principal_id,n.conversation_id,n.connection_generation)`, id, lease))
	if errors.Is(e, sql.ErrNoRows) {
		return out, service.Fail("access_revoked", 409, "历史读取权限或租约已失效")
	}
	out.Allowed = e == nil
	return out, e
}
func (s *Store) CompleteChannelHistory(ctx context.Context, id string, v service.ChannelHistoryCompletion) error {
	if !service.ValidID(id) || !service.ValidID(v.LeaseToken) || (v.Snapshot.Status != "complete" && v.Snapshot.Status != "partial" && v.Snapshot.Status != "unavailable") || len(v.Snapshot.Items) > 100 || len(v.Snapshot.ErrorCode) > 128 {
		return service.Fail("invalid_argument", 400, "历史快照无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	var inbox, trigger, previous, provider string
	var cutoff, previousAt int64
	var limit int
	e = tx.QueryRowContext(ctx, `SELECT n.id,n.provider_message_id,n.trigger_created_at_ms,j.requested_limit,j.previous_trigger_message_id,j.previous_trigger_created_at_ms,(SELECT provider FROM platform_channel_bindings WHERE id=n.binding_id) FROM manager_channel_history_jobs j JOIN manager_channel_inbox n ON n.id=j.inbox_id WHERE j.id=$1 AND j.lease_token=$2 AND j.status='leased' AND j.lease_until>now() FOR UPDATE OF j`, id, v.LeaseToken).Scan(&inbox, &trigger, &cutoff, &limit, &previous, &previousAt, &provider)
	if errors.Is(e, sql.ErrNoRows) {
		return service.Fail("lease_lost", 409, "历史租约已失效")
	}
	if e != nil {
		return e
	}
	if len(v.Snapshot.Items) > limit {
		return service.Fail("invalid_argument", 400, "历史条数超限")
	}
	if e = validateHistorySnapshotItems(v.Snapshot.Items, provider, trigger, previous, cutoff, previousAt); e != nil {
		return e
	}

	// Recheck permission after provider I/O; revoked snapshots are discarded.
	var allowed bool
	e = tx.QueryRowContext(ctx, `SELECT a.enabled AND COALESCE((b.history->>'enabled')::boolean,false) AND b.config_revision=n.config_revision AND b.rules_version=n.rules_version AND COALESCE((b.history->>'limit')::integer,0)>=$2 AND manager_channel_actor_allowed(n.principal_id,n.conversation_id,n.connection_generation) FROM manager_channel_inbox n JOIN platform_channel_bindings b ON b.id=n.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE n.id=$1 FOR SHARE OF b,a`, inbox, limit).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		v.Snapshot = service.ChannelHistorySnapshot{Status: "unavailable", Items: []service.ChannelHistoryItem{}, ErrorCode: "access_revoked"}
	}
	raw, e := json.Marshal(v.Snapshot)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_history_jobs SET status='completed',snapshot=$2 WHERE id=$1`, id, raw); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='pending',updated_at=now() WHERE id=$1 AND status='history'`, inbox); e != nil {
		return e
	}
	e = tx.Commit()
	if e == nil {
		s.channelLog(ctx, "channel.inbox_history_persisted", "provider", provider, "inbox_id", inbox, "history_job_id", id, "status", v.Snapshot.Status, "item_count", len(v.Snapshot.Items))
	}
	return e
}

// Input and its group boundary are committed together. Context-only events
// participate too; receipt/turn status cannot move this cursor backwards.
func acceptChannelInput(ctx context.Context, tx *sql.Tx, inbox string, input service.Input) error {
	if _, e := tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='accepted',input_id=$2,lease_token=NULL,lease_until=NULL,updated_at=now() WHERE id=$1`, inbox, input.ID); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO manager_channel_history_boundaries(conversation_id,trigger_message_id,trigger_created_at_ms,input_seq,inbox_id)
 SELECT conversation_id,provider_message_id,trigger_created_at_ms,$2,id FROM manager_channel_inbox WHERE id=$1 AND chat_type='group' AND trigger_created_at_ms>0
 ON CONFLICT(conversation_id) DO UPDATE SET trigger_message_id=EXCLUDED.trigger_message_id,trigger_created_at_ms=EXCLUDED.trigger_created_at_ms,input_seq=EXCLUDED.input_seq,inbox_id=EXCLUDED.inbox_id,notification_id=NULL WHERE manager_channel_history_boundaries.input_seq<EXCLUDED.input_seq AND manager_channel_history_boundaries.trigger_created_at_ms<=EXCLUDED.trigger_created_at_ms`, inbox, input.Seq)
	return e
}

func validateHistorySnapshotItems(items []service.ChannelHistoryItem, provider, trigger, previous string, cutoff, previousAt int64) error {
	seen := map[string]bool{}
	size := 0
	for _, item := range items {
		if !validChannelMetadata(service.ChannelMessageMetadata{SenderName: item.SenderName, SenderTenant: item.SenderTenant, SenderIDType: item.SenderIDType, Mentions: item.Mentions}) {
			return service.Fail("invalid_argument", 400, "历史消息元数据无效")
		}
		size += utf8.RuneCountInString(item.Text)
		key := item.MessageID
		valid := item.MessageID != "" && len(item.MessageID) <= 256 && item.RecordKey == "" && item.MessageID != trigger && item.MessageID != previous && item.CreatedAtMS >= previousAt && item.CreatedAtMS <= cutoff
		if provider == "wecom" {
			key = item.RecordKey
			valid = item.MessageID == "" && len(key) > 0 && len(key) <= 256 && item.SenderType == "unknown" && item.SenderIDType == "" && len(item.Mentions) == 0 && item.CreatedAtMS >= cutoff-7*86400000 && item.CreatedAtMS <= cutoff+1000
		}
		if !valid || seen[key] || len(item.SenderID) > 256 || len(item.SenderType) > 64 || len(item.MessageType) > 64 || item.CreatedAtMS <= 0 || size > 32768 {
			return service.Fail("invalid_argument", 400, "历史范围无效")
		}
		seen[key] = true
	}
	return nil
}
