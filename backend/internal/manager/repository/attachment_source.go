package repository

import "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"

// Delegation may forward files the user actually referenced in this private chat.
func (t *sessionTx) checkAttachmentSource(files []service.Attachment) error {
	if t.target.Source == nil {
		return nil
	}
	channel, _ := service.ChannelActorFrom(t.ctx)
	for _, f := range files {
		var allowed bool
		err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM manager_inputs i WHERE i.session_id=$1 AND i.source_kind='user' AND (i.submitted_by=$2 OR (i.channel_principal_id=$2 AND i.channel_generation=$4 AND i.trigger_agent_loop)) AND i.attachments @> jsonb_build_array(jsonb_build_object('path',$3::text)))`, t.target.Source.SessionID, t.target.Actor, f.Path, channel.Generation).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return service.Fail("permission_denied", 403, "当前会话中该发送人未引用此附件")
		}
	}
	return nil
}
