package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Eligibility comes from the actual terminal native delivery, its bound app
// configuration and its intended recipient. Never invent a message Inbox for
// a provider callback. Normal confirmation/session gates still apply.
const humanCardNoticeMatch = `n.id=$2 AND n.binding_id=$3 AND n.provider=$4
 AND n.tenant_id=$5 AND n.recipient->>'external_id'=$6
 AND n.connection_generation=$8 AND n.config_revision=$9
 AND n.status='sent' AND n.receipt->>'human_confirmation_card'='true'
 AND (CASE WHEN n.provider='feishu' THEN n.provider_message_id
           WHEN n.provider='dingtalk' THEN n.provider_request_id
           WHEN n.provider='wecom' THEN n.provider_request_id END)=$10
 AND (n.provider<>'feishu' OR n.provider_chat_id=$11)
 AND b.id=n.binding_id AND b.workspace_id=n.workspace_id AND b.agent_id=n.agent_id
 AND b.enabled AND b.provider=n.provider AND b.external_tenant_id=n.tenant_id
 AND b.connection_generation=n.connection_generation AND b.config_revision=n.config_revision
 AND b.identity_epoch=n.identity_epoch`

const humanCardAnswerTarget = `r.workspace_id=$1 AND EXISTS (
 SELECT 1 FROM manager_human_notices n JOIN platform_channel_bindings b ON b.id=n.binding_id
 WHERE ` + humanCardNoticeMatch + `
 AND n.request_id=r.id AND n.request_version=r.version AND n.recipient=r.recipient
 AND n.workspace_id=r.workspace_id AND n.agent_id=r.agent_id)
 AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL
 AND ` + humanRecipientStillConfigured

type humanCardTarget struct {
	workspace, request, state, text string
	version                         int64
	question                        service.HumanQuestion
}

func loadHumanCardTarget(ctx context.Context, db humanQueryer, args []any) (humanCardTarget, error) {
	var t humanCardTarget
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT n.workspace_id,n.request_id,n.request_version,r.state,n.text,r.question
 FROM manager_human_notices n JOIN platform_channel_bindings b ON b.id=n.binding_id
 JOIN manager_human_requests r ON r.id=n.request_id
 WHERE `+humanCardNoticeMatch+` AND $1::text='' AND $7::text=''
 AND r.version=n.request_version AND r.recipient=n.recipient
 AND manager_human_recipient_name(r.workspace_id,r.agent_id,r.recipient,false) IS NOT NULL AND `+humanRecipientStillConfigured, args...).
		Scan(&t.workspace, &t.request, &t.version, &t.state, &t.text, &raw)
	if err != nil {
		return t, visibleError(err)
	}
	return t, json.Unmarshal(raw, &t.question)
}

func humanCardCallbackArgs(notice string, action service.HumanCardAction) ([]any, error) {
	if !service.ValidID(notice) || !service.ValidID(action.BindingID) || action.Generation < 1 || action.Revision < 1 ||
		action.Provider != "feishu" && action.Provider != "dingtalk" && action.Provider != "wecom" ||
		!validCardCallbackIdentity(action.ExternalTenant) || !validCardCallbackIdentity(action.ExternalActor) || !validCardCallbackIdentity(action.ProviderCardID) ||
		action.Provider == "feishu" && !validCardCallbackIdentity(action.ProviderChatID) {
		return nil, service.Fail("invalid_human_card", 400, "卡片回调身份无效")
	}
	return []any{"", notice, action.BindingID, action.Provider, action.ExternalTenant, action.ExternalActor, "", action.Generation, action.Revision, action.ProviderCardID, action.ProviderChatID}, nil
}

func (s *Store) ReadHumanCard(ctx context.Context, notice string, action service.HumanCardAction) (any, error) {
	args, err := humanCardCallbackArgs(notice, action)
	if err != nil {
		return nil, err
	}
	t, err := loadHumanCardTarget(ctx, s.DB, args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"state": t.state, "card": humanCardPresentation(t.question, t.text)}, nil
}

func (s *Store) AnswerHumanCard(ctx context.Context, notice string, action service.HumanCardAction) (any, error) {
	args, err := humanCardCallbackArgs(notice, action)
	if err != nil {
		return nil, err
	}
	if action.Decision != "approve" && action.Decision != "reject" && action.Decision != "information" {
		return nil, service.Fail("invalid_human_card", 400, "卡片答复无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	t, err := loadHumanCardTarget(ctx, tx, args)
	if err != nil {
		return nil, err
	}
	if t.state == "approved" || t.state == "rejected" || t.state == "revised" || t.state == "cancelled" {
		return map[string]any{"id": t.request, "state": t.state, "card": humanCardPresentation(t.question, t.text)}, nil
	}
	args[0], args[6] = t.workspace, t.request
	answer := service.HumanAnswer{Version: t.version, Decision: action.Decision, Text: action.Text, Answers: action.Answers}
	result, err := recordHumanAnswerWithTarget(ctx, tx, humanViewer{workspace: t.workspace}, t.request, answer, humanCardAnswerTarget, args)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if state, ok := result.(map[string]string); ok {
		return map[string]any{"id": t.request, "state": state["state"], "card": humanCardPresentation(t.question, t.text)}, nil
	}
	return result, nil
}

func validCardCallbackIdentity(id string) bool {
	return id != "" && len(id) <= 512 && !strings.ContainsAny(id, "\x00\r\n")
}
