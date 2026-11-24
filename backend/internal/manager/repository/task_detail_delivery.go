package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type taskDetailDispatchTarget struct {
	destination                               service.TaskDetailDestination
	inbox, message, session, title, execution string
}

func (s *Store) QueueTaskDetailCard(ctx context.Context, scope service.DelegationScope, task string) (service.TaskDetailDispatchReceipt, error) {
	var out service.TaskDetailDispatchReceipt
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	origin, err := s.TaskDetailLinkOriginTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if origin == "" {
		return out, service.Fail("task_detail_card_disabled", 409, "任务详情卡片当前不可发送")
	}
	t, err := lockTaskDetailDispatchTarget(ctx, tx, scope, task)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "task-detail-send:"+t.session+":"+t.destination.ConversationID); err != nil {
		return out, err
	}
	status, err := priorTaskDetailDelivery(ctx, tx, t, scope.InputID)
	if err != nil {
		return out, err
	}
	if status != "" {
		out.Status = status
		return out, tx.Commit()
	}
	issue, err := s.IssueTaskDetailGrantTx(ctx, tx, service.TaskDetailIssue{SessionID: t.session, Destination: t.destination, Source: &scope})
	if err != nil {
		return out, err
	}
	id := service.NewID()
	sealed, err := s.sealTaskDetailURL(id, issue.URL)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text)
 VALUES($1,$2,$3,$4,$5,'查看任务详情')`, id, t.inbox, t.destination.BindingID, t.destination.Generation, "task_detail:"+id)
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_task_detail_deliveries(delivery_id,grant_id,source_input_id,title,state,sealed_url)
 VALUES($1,$2,$3,$4,$5,$6)`, id, issue.Grant.ID, scope.InputID, safeTaskDetailTitle(t.title, t.destination.ChatType), taskDetailCardState(t.execution), sealed)
	if err != nil {
		return out, err
	}
	out.Status = "queued"
	return out, tx.Commit()
}

func lockTaskDetailDispatchTarget(ctx context.Context, tx *sql.Tx, scope service.DelegationScope, task string) (taskDetailDispatchTarget, error) {
	var target taskDetailDispatchTarget
	v := scope.Identity
	// Check the root input itself, not just ReportOnly, which becomes false after
	// a later user input is steered into a report turn.
	err := tx.QueryRowContext(ctx, `SELECT n.id,n.binding_id,n.connection_generation,n.conversation_id,b.provider,n.chat_type,
 n.external_chat_id,n.thread_id,n.text,k.id,COALESCE(k.title,''),CASE
 WHEN k.block_reason IS NOT NULL THEN 'blocked'
 WHEN k.stop_revision>k.applied_stop_revision THEN 'stopping'
 WHEN jsonb_array_length(k.required_actions)>0 THEN 'waiting'
 WHEN k.active_turn_id IS NOT NULL THEN 'running'
 WHEN EXISTS(SELECT FROM manager_inputs q WHERE q.session_id=k.id AND q.status='queued' AND q.trigger_agent_loop) THEN 'queued'
 ELSE 'finished' END`+delegationTables+`
 JOIN manager_channel_inbox n ON n.input_id=$8 AND n.conversation_id=s.conversation_id AND n.status='accepted'
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_tasks tsk ON tsk.id=$9 AND tsk.source_chat_session_id=s.id AND tsk.workspace_id=s.workspace_id
 JOIN manager_sessions k ON k.task_id=tsk.id AND k.mode='task' `+delegationFilter+`
 AND i.source_kind='user' AND i.trigger_agent_loop AND s.workspace_id=$5 AND s.conversation_id=$6
 AND COALESCE(i.submitted_by,i.channel_principal_id)=$7 AND `+effectiveDelegatingInput+`=$8

 AND n.principal_id=$7 AND b.enabled AND b.connection_generation=n.connection_generation
 AND k.lifecycle='active' AND tsk.planning_state NOT IN ('planned','canceled') FOR SHARE OF s,b`, v.SessionID, v.EngineID, v.AssignmentID, v.Generation, scope.WorkspaceID, scope.ConversationID, scope.ActorID, scope.InputID, task).Scan(
		&target.inbox, &target.destination.BindingID, &target.destination.Generation, &target.destination.ConversationID, &target.destination.Channel,
		&target.destination.ChatType, &target.destination.ChatID, &target.destination.TopicID, &target.message, &target.session, &target.title, &target.execution)
	if err != nil {
		return target, visibleError(err)
	}
	target.destination.WorkspaceID = scope.WorkspaceID
	return target, nil
}

func priorTaskDetailDelivery(ctx context.Context, tx *sql.Tx, t taskDetailDispatchTarget, input string) (string, error) {
	var status string
	var same bool
	err := tx.QueryRowContext(ctx, `SELECT ch.status,dt.source_input_id=$3
 FROM manager_task_detail_deliveries dt JOIN manager_task_detail_grants g ON g.id=dt.grant_id
 JOIN manager_channel_deliveries ch ON ch.id=dt.delivery_id WHERE g.session_id=$1
 AND g.destination_conversation_id=$2 AND (dt.source_input_id=$3 OR
 g.revoked_at IS NULL AND g.expires_at>now() AND ch.status IN ('pending','leased','sending','accepted','unknown','sent'))
 ORDER BY (dt.source_input_id=$3) DESC, ch.created_at DESC, ch.id LIMIT 1`, t.session, t.destination.ConversationID, input).Scan(&status, &same)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !same && status == "sent" && explicitTaskDetailResend(t.message) {
		return "", nil
	}
	switch status {
	case "sent":
		return "already_sent", nil
	case "unknown", "accepted":
		return "delivery_unconfirmed", nil
	case "failed", "cancelled":
		return "not_sent", nil
	default:
		return "queued", nil
	}
}

