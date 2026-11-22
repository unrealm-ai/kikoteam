package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
	"time"
)

func (s *Store) ReceiveTriggerGithub(ctx context.Context, id, signature, event, delivery string, body []byte, now time.Time) (service.TriggerReceipt, bool, error) {
	var out service.TriggerReceipt
	bad := func() (service.TriggerReceipt, bool, error) {
		return out, false, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	if !service.ValidID(id) || len(body) == 0 || len(body) > 256*1024 || !strings.HasPrefix(signature, "sha256=") {
		return bad()
	}
	sig, e := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if e != nil || len(sig) != 32 || strings.ToLower(signature) != signature {
		return bad()
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, false, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, e := lockTriggerAcceptance(ctx, tx, id)
	if e != nil {
		return out, false, hookLookupError(e)
	}
	if b.Kind != "webhook" {
		return bad()
	}
	var c service.TriggerWebhookConfig
	if e = strictJSON(b.Config, &c); e != nil || c.Adapter != "github_issue" {
		return bad()
	}
	match, e := s.verifyGithubCredential(ctx, tx, id, body, sig)
	if e != nil {
		return out, false, e
	}
	if !match {
		return bad()
	}
	if !b.Enabled {
		return out, false, service.Fail("trigger_disabled", 403, "触发器已停用")
	}
	var ignored bool
	e = finishHookIngress(ctx, tx, b, func() error {
		command, skip, err := service.GithubTriggerCommand(body, event, delivery, c)
		if err != nil {
			return err
		}
		ignored = skip
		if skip {
			return nil
		}
		hash := sha256.Sum256(body)
		out, err = s.acceptTriggerTx(ctx, tx, b, service.TriggerAcceptance{TriggerID: id, BindingVersion: b.Version, EventID: command.EventID, BodyHash: hex.EncodeToString(hash[:]), Command: command, OccurredAt: now})
		return err
	})
	return out, ignored && e == nil, e
}
func (s *Store) verifyGithubCredential(ctx context.Context, tx *sql.Tx, id string, body, sig []byte) (bool, error) {
	return s.matchTriggerCredential(ctx, tx, id, func(secret []byte) bool {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		return hmac.Equal(sig, mac.Sum(nil))
	})
}
func (s *Store) matchTriggerCredential(ctx context.Context, tx *sql.Tx, id string, matches func([]byte) bool) (bool, error) {
	a, e := s.triggerCipher()
	if e != nil {
		return false, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT key_id,ciphertext FROM manager_trigger_credentials WHERE trigger_id=$1 AND status='active' ORDER BY key_id LIMIT 5`, id)
	if e != nil {
		return false, e
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	found := false
	for rows.Next() {
		var key string
		var raw []byte
		if e = rows.Scan(&key, &raw); e != nil {
			return false, errors.Join(e, rows.Close())
		}
		if len(raw) < a.NonceSize() {
			continue
		}
		secret, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(id+":"+key))
		if err != nil {
			continue
		}
		if matches(secret) {
			found = true
		}
	}
	return found, errors.Join(rows.Err(), rows.Close())
}
