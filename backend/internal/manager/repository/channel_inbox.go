package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/shared/trigger"
)

func invalidChannelInbound(v service.ChannelInbound) bool {
	return !validInboundMedia(v.Media) || v.ControlEligible && len(v.Media) > 0 || !service.ValidID(v.BindingID) || v.ConnectionGeneration < 1 || v.ConfigRevision < 1 || v.RulesVersion < 1 || (v.ChatType != "direct" && v.ChatType != "group") || len(v.ProviderEventID) == 0 || len(v.ProviderEventID) > 256 || len(v.ProviderMessageID) == 0 || len(v.ProviderMessageID) > 256 || len(v.ExternalTenantID) == 0 || len(v.ExternalTenantID) > 256 || len(v.ExternalChatID) == 0 || len(v.ExternalChatID) > 256 || len(v.SenderID) == 0 || len(v.SenderID) > 256 || len(v.ThreadID) > 256 || strings.ContainsRune(v.ThreadID, 0) || len(v.MessageType) == 0 || len(v.MessageType) > 64 || utf8.RuneCountInString(v.Text) > 65536 || strings.ContainsRune(v.Text, 0)
}

func (s *Store) ReceiveChannel(ctx context.Context, v service.ChannelInbound) (out service.ChannelReceipt, retErr error) {
	started := time.Now()
	out = service.ChannelReceipt{ID: service.NewID(), Status: "accepted"}
	var provider string
	controlOnly := false
	defer func() {
		fields := []any{"provider", provider, "binding_id", safeChannelLogID(v.BindingID), "provider_event_id", safeChannelLogID(v.ProviderEventID), "provider_message_id", safeChannelLogID(v.ProviderMessageID), "duration_ms", time.Since(started).Milliseconds()}
		if v.CreatedAtMS > 0 && (provider == "feishu" || provider == "dingtalk") {
			fields = append(fields, "provider_created_at_ms", v.CreatedAtMS)
		}
		switch {
		case retErr != nil:
			s.channelLog(ctx, "channel.inbox_admission_failed", append(fields, "error_code", channelErrorCode(retErr))...)
		case controlOnly:
			s.channelLog(ctx, "channel.inbox_control_handled", fields...)
		default:
			s.channelLog(ctx, "channel.inbox_persisted", append(fields, "inbox_id", out.ID, "status", out.Status)...)
		}
	}()
	if invalidChannelInbound(v) {
		return out, service.Fail("invalid_argument", 400, "渠道事件无效")
	}
	if len(v.ChatName) > 512 || strings.ContainsAny(v.ChatName, "\r\n\x00") {
		return out, service.Fail("invalid_argument", 400, "群显示名无效")
	}
	if len(v.ReplyMessageID) > 256 || strings.ContainsAny(v.ReplyMessageID, "\r\n\x00") {
		return out, service.Fail("invalid_argument", 400, "渠道引用标识无效")
	}
	if v.QuotedText != "" && (len(v.Text)+len(v.QuotedText) > 65536 || strings.ContainsRune(v.QuotedText, 0)) {
		return out, service.Fail("invalid_argument", 400, "渠道引用正文无效")
	}
	metadata := service.ChannelMessageMetadata{SenderName: v.SenderName, SenderTenant: v.SenderTenant, SenderIDType: v.SenderIDType, Mentions: v.Mentions}
	if !validChannelMetadata(metadata) {
		return out, service.Fail("invalid_argument", 400, "渠道消息元数据无效")
	}
	metadata.ReplyMessageID = v.ReplyMessageID
	metadata.QuotedText = strings.TrimSpace(v.QuotedText)
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return out, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	provider, identityEpoch, err := verifyChannelInboundIdentity(ctx, tx, v)
	if err != nil {
		return out, err
	}
	if v.ReplyMessageID != "" && (provider != "feishu" || v.ChatType != "direct" || v.ThreadID != "") {
		return out, service.Fail("invalid_argument", 400, "渠道不支持该引用标识")
	}
	if v.QuotedText != "" && provider != "wecom" {
		return out, service.Fail("invalid_argument", 400, "渠道不支持引用正文")
	}
	if v.ControlEligible && provider != "wecom" {
		return out, service.Fail("invalid_argument", 400, "渠道不支持该控制事件")
	}
	if provider == "wecom" && s.ConsumeWecomControl != nil {
		control, controlErr := s.ConsumeWecomControl(ctx, tx, v, identityEpoch)
		if controlErr != nil {
			return out, controlErr
		}
		if !control.Valid() {
			return out, errors.New("invalid WeCom control result")
		}
		if control.Handled {
			controlOnly = true
			if err = tx.Commit(); err != nil {
				return out, err
			}
			out.ControlReply = control.Reply
			return out, nil
		}
	}
	if err = persistChannelInbound(ctx, tx, v, provider, identityEpoch, metadataJSON, fingerprint, &out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func persistChannelInbound(ctx context.Context, tx *sql.Tx, v service.ChannelInbound, provider string, identityEpoch int64, metadataJSON []byte, fingerprint string, out *service.ChannelReceipt) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_channel_inbox(id,binding_id,connection_generation,config_revision,rules_version,provider_event_id,provider_message_id,fingerprint,external_tenant_id,external_chat_id,chat_type,sender_id,text,message_type,bot_mentioned,thread_id,trigger_created_at_ms,identity_epoch,message_metadata,provider_directed) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) ON CONFLICT DO NOTHING`, out.ID, v.BindingID, v.ConnectionGeneration, v.ConfigRevision, v.RulesVersion, v.ProviderEventID, v.ProviderMessageID, fingerprint, v.ExternalTenantID, v.ExternalChatID, v.ChatType, v.SenderID, v.Text, v.MessageType, v.BotMentioned, v.ThreadID, v.CreatedAtMS, identityEpoch, metadataJSON, v.ProviderDirected)
	if err != nil {
		return err
	}
	var prior string
	if err = tx.QueryRowContext(ctx, `SELECT id,fingerprint FROM manager_channel_inbox WHERE binding_id=$1 AND identity_epoch=$4 AND external_tenant_id=$5 AND (provider_event_id=$2 OR provider_message_id=$3) ORDER BY created_at LIMIT 1`, v.BindingID, v.ProviderEventID, v.ProviderMessageID, identityEpoch, v.ExternalTenantID).Scan(&prior, &fingerprint); err != nil {
		return err
	}
	// Event id and message id are both stable dedup boundaries. A duplicate
	// provider delivery must not restart execution, even if envelope metadata differs.
	if prior != out.ID {
		out.ID = prior
		out.Status = "duplicate"
	}
	if out.Status == "accepted" {
		for i, item := range v.Media {
			b, e := json.Marshal(item)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO manager_channel_media(inbox_id,ordinal,locator,name) VALUES($1,$2,$3,$4)`, out.ID, i, b, item.Name); e != nil {
				return e
			}
		}
	}
	if provider == "feishu" && prior == out.ID && out.Status == "accepted" {
		if err = queueGroupRecheck(ctx, tx, v, identityEpoch); err != nil {
			return err
		}
	}
	if v.ChatType == "group" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO platform_channel_observed_chats(binding_id,identity_epoch,external_tenant_id,external_chat_id,display_name) VALUES($1,$2,$3,$4,$5) ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_chat_id) DO UPDATE SET display_name=CASE WHEN EXCLUDED.display_name<>'' THEN EXCLUDED.display_name ELSE platform_channel_observed_chats.display_name END,observed_at=now()`, v.BindingID, identityEpoch, v.ExternalTenantID, v.ExternalChatID, v.ChatName); err != nil {
			return err
		}
	}
	return nil
}

type inboxWork struct {
	confirmationOnly                                                           bool
	Attachments                                                                []service.Attachment
	History                                                                    []byte
	Provider                                                                   string
	CreatedAt                                                                  time.Time
	Kind                                                                       string
	ID, Binding, Lease, Workspace, Agent, Principal, Conversation, MessageType string
	Generation                                                                 int64
	Text                                                                       string
}

func (s *Store) ProcessChannelInbox(ctx context.Context, m *service.Service) (retErr error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	if _, err = tx.ExecContext(ctx, `WITH expired AS (UPDATE manager_channel_history_jobs SET status='completed',snapshot='{"status":"unavailable","items":[],"error_code":"history_deadline"}' WHERE status<>'completed' AND deadline<now() RETURNING inbox_id) UPDATE manager_channel_inbox n SET status='pending',updated_at=now() FROM expired WHERE n.id=expired.inbox_id AND n.status='history'`); err != nil {
		return err
	}
	var w inboxWork
	defer func() {
		if retErr != nil && w.ID != "" {
			s.channelInboxLog(ctx, w, "channel.inbox_processing_failed", "error_code", channelErrorCode(retErr))
		}
	}()
	var tenant, chat, kind, sender, thread string
	var mentioned, directed bool
	err = tx.QueryRowContext(ctx, `SELECT id,binding_id,connection_generation,external_tenant_id,external_chat_id,chat_type,sender_id,text,message_type,bot_mentioned,thread_id,provider_directed,created_at FROM manager_channel_inbox n WHERE NOT EXISTS(SELECT 1 FROM manager_channel_groups g WHERE n.chat_type='group' AND g.binding_id=n.binding_id AND g.identity_epoch=n.identity_epoch AND g.external_tenant_id=n.external_tenant_id AND g.external_chat_id=n.external_chat_id AND g.state='pending') AND ((n.status='pending' AND (n.lease_until IS NULL OR n.lease_until<now())) OR (n.status='processing' AND n.lease_until<now())) AND NOT EXISTS(SELECT 1 FROM manager_channel_inbox prior WHERE prior.binding_id=n.binding_id AND prior.identity_epoch=n.identity_epoch AND prior.external_tenant_id=n.external_tenant_id AND prior.external_chat_id=n.external_chat_id AND prior.chat_type=n.chat_type AND prior.thread_id=n.thread_id AND (prior.created_at,prior.id)<(n.created_at,n.id) AND prior.status IN ('pending','processing','history')) ORDER BY n.created_at,n.id FOR UPDATE OF n SKIP LOCKED LIMIT 1`).Scan(&w.ID, &w.Binding, &w.Generation, &tenant, &chat, &kind, &sender, &w.Text, &w.MessageType, &mentioned, &thread, &directed, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.processClaimedInbox(ctx, tx, m, w, kind, tenant, chat, sender, thread, mentioned, directed)
}
func (s *Store) processClaimedInbox(ctx context.Context, tx *sql.Tx, m *service.Service, w inboxWork, kind, tenant, chat, sender, thread string, mentioned, directed bool) error {
	var err error
	var enabled, agentEnabled bool
	var generation, identityEpoch int64
	var boundTenant, provider string
	var trigger, history []byte
	if err = tx.QueryRowContext(ctx, `SELECT b.workspace_id,b.agent_id,b.enabled,a.enabled,b.connection_generation,b.identity_epoch,b.external_tenant_id,b.trigger,b.history,b.provider FROM platform_channel_bindings b JOIN platform_agents a ON a.id=b.agent_id WHERE b.id=$1 FOR SHARE OF b,a`, w.Binding).Scan(&w.Workspace, &w.Agent, &enabled, &agentEnabled, &generation, &identityEpoch, &boundTenant, &trigger, &history, &provider); err != nil {
		return err
	}
	w.Provider = provider
	finish := func(code, text string) error { return s.finishChannelInbox(ctx, tx, w, code, text) }

	if !channelBindingCurrent(enabled, generation, w.Generation, boundTenant, tenant) {
		return finish("binding_unavailable", "")
	}
	if kind == "group" && provider == "feishu" {
		wait, reject, e := channelGroupAdmission(ctx, tx, w.ID, w.Binding, identityEpoch, tenant, chat)
		if e != nil {
			return e
		}
		if wait {
			return nil
		}
		if reject {
			return finish("group_unavailable", "")
		}
	}

	if kind == "group" {
		match, e := channelInboundTriggerMatches(provider, trigger, w.Text, mentioned, directed)
		if e != nil {
			return e
		}
		if !match {
			return finish("trigger_not_matched", "")
		}
	}
	w.Principal = service.NewID()
	if err = tx.QueryRowContext(ctx, `INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id,identity_epoch) VALUES($1,$2,$3,$7,$4,$5,$6) ON CONFLICT(binding_id,identity_epoch,external_tenant_id,external_user_id) DO UPDATE SET external_user_id=EXCLUDED.external_user_id RETURNING id`, w.Principal, w.Workspace, w.Binding, tenant, sender, identityEpoch, provider).Scan(&w.Principal); err != nil {
		return err
	}
	// Both private and group mappings are channel-local and never adopt Web chats.
	w.Conversation = service.NewID()
	if err = tx.QueryRowContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_identity_epoch,channel_tenant_id,channel_thread_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id,channel_thread_id) WHERE channel_binding_id IS NOT NULL DO UPDATE SET external_chat_id=EXCLUDED.external_chat_id RETURNING id`, w.Conversation, w.Workspace, w.Agent, w.Principal, w.Binding, chat, kind, identityEpoch, tenant, thread).Scan(&w.Conversation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET principal_id=$2,conversation_id=$3 WHERE id=$1`, w.ID, w.Principal, w.Conversation); err != nil {
		return err
	}
	var allowed bool
	if err = tx.QueryRowContext(ctx, `SELECT manager_channel_actor_allowed($1,$2,$3)`, w.Principal, w.Conversation, w.Generation).Scan(&allowed); err != nil {
		return err
	}
	if agentEnabled && isHumanMainDM(w, kind, thread, allowed) {
		if err = captureRecentHumanDMNotices(ctx, tx, w); err != nil {
			return err
		}
	}
	if !allowed {
		if kind == "direct" && thread == "" && w.MessageType == "text" && strings.TrimSpace(w.Text) != "" {
			err = tx.QueryRowContext(ctx, `SELECT manager_human_inbox_confirmation_allowed($1,$2)`, w.ID, w.Principal).Scan(&w.confirmationOnly)
			if err != nil {
				return err
			}
		}
		if !w.confirmationOnly {
			return s.finishChannelAccessDenied(ctx, tx, w, kind, provider, mentioned, directed)
		}
	}
	if !agentEnabled {
		return finish("agent_disabled", "此 AI 员工已停用，暂时无法处理消息。")
	}
	if ready, e := s.prepareInboxContent(ctx, tx, &w, provider); !ready || e != nil {
		return e
	}
	if kind == "group" {
		waiting, e := prepareChannelHistory(ctx, tx, w.ID, history, provider, w.MessageType)
		if e != nil {
			return e
		}
		if waiting {
			err = tx.Commit()
			if err == nil {
				s.channelInboxLog(ctx, w, "channel.inbox_waiting_history")
			}
			return err
		}

	}
	w.History = history
	w.Kind = kind
	w.Lease = service.NewID()
	if _, err = tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='processing',lease_token=$2,lease_until=now()+interval '30 seconds',updated_at=now() WHERE id=$1`, w.ID, w.Lease); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.channelInboxLog(ctx, w, "channel.inbox_claimed", "queue_wait_ms", max(0, time.Since(w.CreatedAt).Milliseconds()))
	return s.submitChannelInput(ctx, m, w)
}

