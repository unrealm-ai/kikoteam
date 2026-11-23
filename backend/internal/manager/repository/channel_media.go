package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func validInboundMedia(refs []service.MediaReference) bool {
	if len(refs) > 32 {
		return false
	}
	for _, m := range refs {
		if m.Kind != "image" && m.Kind != "file" || len(m.Name) > 240 || strings.ContainsAny(m.Name, "/\\\r\n\x00") || m.Name == "." || m.Name == ".." || m.Locator == "" || len(m.Locator) > 16384 {
			return false
		}
	}
	return true
}
func inboxMediaReferences(ctx context.Context, tx *sql.Tx, id string) ([]service.Attachment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,name,CASE WHEN path IS NOT NULL THEN 'ready' WHEN error_code IS NOT NULL THEN 'failed' ELSE 'downloading' END FROM manager_channel_media WHERE inbox_id=$1 ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned.
	var out []service.Attachment
	for rows.Next() {
		var n int
		var a service.Attachment
		if err = rows.Scan(&n, &a.Name, &a.Status); err != nil {
			return nil, err
		}
		a.ID = fmt.Sprintf("%s:%d", id, n)
		out = append(out, a)
	}
	return out, rows.Err()
}

const mediaAllowed = `n.status IN ('pending','processing','accepted') AND EXISTS(SELECT FROM manager_conversations c JOIN platform_agents a ON a.id=c.agent_id WHERE c.id=n.conversation_id AND c.lifecycle='active' AND a.enabled) AND n.principal_id IS NOT NULL AND b.enabled AND b.connection_generation=n.connection_generation AND b.identity_epoch=n.identity_epoch AND b.external_tenant_id=n.external_tenant_id AND manager_channel_actor_allowed(n.principal_id,n.conversation_id,n.connection_generation)`

const mediaMaxAttempts = 3

// Claiming a lease consumes one download-and-store attempt. Lost workers
// cannot evade the budget: an expired final lease is reconciled, not reclaimed.
func (s *Store) ClaimMedia(ctx context.Context, mgr *service.Service) (*service.MediaJob, error) {
	if err := s.reconcileExpiredMedia(ctx, mgr); err != nil {
		return nil, err
	}
	var j service.MediaJob
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `WITH candidate AS (SELECT m.inbox_id,m.ordinal FROM manager_channel_media m JOIN manager_channel_inbox n ON n.id=m.inbox_id JOIN platform_channel_bindings b ON b.id=n.binding_id WHERE `+mediaAllowed+`
 AND m.path IS NULL AND m.error_code IS NULL AND m.attempts<$1 AND (m.lease_until IS NULL OR m.lease_until<=now()) ORDER BY n.created_at,m.ordinal FOR UPDATE OF m SKIP LOCKED LIMIT 1)
 UPDATE manager_channel_media m SET lease_token=gen_random_uuid(),lease_until=now()+interval '2 minutes',attempts=attempts+1
 FROM candidate c,manager_channel_inbox n,platform_channel_bindings b WHERE (m.inbox_id,m.ordinal)=(c.inbox_id,c.ordinal) AND n.id=m.inbox_id AND b.id=n.binding_id
 RETURNING m.inbox_id,m.ordinal,m.lease_token,m.locator,b.id,b.provider,n.connection_generation,n.provider_message_id,
 b.provider IN ('dingtalk','wecom') AND NOT COALESCE((SELECT validate_public_ip FROM platform_channel_download_settings WHERE singleton),true)`, mediaMaxAttempts).
		Scan(&j.InboxID, &j.Ordinal, &j.Lease, &raw, &j.BindingID, &j.Provider, &j.Generation, &j.MessageID, &j.AllowNonPublicIP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &j.MediaReference)
	return &j, err
}

type mediaSettlement struct {
	id, lease, principal, binding, name string
	index                               int
	gen                                 int64
	c                                   service.Conversation
}

func (m *mediaSettlement) uploadContext(ctx context.Context) context.Context {
	m.c.Lifecycle = "active"
	m.c.ChannelBindingID = &m.binding
	return service.WithChannelActor(ctx, service.ChannelActor{ID: m.principal, Generation: m.gen, InboxID: m.id})
}

func (m mediaSettlement) uploadKey() string {
	return fmt.Sprintf("channel_%s_%d", m.id, m.index)
}

const mediaSettlementSelect = `SELECT m.inbox_id,m.ordinal,COALESCE(m.lease_token::text,''),n.conversation_id,b.workspace_id,b.agent_id,n.principal_id,b.id,n.connection_generation,m.name
 FROM manager_channel_media m JOIN manager_channel_inbox n ON n.id=m.inbox_id JOIN platform_channel_bindings b ON b.id=n.binding_id
 WHERE m.path IS NULL AND m.error_code IS NULL AND ` + mediaAllowed

func readMediaSettlement(row *sql.Row) (mediaSettlement, error) {
	var m mediaSettlement
	err := row.Scan(&m.id, &m.index, &m.lease, &m.c.ID, &m.c.WorkspaceID, &m.c.AgentID, &m.principal, &m.binding, &m.gen, &m.name)
	return m, err
}

