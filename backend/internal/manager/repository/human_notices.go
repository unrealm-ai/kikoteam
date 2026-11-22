package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const humanNoticeReady = `r.state='pending' AND r.fenced_at IS NOT NULL AND r.version=n.request_version AND r.recipient=n.recipient
 AND s.lifecycle='active' AND s.active_turn_id IS NULL AND s.stop_revision=s.applied_stop_revision
 AND COALESCE(c.lifecycle,p.lifecycle)='active' AND s.stop_revision=r.stop_revision
 AND manager_input_actor_allowed(r.authority_input_id)
 AND (` + humanRecipientStillConfigured + `)
 AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL
 AND b.enabled AND b.external_tenant_id=n.tenant_id AND b.connection_generation=n.connection_generation AND b.config_revision=n.config_revision AND b.identity_epoch=n.identity_epoch`

const humanNoticeSelect = `SELECT n.id,n.lease_token,n.binding_id,n.connection_generation,n.config_revision,n.provider,n.tenant_id,
 n.recipient->>'external_id',COALESCE(n.attempt_target_digest,''),COALESCE(n.provider_request_id,''),COALESCE(n.query_token::text,''),
 n.request_id,n.request_version,r.question,COALESCE(n.text,'') FROM `
const humanNoticeJoins = ` n JOIN manager_human_requests r ON r.id=n.request_id
 JOIN manager_sessions s ON s.id=r.session_id JOIN platform_channel_bindings b ON b.id=n.binding_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id`

func scanHumanNotice(row scanner) (service.ChannelDelivery, error) {
	var d service.ChannelDelivery
	var raw []byte
	var request string
	var version int64
	err := row.Scan(&d.ID, &d.LeaseToken, &d.BindingID, &d.ConnectionGeneration, &d.ConfigRevision, &d.Provider, &d.ExternalTenantID,
		&d.RecipientID, &d.TargetDigest, &d.ProviderRequestID, &d.QueryToken, &request, &version, &raw, &d.Text)
	if err != nil {
		return d, err
	}
	d.ChatType, d.Format, d.IdempotencyKey = "direct", "text", d.ID
	if d.Provider == "wecom" {
		d.ChatID = d.RecipientID
	}
	if d.Text == "" {
		var q service.HumanQuestion
		if err = json.Unmarshal(raw, &q); err != nil {
			return d, err
		}
		d.Text = humanNoticeText(service.HumanShortCode(request, version), q)
	}
	return d, nil
}

func humanNoticeText(request string, q service.HumanQuestion) string {
	question := q.Question
	hasRequired := false
	if len(q.Questions) != 0 {
		lines := make([]string, 0, len(q.Questions))
		for index, item := range q.Questions {
			required := item.Required != nil && *item.Required
			hasRequired = hasRequired || required
			line := humanNoticeQuestion(item, required)
			if len(q.Questions) > 1 {
				line = fmt.Sprintf("%d. %s", index+1, line)
			}
			lines = append(lines, line)
		}
		question = strings.Join(lines, "\n")
	}
	lines := []string{"人工确认 " + request}
	if background := strings.TrimSpace(q.Background); background != "" && !strings.Contains(question, background) {
		lines = append(lines, background)
	}
	lines = append(lines, question)
	information := q.AnswerType == "information"
	if action := strings.TrimSpace(q.ProposedAction); action != "" {
		label := "同意后："
		if information {
			label = "收到答复后："
		}
		lines = append(lines, label+action)
	}
	if impact := strings.TrimSpace(q.ImpactScope); impact != "" {
		lines = append(lines, "影响范围："+impact)
	}
	switch {
	case information:
		lines = append(lines, "请直接回复答案，或告诉我需要补充什么。")
	case hasRequired:
		lines = append(lines, "如同意，请同时回答必答项，并明确表示同意。也可以直接拒绝或说明需要调整的地方。")
	case len(q.Questions) == 0 && len(service.NumberedHumanQuestions(question)) != 0:
		lines = append(lines, "如同意，请先逐项回复，再单独明确同意。也可以直接拒绝或说明需要调整的地方。")
	default:
		lines = append(lines, "请直接回复同意、拒绝，或说明需要调整的地方。")
	}
	return strings.Join(lines, "\n")
}