func (s *Store) finishChannelAccessDenied(ctx context.Context, tx *sql.Tx, w inboxWork, kind, provider string, mentioned, directed bool) error {
	// Keyword/regex matches are passive group triggers. Do not interrupt the
	// group with permission replies unless this employee was explicitly @ed.
	if kind == "group" && !mentioned && (provider != "wecom" || !directed) {
		return s.finishChannelInbox(ctx, tx, w, "access_denied", "")
	}
	return s.finishChannelInbox(ctx, tx, w, "access_denied", "你暂时没有使用此 AI 员工的权限。")
}

func (s *Store) submitChannelInput(ctx context.Context, m *service.Service, w inboxWork) error {
	var err error
	var history struct {
		Enabled bool `json:"enabled"`
	}
	if err = json.Unmarshal(w.History, &history); err != nil {
		return err
	}
	channelCtx := service.WithChannelActor(ctx, service.ChannelActor{ID: w.Principal, Generation: w.Generation, NeedsContextReceipt: w.Kind == "group" && history.Enabled, InboxID: w.ID, ConfirmationOnly: w.confirmationOnly, Attachments: w.Attachments})
	var session string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat'`, w.Conversation).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		v, _, e := m.CreateSession(channelCtx, w.Workspace, w.Principal, "", service.CreateSession{ConversationID: w.Conversation, Mode: "chat"})
		err = e
		session = v.ID
		if e != nil {
			var conflict *service.Error
			if errors.As(e, &conflict) && conflict.Code == "chat_session_exists" {
				err = s.DB.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat'`, w.Conversation).Scan(&session)
			}
		}
	}
	var input service.Input
	var replayed bool
	if err == nil {
		input, replayed, err = m.SubmitInputMode(channelCtx, w.Workspace, w.Principal, session, "channel_"+strings.ReplaceAll(w.ID, "-", ""), w.Text, "")
	}
	if err != nil {
		var e *service.Error
		if !errors.As(err, &e) || e.Status >= 500 {
			return err
		}
		return s.finishChannelFailure(ctx, w, e.Code)
	}
	s.channelInboxLog(ctx, w, "channel.input_persisted", "input_id", input.ID, "session_id", session, "input_seq", input.Seq, "delivery_mode", input.DeliveryMode, "replayed", replayed)
	_, err = s.DB.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='accepted',input_id=$3,lease_token=NULL,lease_until=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='processing'`, w.ID, w.Lease, input.ID)
	return err
}
func (s *Store) finishChannelFailure(ctx context.Context, w inboxWork, code string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Preserve operation error.
	result, e := tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='rejected',error_code=$3,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='processing'`, w.ID, w.Lease, code)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return nil
	}
	text := "暂时无法处理这条消息，请稍后再试。"
	if code == "configuration_unusable" || code == "platform_billing_unconfigured" {
		text = "此 AI 员工的模型配置当前不可用，请联系管理员检查员工模型设置。"
	}
	if code == "agent_disabled" {
		text = "此 AI 员工已停用，暂时无法处理消息。"
	}
	if code == "conversation_archived" || code == "session_archived" {
		text = "此会话已归档，暂时无法接收新消息。"
	}
	if _, e := tx.ExecContext(ctx, `UPDATE manager_channel_media SET locator='{}',lease_token=NULL,lease_until=NULL WHERE inbox_id=$1 AND path IS NULL`, w.ID); e != nil {
		return e
	}
	deliveryID, e := enqueueFixedChannelReply(ctx, tx, w, text)
	if e != nil {
		return e
	}
	if e = tx.Commit(); e == nil {
		s.logChannelRejection(ctx, w, code, deliveryID)
	}
	return e
}