func (s *Store) reconcileExpiredMedia(ctx context.Context, mgr *service.Service) error {
	m, err := readMediaSettlement(s.DB.QueryRowContext(ctx, mediaSettlementSelect+`
 AND m.attempts>=$1 AND (m.lease_until IS NULL OR m.lease_until<=now()) ORDER BY n.created_at,m.ordinal LIMIT 1`, mediaMaxAttempts))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = s.reconcileMedia(ctx, mgr, m, true)
	var problem *service.Error
	if errors.As(err, &problem) && problem.Code == "lease_lost" {
		return nil // Another reconciliation settled this row first.
	}
	return err
}

func (s *Store) FinishMedia(ctx context.Context, mgr *service.Service, id string, index int, lease string, data []byte, failed bool, providerNames ...string) error {
	if !service.ValidID(id) || !service.ValidID(lease) || index < 0 || index >= 32 {
		return service.Fail("invalid_argument", 400, "附件身份无效")
	}
	m, err := readMediaSettlement(s.DB.QueryRowContext(ctx, mediaSettlementSelect+`
 AND m.inbox_id=$1 AND m.ordinal=$2 AND m.lease_token=$3 AND m.lease_until>now()`, id, index, lease))
	if err != nil {
		return visibleError(err)
	}
	if failed {
		return s.reconcileMedia(ctx, mgr, m, false)
	}
	if len(providerNames) == 1 && providerNames[0] != "" {
		// Bind the independently downloaded filename to this attempt so a lost
		// response can recover the same upload journal entry.
		err = s.DB.QueryRowContext(ctx, `UPDATE manager_channel_media z SET name=$4
 WHERE z.inbox_id=$1 AND z.ordinal=$2 AND z.lease_token=$3 AND z.lease_until>now()
 AND z.path IS NULL AND z.error_code IS NULL AND EXISTS(SELECT 1 FROM manager_channel_inbox n
 JOIN platform_channel_bindings b ON b.id=n.binding_id WHERE n.id=z.inbox_id AND b.provider='wecom' AND `+mediaAllowed+`)
 RETURNING z.name`, id, index, lease, providerNames[0]).Scan(&m.name)
		if err != nil {
			return visibleError(err)
		}
	}
	ctx = m.uploadContext(ctx)
	storageName := m.name
	if storageName == "" {
		storageName = "attachment" // A storage basename is not a sender-provided filename.
	}
	file, err := mgr.UploadChannelMedia(ctx, m.c, m.principal, m.uploadKey(), storageName, bytes.NewReader(data), func() error {
		var present int
		err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM manager_channel_media m JOIN manager_channel_inbox n ON n.id=m.inbox_id
 JOIN platform_channel_bindings b ON b.id=n.binding_id WHERE `+mediaAllowed+`
 AND m.inbox_id=$1 AND m.ordinal=$2 AND m.lease_token=$3 AND m.lease_until>now() AND m.path IS NULL AND m.error_code IS NULL`, id, index, lease).Scan(&present)
		return visibleError(err)
	})
	if err != nil {
		return err // The same-lease failure callback or lease expiry reconciles publication.
	}
	return s.settleMedia(ctx, m, file.Path, false)
}

func (s *Store) reconcileMedia(ctx context.Context, mgr *service.Service, m mediaSettlement, expired bool) error {
	settlementAttempted := false
	err := mgr.ReconcileChannelMedia(m.uploadContext(ctx), m.c, m.principal, m.uploadKey(), func(path string) error {
		settlementAttempted = true
		return s.settleMedia(ctx, m, path, expired)
	})
	if err == nil || settlementAttempted || ctx.Err() != nil {
		return err
	}
	// Unreadable storage is also a failed attempt, not an excuse to block the
	// inbox forever. A database failure above is still retried by the worker.
	return s.settleMedia(ctx, m, "", expired)
}

func (s *Store) settleMedia(ctx context.Context, m mediaSettlement, path string, expired bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE manager_channel_media m SET
 path=NULLIF($4,''),error_code=CASE WHEN $4='' AND m.attempts >= $6 THEN 'unavailable' END,
 locator=CASE WHEN $4<>'' OR m.attempts >= $6 THEN '{}'::jsonb ELSE m.locator END,
 lease_token=NULL,lease_until=CASE WHEN $4='' AND m.attempts < $6 THEN now()+interval '5 seconds' END
 FROM manager_channel_inbox n,platform_channel_bindings b
 WHERE n.id=m.inbox_id AND b.id=n.binding_id AND `+mediaAllowed+`
 AND m.inbox_id=$1 AND m.ordinal=$2 AND COALESCE(m.lease_token::text,'')=$3 AND m.path IS NULL AND m.error_code IS NULL
 AND ((NOT $5 AND m.lease_until>now()) OR ($5 AND m.attempts>=$6 AND (m.lease_until IS NULL OR m.lease_until<=now())))`, m.id, m.index, m.lease, path, expired, mediaMaxAttempts)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.Fail("lease_lost", 409, "附件租约已变更")
	}
	return nil
}
