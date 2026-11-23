package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const webAttachmentColumns = `id,name,size,path,CASE WHEN status='downloading' AND expires_at<=clock_timestamp() THEN 'failed' ELSE status END`
const webAttachmentScope = `workspace_id=$1 AND conversation_id=$2 AND actor_id=$3`

func scanWebAttachment(row scanner) (service.Attachment, error) {
	var a service.Attachment
	err := row.Scan(&a.ID, &a.Name, &a.Size, &a.Path, &a.Status)
	return a, err
}

func (s *Store) ReserveWebAttachment(ctx context.Context, t service.Target, a service.Attachment, key string, ttl time.Duration) (service.Attachment, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.Attachment{}, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // A completed transaction has nothing to roll back.
	var active, permitted bool
	err = tx.QueryRowContext(ctx, `SELECT c.lifecycle='active',platform_agent_web_allowed(c.workspace_id,c.agent_id,$3)
	 FROM manager_conversations c JOIN platform_members m ON m.workspace_id=c.workspace_id AND m.user_id=$3 AND m.status='active'
	 WHERE c.workspace_id=$1 AND c.id=$2 AND c.web_user_id=$3 AND c.channel_binding_id IS NULL AND c.web_group_id IS NULL FOR UPDATE OF c`, t.Workspace, t.Conversation, t.Actor).Scan(&active, &permitted)
	if err != nil {
		return service.Attachment{}, false, visibleError(err)
	}
	if !active || !permitted {
		return service.Attachment{}, false, service.Fail("permission_denied", 403, "此会话不可上传附件")
	}
	existing, err := scanWebAttachment(tx.QueryRowContext(ctx, `SELECT `+webAttachmentColumns+` FROM manager_web_attachments WHERE `+webAttachmentScope+` AND request_key=$4`, t.Workspace, t.Conversation, t.Actor, key))
	if err == nil {
		if existing.Size != a.Size || existing.Name != a.Name {
			return service.Attachment{}, false, service.Fail("idempotency_conflict", 409, "去重键已用于其他附件")
		}
		return existing, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return service.Attachment{}, false, err
	}
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM manager_web_attachments WHERE `+webAttachmentScope+` AND status='downloading' AND expires_at>now()`, t.Workspace, t.Conversation, t.Actor).Scan(&pending)
	if err != nil {
		return service.Attachment{}, false, err
	}
	if pending >= 32 {
		return service.Attachment{}, false, service.Fail("attachment_limit", 429, "待上传附件过多")
	}
	out, err := scanWebAttachment(tx.QueryRowContext(ctx, `INSERT INTO manager_web_attachments(id,workspace_id,conversation_id,actor_id,request_key,name,size,expires_at)
	 VALUES($4,$1,$2,$3,$5,$6,$7,now()+($8::bigint*interval '1 second')) RETURNING `+webAttachmentColumns, t.Workspace, t.Conversation, t.Actor, a.ID, key, a.Name, a.Size, int64(ttl/time.Second)))
	if err != nil {
		return service.Attachment{}, false, err
	}
	return out, false, tx.Commit()
}

func (s *Store) WebAttachment(ctx context.Context, t service.Target, id string) (service.Attachment, error) {
	out, err := scanWebAttachment(s.DB.QueryRowContext(ctx, `SELECT `+webAttachmentColumns+` FROM manager_web_attachments a
	 WHERE `+webAttachmentScope+` AND id=$4 AND EXISTS(SELECT FROM platform_members m WHERE m.workspace_id=a.workspace_id AND m.user_id=a.actor_id AND m.status='active')`, t.Workspace, t.Conversation, t.Actor, id))
	return out, visibleError(err)
}

func (s *Store) BeginWebAttachment(ctx context.Context, t service.Target, id, token string) (service.Attachment, error) {
	out, err := scanWebAttachment(s.DB.QueryRowContext(ctx, `UPDATE manager_web_attachments a SET upload_token=$5,updated_at=now()
	 WHERE `+webAttachmentScope+` AND id=$4 AND upload_token IS NULL AND status='downloading' AND expires_at>now()
	 AND EXISTS(SELECT FROM platform_members m WHERE m.workspace_id=a.workspace_id AND m.user_id=a.actor_id AND m.status='active')
	 RETURNING `+webAttachmentColumns, t.Workspace, t.Conversation, t.Actor, id, token))
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out, err = s.WebAttachment(ctx, t, id)
	if err != nil || out.Status == "ready" {
		return out, err
	}
	return service.Attachment{}, service.Fail("attachment_not_uploadable", 409, "附件已失效或正在上传")
}

// The usual workspace, employee and private conversation locks serialize access
// checks with changes. The row lock pins the claim, with expiry checked before
// the filesystem becomes visible. No retry wrapper: publication is an external effect.
func (s *Store) PublishWebAttachment(ctx context.Context, t service.Target, id, token string, publish func(service.SessionState) (string, error)) (service.Attachment, error) {
	var out service.Attachment
	err := s.withSessionAttempt(ctx, t, func(owner service.SessionTx) error {
		tx := owner.(*sessionTx).tx
		var claimed string
		args := []any{t.Workspace, t.Conversation, t.Actor, id, token}
		err := tx.QueryRowContext(ctx, `SELECT id FROM manager_web_attachments WHERE `+webAttachmentScope+` AND id=$4
		 AND status='downloading' AND upload_token=$5 AND expires_at>clock_timestamp() FOR UPDATE`, args...).Scan(&claimed)
		if err != nil {
			return webAttachmentClaimError(err)
		}
		path, err := publish(owner.State())
		if err != nil {
			return err
		}
		out, err = scanWebAttachment(tx.QueryRowContext(ctx, `UPDATE manager_web_attachments SET status='ready',path=$6,upload_token=NULL,updated_at=clock_timestamp()
		 WHERE `+webAttachmentScope+` AND id=$4 AND upload_token=$5 AND status='downloading' RETURNING `+webAttachmentColumns, append(args, path)...))
		return webAttachmentClaimError(err)
	}, false)
	return out, err
}

func webAttachmentClaimError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("attachment_not_uploadable", 409, "附件上传已失效")
	}
	return err
}

func (s *Store) FinishWebAttachmentFailure(ctx context.Context, t service.Target, id, token string) error {
	// A stale attempt must not overwrite a newer terminal state.
	_, err := s.DB.ExecContext(ctx, `UPDATE manager_web_attachments SET status='failed',upload_token=NULL,updated_at=clock_timestamp()
	 WHERE `+webAttachmentScope+` AND id=$4 AND upload_token=$5 AND status='downloading'`, t.Workspace, t.Conversation, t.Actor, id, token)
	return err
}

func (t *sessionTx) resolveWebAttachmentIDs(ids []string) ([]service.Attachment, error) {
	files := make([]service.Attachment, 0, len(ids))
	c := t.state.Conversation
	if c.WebUserID == nil || *c.WebUserID != t.target.Actor || c.ChannelBindingID != nil {
		return nil, service.Fail("resource_not_found", 404, "附件不存在")
	}
	for _, id := range ids {
		item, err := scanWebAttachment(t.tx.QueryRowContext(t.ctx, `SELECT `+webAttachmentColumns+` FROM manager_web_attachments WHERE `+webAttachmentScope+` AND id=$4`, c.WorkspaceID, c.ID, t.target.Actor, id))
		if err != nil {
			return nil, visibleError(err)
		}
		files = append(files, item)
	}
	return files, nil
}
