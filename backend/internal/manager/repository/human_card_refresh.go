package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const humanRefreshJoins = ` JOIN manager_human_requests r ON r.id=n.request_id JOIN platform_channel_bindings b ON b.id=n.binding_id `
const humanRefreshScope = `n.status='sent' AND n.receipt->>'human_confirmation_card'='true'
 AND r.version=n.request_version AND r.recipient=n.recipient AND r.workspace_id=n.workspace_id AND r.agent_id=n.agent_id
 AND r.state IN ('approved','rejected','revised','cancelled')
 AND ((n.provider='feishu' AND n.provider_message_id<>'' AND n.provider_chat_id<>'') OR
      (n.provider='dingtalk' AND n.provider_request_id=n.id::text AND r.state IN ('approved','rejected')))
 AND b.enabled AND b.workspace_id=n.workspace_id AND b.agent_id=n.agent_id AND b.provider=n.provider AND b.external_tenant_id=n.tenant_id
 AND b.connection_generation=n.connection_generation AND b.config_revision=n.config_revision AND b.identity_epoch=n.identity_epoch
 AND ` + humanRecipientStillConfigured + ` AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL`
const humanRefreshSelect = `SELECT n.id,n.card_refresh_token,n.binding_id,n.provider,n.tenant_id,n.connection_generation,n.config_revision,
 CASE WHEN n.provider='feishu' THEN n.provider_message_id ELSE n.provider_request_id END,r.state,n.request_version,r.question,COALESCE(n.text,'') FROM `

func scanHumanCardRefresh(row scanner) (service.HumanCardRefresh, error) {
	var d service.HumanCardRefresh
	var raw []byte
	var text string
	if err := row.Scan(&d.ID, &d.LeaseToken, &d.BindingID, &d.Provider, &d.TenantID, &d.Generation, &d.Revision, &d.ProviderCardID, &d.State, &d.RequestVersion, &raw, &text); err != nil {
		return d, err
	}
	var q service.HumanQuestion
	if err := json.Unmarshal(raw, &q); err != nil {
		return d, err
	}
	d.Card = &service.HumanConfirmationCard{Title: q.Question, Description: text, DecisionRequired: q.AnswerType == "decision", Questions: q.Questions}
	return d, nil
}

func (s *Store) ClaimHumanCardRefresh(ctx context.Context) ([]service.HumanCardRefresh, error) {
	rows, err := s.DB.QueryContext(ctx, `WITH pick AS (
 SELECT n.id FROM manager_human_notices n`+humanRefreshJoins+`WHERE `+humanRefreshScope+`
 AND (n.card_refresh_result IS NULL OR n.card_refresh_result='unknown') AND n.card_refresh_attempts<3
 AND (n.card_refresh_until IS NULL OR n.card_refresh_until<=now())
 ORDER BY n.sent_at,n.id FOR UPDATE OF n SKIP LOCKED LIMIT 1
 ), leased AS (UPDATE manager_human_notices n SET card_refresh_token=gen_random_uuid(),
 card_refresh_until=now()+interval '30 seconds',card_refresh_attempts=n.card_refresh_attempts+1
 FROM pick WHERE n.id=pick.id RETURNING n.*)
 `+humanRefreshSelect+`leased n`+humanRefreshJoins)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Close errors on the read path are handled by rows.Err.
	out := []service.HumanCardRefresh{}
	for rows.Next() {
		job, e := scanHumanCardRefresh(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) AuthorizeHumanCardRefresh(ctx context.Context, id, token string) (service.HumanCardRefresh, error) {
	if !service.ValidID(id) || !service.ValidID(token) {
		return service.HumanCardRefresh{}, invalidHumanRefresh()
	}
	out, err := scanHumanCardRefresh(s.DB.QueryRowContext(ctx, humanRefreshSelect+`manager_human_notices n`+humanRefreshJoins+
		`WHERE n.id=$1 AND n.card_refresh_token=$2 AND n.card_refresh_until>now() AND
 (n.card_refresh_result IS NULL OR n.card_refresh_result='unknown') AND `+humanRefreshScope, id, token))
	if errors.Is(err, sql.ErrNoRows) {
		return out, service.Fail("human_card_refresh_stale", 409, "确认卡片不再允许刷新")
	}
	return out, err
}

func (s *Store) CompleteHumanCardRefresh(ctx context.Context, id, token, result string) error {
	if !service.ValidID(id) || !service.ValidID(token) || (result != "updated" && result != "failed" && result != "unknown") {
		return invalidHumanRefresh()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE manager_human_notices SET card_refresh_result=$3
 WHERE id=$1 AND card_refresh_token=$2 AND (card_refresh_result IS NULL OR card_refresh_result='unknown')`, id, token, result)
	return err
}

func invalidHumanRefresh() error {
	return service.Fail("invalid_human_card_refresh", 400, "无效的确认卡片刷新请求")
}
