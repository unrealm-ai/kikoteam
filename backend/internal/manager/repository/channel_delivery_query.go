package repository

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// ClaimChannelDeliveryQueries grants only a read of a known provider request.
// It cannot transition a delivery to sending or authorize another network send.
func (s *Store) ClaimChannelDeliveryQueries(ctx context.Context, limit int) ([]service.ChannelDelivery, error) {
	if limit < 1 || limit > 50 {
		return nil, service.Fail("invalid_argument", 400, "查询批量无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if _, e = tx.ExecContext(ctx, `UPDATE manager_channel_deliveries SET status='unknown',next_query_at=NULL,error_code='confirmation_window_exhausted',updated_at=now() WHERE status='accepted' AND (query_attempts>=5 OR authorized_at<now()-interval '5 minutes')`); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `WITH candidates AS (SELECT id FROM manager_channel_deliveries WHERE provider='dingtalk' AND status IN ('accepted','unknown') AND provider_request_id IS NOT NULL AND attempt_target_digest IS NOT NULL AND query_attempts<5 AND authorized_at>=now()-interval '5 minutes' AND (query_until IS NULL OR query_until<now()) AND next_query_at<=now() ORDER BY next_query_at,id FOR UPDATE SKIP LOCKED LIMIT $1),updated AS(UPDATE manager_channel_deliveries d SET query_token=COALESCE(query_token,gen_random_uuid()),query_until=now()+interval '30 seconds',next_query_at=now()+interval '30 seconds',query_attempts=query_attempts+1 FROM candidates WHERE candidates.id=d.id RETURNING d.*)SELECT `+deliverySelect+` FROM updated d JOIN manager_channel_inbox n ON n.id=d.inbox_id`, limit)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	out := []service.ChannelDelivery{}
	for rows.Next() {
		v, e := scanDelivery(rows)
		if e != nil {
			return nil, e
		}
		v.Text = ""
		v.TaskDetailCard = nil
		out = append(out, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