func channelTriggerMatches(raw []byte, text string, mentioned bool) (bool, error) {
	if err := trigger.ValidateText(text); err != nil {
		return false, err
	}
	var rule struct {
		Mention  bool     `json:"mention"`
		Keywords []string `json:"keywords"`
		Regexes  []string `json:"regexes"`
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		return false, err
	}
	if rule.Mention && mentioned {
		return true, nil
	}
	for i, list := range [][]string{rule.Keywords, rule.Regexes} {
		for _, pattern := range list {
			m, err := trigger.Compile([]string{"keyword", "regex"}[i], pattern)
			if err != nil {
				return false, err
			}
			if _, hit := m.Match(text); hit {
				return true, nil
			}
		}
	}

	return false, nil
}

func (s *Store) finishChannelInbox(ctx context.Context, tx *sql.Tx, w inboxWork, code, text string) error {
	if _, e := tx.ExecContext(ctx, `UPDATE manager_channel_inbox SET status='rejected',error_code=$2,updated_at=now() WHERE id=$1`, w.ID, code); e != nil {
		return e
	}
	deliveryID, e := enqueueFixedChannelReply(ctx, tx, w, text)
	if e != nil {
		return e
	}
	if e = tx.Commit(); e == nil {
		s.logChannelRejection(ctx, w, code, deliveryID)
	}
	return e
}

