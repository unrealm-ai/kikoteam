package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// This is a bounded keyword match over already verified local profiles, not an
// organization directory. Old search profiles remain display data, not proof of
// a new identity or renewed freshness for a verification challenge.
func (s *WecomContactStore) MatchVerifiedWecomContacts(ctx context.Context, b service.ChannelBinding, actor, q string) (service.WecomContactSearch, error) {
	out := service.WecomContactSearch{Items: []service.WecomContact{}}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read-only transaction.
	if err = lockWecomContactSource(ctx, tx, b, actor); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.search_user_id,c.display_name,c.department_paths,m.callback_user_id,m.verification_id FROM platform_wecom_contacts c
 JOIN platform_wecom_identity_mappings m ON m.binding_id=c.binding_id AND m.identity_epoch=c.identity_epoch AND m.connection_generation=c.connection_generation AND m.search_user_id=c.search_user_id
 WHERE c.binding_id=$1 AND c.identity_epoch=$2 AND c.connection_generation=$3 AND $4<>'' AND (position(lower($4) in lower(c.display_name))>0 OR position(lower($4) in lower(c.search_user_id))>0 OR position(lower($4) in lower(c.department_paths::text))>0)
 ORDER BY c.search_user_id LIMIT 101`, b.ID, b.IdentityEpoch, b.Generation, q)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration checked below.
	for rows.Next() {
		var v service.WecomContact
		var paths []byte
		if err = rows.Scan(&v.SearchUserID, &v.DisplayName, &paths, &v.CallbackUserID, &v.VerificationID); err != nil {
			return out, err
		}
		if err = json.Unmarshal(paths, &v.DepartmentPaths); err != nil {
			return out, err
		}
		v.VerificationStatus = "verified"
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Limited = len(out.Items) > 100
	if out.Limited {
		out.Items = out.Items[:100]
	}
	return out, nil
}
