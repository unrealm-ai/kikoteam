package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strconv"
)

func (s *Store) prepareSharedWebPage(ctx context.Context, id, inputID string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns lease.
	var raw []byte
	var start, end int64
	var limit int
	var cursor string
	e = tx.QueryRowContext(ctx, `SELECT source_ref,start_ms,end_ms,page_limit,provider_cursor FROM manager_group_history_queries WHERE id=$1 AND input_id=$2 AND source_ref->>'provider'='web' AND (status='pending' OR(status='leased' AND lease_until<now())) AND expires_at>now() FOR UPDATE`, id, inputID).Scan(&raw, &start, &end, &limit, &cursor)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = sharedHistoryAllowed(ctx, tx, id); e != nil {
		return e
	}
	var source struct {
		ExternalID string `json:"external_id"`
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		return e
	}
	before := int64(9223372036854775807)
	if cursor != "" {
		before, e = strconv.ParseInt(cursor, 10, 64)
		if e != nil {
			return service.Fail("invalid_history_cursor", 409, "历史分页状态无效")
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,seq,author_kind,COALESCE(sender_id::text,agent_id::text,''),display_name,text,(extract(epoch FROM created_at)*1000)::bigint FROM manager_web_group_messages WHERE group_id=$1 AND created_at>=to_timestamp($2::double precision/1000) AND created_at<to_timestamp($3::double precision/1000) AND seq<$4 ORDER BY seq DESC LIMIT $5`, source.ExternalID, start, end, before, limit+1)
	if e != nil {
		return e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close checked below.
	page := service.SharedHistoryPage{Items: []service.SharedHistoryItem{}, Coverage: "local_group_history"}
	var last int64
	for rows.Next() {
		var m service.SharedHistoryItem
		var seq int64
		if e = rows.Scan(&m.ID, &seq, &m.SenderType, &m.SenderID, &m.SenderName, &m.Text, &m.CreatedAtMS); e != nil {
			return errors.Join(e, rows.Close())
		}
		if len(page.Items) == limit {
			page.NextProviderCursor = strconv.FormatInt(last, 10)
			break
		}
		last = seq
		m.MessageType = "text"
		page.Items = append(page.Items, m)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return e
	}
	lease := service.NewID()
	if _, e = tx.ExecContext(ctx, `UPDATE manager_group_history_queries SET status='leased',lease_token=$2,lease_until=now()+interval '30 seconds' WHERE id=$1`, id, lease); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return s.CompleteSharedHistory(ctx, id, service.SharedHistoryCompletion{LeaseToken: lease, Status: "completed", Page: page})
}
