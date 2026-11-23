package repository

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// ResolveTaskAttachmentIDs copies only a persisted grant from the source chat.
// For direct Web input, the reservation is consumed in this same transaction.
func (t *sessionTx) ResolveTaskAttachmentIDs(ids []string) ([]service.Attachment, error) {
	if t.target.Source == nil {
		return t.resolveWebAttachmentIDs(ids)
	}
	channel, isChannel := service.ChannelActorFrom(t.ctx)
	refs := make([]service.Attachment, 0, len(ids))
	for _, id := range ids {
		var raw []byte
		err := t.tx.QueryRowContext(t.ctx, `SELECT ref.value
    FROM manager_inputs i
    CROSS JOIN LATERAL jsonb_array_elements(i.attachments) ref
    WHERE i.workspace_id=$1 AND i.session_id=$2 AND i.source_kind='user' AND i.trigger_agent_loop
      AND i.status NOT IN ('canceled','rejected')
      AND (($5 AND i.channel_principal_id=$3 AND i.channel_generation=$4)
           OR (NOT $5 AND i.submitted_by=$3 AND i.channel_principal_id IS NULL))
      AND EXISTS(SELECT 1 FROM manager_inputs current_i
        WHERE current_i.id=$7 AND current_i.workspace_id=i.workspace_id AND current_i.session_id=i.session_id
          AND current_i.source_kind='user' AND current_i.trigger_agent_loop
          AND (($5 AND current_i.channel_principal_id=$3 AND current_i.channel_generation=$4)
            OR (NOT $5 AND current_i.submitted_by=$3 AND current_i.channel_principal_id IS NULL)))
      AND i.attachments @> jsonb_build_array(jsonb_build_object('id',$6::text))
      AND ref.value->>'id'=$6
    ORDER BY i.seq DESC LIMIT 1`, t.target.Workspace, t.target.Source.SessionID, t.target.Actor, channel.Generation, isChannel, id, nullID(t.target.SourceInputID)).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.Fail("permission_denied", 403, "当前会话中该发送人未引用此附件")
		}
		if err != nil {
			return nil, err
		}
		var f service.Attachment
		if err = json.Unmarshal(raw, &f); err != nil {
			return nil, err
		}
		switch f.Status {
		case "ready", "downloading", "failed":
			f.Path = ""
		default:
			return nil, service.Fail("attachment_not_available", 409, "附件状态无效，无法转交")
		}
		refs = append(refs, f)
	}
	return refs, nil
}

// Delegation may forward files the user actually referenced in this chat.
func (t *sessionTx) checkAttachmentSource(files []service.Attachment) error {
	if t.target.Source == nil {
		return nil
	}
	channel, _ := service.ChannelActorFrom(t.ctx)
	for _, f := range files {
		if f.ID != "" {
			if _, err := t.ResolveTaskAttachmentIDs([]string{f.ID}); err != nil {
				return err
			}
			continue
		}
		var allowed bool
		err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM manager_inputs i WHERE i.session_id=$1 AND i.source_kind='user' AND (i.submitted_by=$2 OR (i.channel_principal_id=$2 AND i.channel_generation=$4 AND i.trigger_agent_loop)) AND EXISTS(SELECT 1 FROM jsonb_array_elements(i.attachments) ref WHERE ref->>'path'=$3 AND COALESCE(ref->>'id','')=''))`, t.target.Source.SessionID, t.target.Actor, f.Path, channel.Generation).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return service.Fail("permission_denied", 403, "当前会话中该发送人未引用此附件")
		}
	}
	return nil
}