func explicitTaskDetailResend(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	// Narrow positive phrases, from the verified channel message only. Quoted
	// history, generic mentions of a link, and agent assertions do not suffice.
	for _, p := range []string{"请重发任务详情", "重发任务详情", "请再发一次详情", "再发一次详情", "resend the task detail", "please resend the task detail"} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

func safeTaskDetailTitle(title, chatType string) string {
	// Task titles can contain personal input. Group cards send an innocuous
	// label until there is an explicitly published title.
	if chatType == "group" {
		return "任务详情"
	}
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, redactNoticeText(title))
	title = strings.Join(strings.Fields(title), " ")
	if n := utf8.RuneCountInString(title); n > 80 {
		title = string([]rune(title)[:80])
	}
	if title == "" {
		return "任务详情"
	}
	return title
}

func taskDetailCardState(state string) string {
	switch state {
	case "running":
		return "执行中"
	case "waiting":
		return "等待处理"
	case "blocked":
		return "待核对"
	case "stopping":
		return "正在停止"
	case "queued":
		return "已排队"
	default:
		return "本轮已结束"
	}
}

func (s *Store) taskDetailURLCipher() (cipher.AEAD, error) {
	if len(s.TriggerSecret) < 32 {
		return nil, service.Fail("task_detail_unavailable", 503, "任务详情卡片暂不可用")
	}
	key := sha256.Sum256(append([]byte("kiko:task-detail-dispatch:v1:"), s.TriggerSecret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Store) sealTaskDetailURL(delivery, raw string) ([]byte, error) {
	a, err := s.taskDetailURLCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, []byte(raw), []byte(delivery)), nil
}

func (s *Store) openTaskDetailURL(delivery string, sealed []byte) (string, error) {
	a, err := s.taskDetailURLCipher()
	if err != nil {
		return "", err
	}
	n := a.NonceSize()
	if len(sealed) < n {
		return "", service.Fail("task_detail_unavailable", 503, "任务详情卡片暂不可用")
	}
	raw, err := a.Open(nil, sealed[:n], sealed[n:], []byte(delivery))
	if err != nil {
		return "", service.Fail("task_detail_unavailable", 503, "任务详情卡片暂不可用")
	}
	return string(raw), nil
}

func (s *Store) populateTaskDetailCard(ctx context.Context, tx *sql.Tx, d *service.ChannelDelivery, dispatch bool) error {
	if d.TaskDetailCard == nil || !dispatch {
		return nil
	}
	sealed, err := s.authorizedTaskDetailPayload(ctx, tx, d.ID)
	if err != nil {
		return err
	}
	d.TaskDetailCard.URL, err = s.openTaskDetailURL(d.ID, sealed)
	return err
}

func (s *Store) authorizedTaskDetailPayload(ctx context.Context, tx *sql.Tx, id string) ([]byte, error) {
	origin, err := s.TaskDetailLinkOriginTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if origin == "" {
		return nil, service.Fail("task_detail_card_disabled", 409, "任务详情卡片当前不可发送")
	}
	var sealed []byte
	var grant string
	err = tx.QueryRowContext(ctx, `SELECT grant_id,sealed_url FROM manager_task_detail_deliveries
 WHERE delivery_id=$1 AND sealed_url IS NOT NULL FOR SHARE`, id).Scan(&grant, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.Fail("task_detail_card_unavailable", 409, "任务详情卡片当前不可发送")
	}
	if err != nil {
		return nil, err
	}
	if _, err = s.readTaskDetailGrant(ctx, tx, grant, false); err != nil {
		return nil, err
	}
	raw, err := s.openTaskDetailURL(id, sealed)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(raw, origin+"/tasks/") {
		return nil, service.Fail("task_detail_origin_changed", 409, "任务详情域名已更改")
	}
	return sealed, nil
}

// CheckTaskDetailDispatch applies only to the card variant of the existing
// dispatch-check route. It returns no URL and does not acquire a new lease.
func (s *Store) CheckTaskDetailDispatch(ctx context.Context, id, lease string) (bool, error) {
	if !service.ValidID(id) || !service.ValidID(lease) {
		return false, service.Fail("invalid_argument", 400, "投递身份无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var kind string
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT d.kind,COALESCE(d.lease_token=$2 AND d.status='sending' AND d.lease_until>now() AND (`+deliveryAllowedExpression+`),false)
 FROM manager_channel_deliveries d JOIN manager_channel_inbox n ON n.id=d.inbox_id
 JOIN platform_channel_bindings b ON b.id=d.binding_id JOIN platform_agents a ON a.id=b.agent_id WHERE d.id=$1 FOR SHARE OF b,d`, id, lease).Scan(&kind, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(kind, "task_detail:") {
		return false, nil
	}
	if !allowed {
		return true, service.Fail("task_detail_card_unavailable", 409, "任务详情卡片当前不可发送")
	}
	if _, err = s.authorizedTaskDetailPayload(ctx, tx, id); err != nil {
		var issue *service.Error
		if errors.As(err, &issue) && issue.Status == 404 {
			err = service.Fail("task_detail_card_unavailable", 409, "任务详情卡片当前不可发送")
		}
		return true, err
	}
	return true, tx.Commit()
}
