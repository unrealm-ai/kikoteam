package repository

import (
	"encoding/json"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// insertChannelHistory keeps source messages separate in storage. The Engine,
// not admission, renders the eventual single native Pi prompt. All rows inherit
// the admitted event's authority; historical speakers are quoted data only.
func (t *sessionTx) insertChannelHistory(current service.Input) (int, error) {
	actor, ok := service.ChannelActorFrom(t.ctx)
	if !ok || actor.InboxID == "" {
		return 0, nil
	}
	var kind string
	if err := t.tx.QueryRowContext(t.ctx, `SELECT chat_type FROM manager_channel_inbox WHERE id=$1 AND principal_id=$2 AND conversation_id=$3 AND status='processing'`, actor.InboxID, actor.ID, t.target.Conversation).Scan(&kind); err != nil {
		return 0, err
	}
	if kind != "group" {
		return 0, nil
	}
	items, err := channelHistoryItems(t.ctx, t.tx, actor.InboxID)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		isUser := item.RecordKey != "" || item.SenderType == "" || item.SenderType == "user"
		name, mentions, e := t.channelPresentation(item.SenderID, service.ChannelMessageMetadata{SenderName: item.SenderName, SenderTenant: item.SenderTenant, SenderIDType: item.SenderIDType, Mentions: item.Mentions}, isUser && item.RecordKey == "")
		if e != nil {
			return 0, e
		}
		if item.RecordKey != "" {
			name = channelLabel(item.SenderName)
			if name == "" {
				name = "未知成员"
			}
			mentions = nil
		}
		text := renderSystemMessage(item.Text, mentions)
		if isUser {
			text = renderGroupMessage(name, item.Text, mentions)
		}
		disabled := false
		input := service.Input{ID: service.NewID(), SessionID: current.SessionID, Text: text, Status: "queued", DeliveryMode: "queue", SourceKind: "user", TriggerAgentLoop: &disabled, CreatedAt: current.CreatedAt, ChannelPrincipalID: &actor.ID, PrincipalType: "channel"}
		if item.RecordKey == "" && item.SenderType != "" && item.SenderType != "user" {
			input.SourceKind = "system"
		}
		if err = t.tx.QueryRowContext(t.ctx, `UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq`, current.SessionID).Scan(&input.Seq); err != nil {
			return 0, err
		}
		_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,admission_epoch,seq,text,created_at,channel_principal_id,channel_generation,context_state,delivery_mode,source_kind,trigger_agent_loop,channel_history_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending','queue',$10,false,$11)`, input.ID, t.target.Workspace, input.SessionID, t.state.AdmissionEpoch, input.Seq, input.Text, input.CreatedAt, actor.ID, actor.Generation, input.SourceKind, len(items))
		if err != nil {
			return 0, err
		}
		if err = t.Event("input.updated", &input.ID, nil, service.InputFields(input)); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func channelHistoryInputText(item service.ChannelHistoryItem) string {
	if item.RecordKey != "" {
		name := channelLabel(item.SenderName)
		if name == "" {
			name = "未知成员"
		}
		return renderGroupMessage(name, item.Text, nil)
	}
	if item.RecordKey == "" && item.SenderType != "" && item.SenderType != "user" {
		return renderSystemMessage(item.Text, item.Mentions)
	}
	return renderGroupMessage("未知用户", item.Text, item.Mentions)
}

// Render the current channel message from trusted inbox metadata rather than
// accepting a client-supplied speaker identity.
func (t *sessionTx) channelInputText(input service.Input) (string, error) {
	if input.SourceKind != "" && input.SourceKind != "user" {
		return "[系统消息]" + strings.TrimPrefix(input.Text, "[系统消息]"), nil
	}
	actor, ok := service.ChannelActorFrom(t.ctx)
	if !ok || actor.InboxID == "" {
		return input.Text, nil
	}
	var kind, sender string
	var raw []byte
	if err := t.tx.QueryRowContext(t.ctx, `SELECT chat_type,sender_id,message_metadata FROM manager_channel_inbox WHERE id=$1 AND principal_id=$2 AND conversation_id=$3`, actor.InboxID, actor.ID, t.target.Conversation).Scan(&kind, &sender, &raw); err != nil {
		return "", err
	}
	var metadata service.ChannelMessageMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return "", err
	}
	text := input.Text
	if kind == "group" {
		name, mentions, err := t.channelPresentation(sender, metadata, true)
		if err != nil {
			return "", err
		}
		text = renderGroupMessage(name, text, mentions)
	}
	if metadata.QuotedText != "" {
		text += "\n\n[用户引用的内容，原始身份未核验]\n" + metadata.QuotedText
	}
	return text, nil
}
