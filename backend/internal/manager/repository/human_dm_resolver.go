package repository

import (
	"context"
	"database/sql"
	"errors"
)

type humanDMSelection struct {
	id      string
	version int64
}

// Match only provider-proven original confirmation notices. The model uses
// the matched reference as evidence in its interpretation of the reply.
func findHumanDMSent(ctx context.Context, tx *sql.Tx, viewer humanViewer, quote string) (humanDMSelection, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT r.id,d.request_version FROM manager_human_requests r
 JOIN manager_human_notices d ON d.request_id=r.id AND d.recipient=r.recipient
 JOIN manager_channel_inbox n ON n.id=NULLIF($6,'')::uuid
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 WHERE `+humanAnswerTarget+` AND d.status='sent' AND d.sent_at<=n.created_at
 AND d.workspace_id=r.workspace_id AND d.agent_id=r.agent_id AND d.binding_id=n.binding_id
 AND d.tenant_id=n.external_tenant_id AND d.connection_generation=n.connection_generation
 AND d.config_revision=n.config_revision AND b.config_revision=d.config_revision AND d.identity_epoch=n.identity_epoch
 AND d.provider='feishu' AND d.provider_chat_id=n.external_chat_id AND d.provider_message_id=$7
 LIMIT 2`, append(viewer.queryArgs(), quote)...)
	var selection humanDMSelection
	if err != nil {
		return selection, err
	}
	if rows.Next() {
		err = rows.Scan(&selection.id, &selection.version)
		if err == nil && rows.Next() {
			selection = humanDMSelection{}
		}
	}
	return selection, errors.Join(err, rows.Err(), rows.Close())
}