func enqueueFixedChannelReply(ctx context.Context, tx *sql.Tx, w inboxWork, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	id := service.NewID()
	result, e := tx.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text) VALUES($1,$2,$3,$4,'fixed',$5) ON CONFLICT(inbox_id,kind) DO NOTHING`, id, w.ID, w.Binding, w.Generation, text)
	if e != nil {
		return "", e
	}
	if n, e := result.RowsAffected(); e != nil || n == 0 {
		return "", nil
	}
	return id, nil
}

func (s *Store) logChannelRejection(ctx context.Context, w inboxWork, code, deliveryID string) {
	s.channelInboxLog(ctx, w, "channel.inbox_rejected", "error_code", code, "delivery_id", deliveryID)
	if deliveryID != "" {
		s.channelInboxLog(ctx, w, "channel.outbox_persisted", "delivery_id", deliveryID, "kind", "fixed", "status", "pending")
	}
}

func channelBindingCurrent(enabled bool, current, want int64, boundTenant, tenant string) bool {
	return enabled && current == want && boundTenant != "" && boundTenant == tenant
}

func supportedChannelInput(provider, kind string) bool {
	switch kind {
	case "text":
		return true
	case "post":
		return provider == "feishu"
	case "richText":
		return provider == "dingtalk"
	case "mixed":
		return provider == "wecom"
	default:
		return false
	}
}

func (s *Store) prepareInboxContent(ctx context.Context, tx *sql.Tx, w *inboxWork, provider string) (bool, error) {
	finish := func(code, text string) error { return s.finishChannelInbox(ctx, tx, *w, code, text) }
	var err error
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT c.lifecycle='active' AND NOT EXISTS(SELECT 1 FROM manager_sessions s WHERE s.conversation_id=c.id AND s.mode='chat' AND s.lifecycle<>'active') FROM manager_conversations c WHERE c.id=$1`, w.Conversation).Scan(&active); err != nil {
		return false, err
	}
	if !active {
		return false, finish("conversation_archived", "此会话已归档，暂时无法接收新消息。")
	}
	refs, e := inboxMediaReferences(ctx, tx, w.ID)
	if e != nil {
		return false, e
	}
	w.Attachments = refs
	if len(refs) == 0 && (!supportedChannelInput(provider, w.MessageType) || strings.TrimSpace(w.Text) == "") {
		return false, finish("unsupported_message", "目前支持文字、图片和文件，请换一种消息格式。")
	}
	return true, nil
}