func humanNoticeQuestion(item service.HumanQuestionItem, required bool) string {
	text := strings.TrimSpace(item.Text)
	var notes []string
	if required {
		notes = append(notes, "必答")
	}
	for _, raw := range item.Constraints {
		condition := strings.TrimSpace(raw)
		switch strings.ToLower(condition) {
		case "", "approve", "reject", "yes", "no", "true", "false":
			// Generic decision choices are already conveyed by the notice footer.
			continue
		}
		if !strings.Contains(text, condition) && !slices.Contains(notes, condition) {
			notes = append(notes, condition)
		}
	}
	if len(notes) != 0 {
		text += "（" + strings.Join(notes, "，") + "）"
	}
	return text
}

const populateHumanNotices = `INSERT INTO manager_human_notices(request_id,request_version,recipient,workspace_id,agent_id,binding_id,provider,tenant_id,connection_generation,config_revision,identity_epoch)
 SELECT r.id,r.version,r.recipient,r.workspace_id,r.agent_id,b.id,b.provider,b.external_tenant_id,b.connection_generation,b.config_revision,b.identity_epoch
 FROM manager_human_requests r JOIN manager_sessions s ON s.id=r.session_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 JOIN platform_channel_bindings b ON b.id::text=r.recipient->>'binding_id' AND b.workspace_id=r.workspace_id AND b.agent_id=r.agent_id
 WHERE r.state='pending' AND r.fenced_at IS NOT NULL AND r.proactive_notice_enabled AND s.lifecycle='active'
 AND COALESCE(c.lifecycle,p.lifecycle)='active' AND s.stop_revision=r.stop_revision
 AND (` + humanRecipientStillConfigured + `)
 AND s.active_turn_id IS NULL AND s.stop_revision=s.applied_stop_revision
 AND b.provider IN ('feishu','wecom','dingtalk') AND manager_input_actor_allowed(r.authority_input_id)
 AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM manager_human_notices n WHERE n.request_id=r.id AND n.request_version=r.version AND n.recipient=r.recipient)
 ORDER BY r.created_at,r.id LIMIT 50 ON CONFLICT(request_id,request_version,recipient) DO NOTHING`
const leaseHumanNotice = `WITH pick AS (SELECT n.id FROM manager_human_notices` + humanNoticeJoins + `
 WHERE (n.status='pending' OR n.status='leased' AND n.lease_until<now()) AND ` + humanNoticeReady + `
 ORDER BY n.created_at,n.id LIMIT $1 FOR UPDATE OF n SKIP LOCKED), leased AS (
 UPDATE manager_human_notices n SET status='leased',lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds'
 FROM pick WHERE n.id=pick.id RETURNING n.*) ` + humanNoticeSelect + `leased` + humanNoticeJoins
const leaseHumanNoticeQuery = `WITH pick AS (SELECT id FROM manager_human_notices
 WHERE provider='dingtalk' AND status IN ('accepted','unknown') AND provider_request_id IS NOT NULL
 AND next_query_at<=now() AND query_attempts<5 AND authorized_at>=now()-interval '5 minutes' AND (query_until IS NULL OR query_until<now())
 ORDER BY next_query_at,id LIMIT $1 FOR UPDATE SKIP LOCKED), leased AS (
 UPDATE manager_human_notices n SET query_token=gen_random_uuid(),query_until=now()+interval '30 seconds',
 query_attempts=query_attempts+1,next_query_at=now()+interval '30 seconds' FROM pick WHERE n.id=pick.id RETURNING n.*) ` + humanNoticeSelect + `leased` + humanNoticeJoins

