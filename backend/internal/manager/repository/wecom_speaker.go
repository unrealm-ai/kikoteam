package repository

import "database/sql"

// Only a verified callback mapping can label a live WeCom user. Search IDs,
// history display names, and equal strings from another bot are not identities.
// Retained profiles affect presentation only; they never authorize execution.
func (t *sessionTx) wecomVerifiedSpeakerName(binding string, epoch int64, tenant, callback string) (string, error) {
	var name sql.NullString
	err := t.tx.QueryRowContext(t.ctx, `SELECT c.display_name FROM platform_wecom_identity_mappings m
 JOIN platform_channel_bindings b ON b.id=m.binding_id
 JOIN platform_wecom_contacts c ON c.binding_id=m.binding_id AND c.identity_epoch=m.identity_epoch AND c.connection_generation=m.connection_generation AND c.search_user_id=m.search_user_id
 WHERE b.id=$1 AND b.provider='wecom' AND b.enabled AND b.identity_epoch=$2 AND b.external_tenant_id=$3
 AND b.identity_verified_generation=b.connection_generation AND m.identity_epoch=b.identity_epoch AND m.connection_generation=b.connection_generation AND m.callback_user_id=$4`, binding, epoch, tenant, callback).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return channelLabel(name.String), nil
}