func (s *Store) ClaimHumanNotices(ctx context.Context, limit int, query bool) ([]service.ChannelDelivery, error) {
	if limit < 1 || limit > 50 {
		return nil, service.Fail("invalid_argument", 400, "投递批量无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	statement := leaseHumanNotice
	if query {
		statement = leaseHumanNoticeQuery
		_, err = tx.ExecContext(ctx, `UPDATE manager_human_notices SET status='unknown',next_query_at=NULL,error_code='confirmation_window_exhausted'
 WHERE status='accepted' AND (authorized_at<now()-interval '5 minutes' OR (query_attempts>=5 AND query_until<now()))`)
	} else {
		if _, err = tx.ExecContext(ctx, populateHumanNotices); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE manager_human_notices SET status='unknown',error_code='send_outcome_unknown'
 WHERE status='sending' AND lease_until<now()`)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE manager_human_notices n SET status='cancelled',error_code='notice_target_changed'
 FROM manager_human_requests r, platform_channel_bindings b
 WHERE r.id=n.request_id AND b.id=n.binding_id AND n.attempt_id IS NULL
 AND (n.status='pending' OR n.status='leased' AND n.lease_until<now())
 AND (r.state<>'pending' OR r.version<>n.request_version OR r.recipient<>n.recipient OR NOT b.enabled
 OR b.connection_generation<>n.connection_generation OR b.config_revision<>n.config_revision
 OR b.identity_epoch<>n.identity_epoch OR b.external_tenant_id<>n.tenant_id)`)
		}

	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, statement, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	out := []service.ChannelDelivery{}
	for rows.Next() {
		d, err := scanHumanNotice(rows)
		if err != nil {
			return nil, err
		}
		if query {
			d.Text = ""
		}
		out = append(out, d)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s *Store) AuthorizeHumanNotice(ctx context.Context, id, lease string, dispatchCheck bool) (service.ChannelDelivery, error) {
	var d service.ChannelDelivery
	if !service.ValidID(id) || !service.ValidID(lease) {
		return d, service.Fail("invalid_argument", 400, "投递身份无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	// Match the answer and recovery lock ordering.
	var session, request string
	err = tx.QueryRowContext(ctx, `SELECT s.id,r.id FROM manager_sessions s JOIN manager_human_requests r ON r.session_id=s.id
 JOIN manager_human_notices n ON n.request_id=r.id WHERE n.id=$1 AND n.lease_token=$2 FOR UPDATE OF s`, id, lease).Scan(&session, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return d, service.Fail("human_notice_unavailable", 409, "该通知当前不能投递")
	}
	if err != nil {
		return d, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM manager_human_requests WHERE id=$1 AND session_id=$2 FOR UPDATE`, request, session).Scan(&request); err != nil {
		return d, err
	}
	state := "leased"
	if dispatchCheck {
		state = "sending"
	}
	d, err = scanHumanNotice(tx.QueryRowContext(ctx, humanNoticeSelect+"manager_human_notices"+humanNoticeJoins+`
 WHERE n.id=$1 AND n.lease_token=$2 AND n.status=$3 AND n.lease_until>now() AND n.request_id=$4 AND `+humanNoticeReady+` FOR UPDATE OF n`, id, lease, state, request))
	if errors.Is(err, sql.ErrNoRows) {
		return d, service.Fail("human_notice_unavailable", 409, "该通知当前不能投递")
	}
	if err != nil {
		return d, err
	}
	if !dispatchCheck {
		d.TargetDigest = deliveryTargetDigest(d)
		_, err = tx.ExecContext(ctx, `UPDATE manager_human_notices SET status='sending',attempt_id=$2,
 authorized_at=now(),attempt_target_digest=$3,text=$4,lease_until=now()+interval '30 seconds' WHERE id=$1`, id, lease, d.TargetDigest, d.Text)
		if err != nil {
			return d, err
		}
	}
	d.Allowed = true
	return d, tx.Commit()
}
