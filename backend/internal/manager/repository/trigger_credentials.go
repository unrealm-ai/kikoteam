package repository

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"io"
	"strconv"
	"time"
	"unicode/utf8"
)

func strictJSON(raw []byte, v any) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("extra JSON")
	}
	return nil
}
func (s *Store) triggerCipher() (cipher.AEAD, error) {
	if len(s.TriggerSecret) < 32 {
		return nil, service.Fail("trigger_secret_unavailable", 503, "凭据服务未配置")
	}
	key := sha256.Sum256(append([]byte("kiko:trigger-credential:v1:"), s.TriggerSecret...))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func (s *Store) CreateTriggerCredential(ctx context.Context, w, actor, agent, id string) (service.TriggerCredential, error) {
	return s.saveTriggerCredential(ctx, w, actor, agent, id, "")
}
func (s *Store) ImportTriggerCredential(ctx context.Context, w, actor, agent, id, token string) (service.TriggerCredential, error) {
	if !service.ValidGitLabSigningToken(token) {
		return service.TriggerCredential{}, service.Fail("invalid_signing_token", 400, "GitLab Signing token 格式无效")
	}
	return s.saveTriggerCredential(ctx, w, actor, agent, id, token)
}
func (s *Store) saveTriggerCredential(ctx context.Context, w, actor, agent, id, token string) (service.TriggerCredential, error) {
	var out service.TriggerCredential
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		var kind, source string
		if e := tx.QueryRowContext(ctx, `SELECT kind,COALESCE(config->>'source','') FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, w, agent, id).Scan(&kind, &source); e != nil {
			return visibleError(e)
		}
		if kind != "webhook" {
			return service.Fail("invalid_argument", 400, "仅Hook需要凭据")
		}
		if (source == "gitlab") != (token != "") {
			return service.Fail("invalid_trigger_credential", 400, "仅 GitLab 签名模式使用导入的 signing token")
		}
		var err error
		out, err = s.insertTriggerCredential(ctx, tx, id, token)
		return err
	})
	return out, err
}

// The caller owns the new or locked trigger row until commit.
func (s *Store) insertTriggerCredential(ctx context.Context, tx *sql.Tx, id, token string) (service.TriggerCredential, error) {
	var out service.TriggerCredential
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM manager_trigger_credentials WHERE trigger_id=$1 AND status='active'`, id).Scan(&active); err != nil {
		return out, err
	}
	if active >= 5 {
		return out, service.Fail("credential_limit", 409, "有效凭据最多五个")
	}
	a, err := s.triggerCipher()
	if err != nil {
		return out, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return out, err
	}
	out = service.TriggerCredential{KeyID: service.NewID(), Status: "active"}
	if token == "" {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return out, err
		}
		token = base64.RawURLEncoding.EncodeToString(secret)
		out.Secret = token
	}
	sealed := a.Seal(nonce, nonce, []byte(token), []byte(id+":"+out.KeyID))
	err = tx.QueryRowContext(ctx, `INSERT INTO manager_trigger_credentials(trigger_id,key_id,ciphertext) VALUES($1,$2,$3) RETURNING created_at`, id, out.KeyID, sealed).Scan(&out.CreatedAt)
	return out, err
}
func (s *Store) TriggerCredentials(ctx context.Context, w, actor, agent, id string) ([]service.TriggerCredential, error) {
	out := []service.TriggerCredential{}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		if _, e := scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3`, w, agent, id)); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT key_id,status,created_at FROM manager_trigger_credentials WHERE trigger_id=$1 ORDER BY created_at DESC,key_id`, id)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
		for rows.Next() {
			var v service.TriggerCredential
			if e = rows.Scan(&v.KeyID, &v.Status, &v.CreatedAt); e != nil {
				return errors.Join(e, rows.Close())
			}
			out = append(out, v)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return out, err
}
func (s *Store) RevokeTriggerCredential(ctx context.Context, w, actor, agent, id, key string) error {
	return s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		b, e := scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, w, agent, id))
		if e != nil {
			return e
		}
		var got string
		if e = tx.QueryRowContext(ctx, `UPDATE manager_trigger_credentials SET status='revoked' WHERE trigger_id=$1 AND key_id=$2 RETURNING key_id`, id, key).Scan(&got); e != nil {
			return visibleError(e)
		}
		// Rotation revocation fences pending/in-flight runs; another key cannot revive the old binding generation.
		if _, e = tx.ExecContext(ctx, `UPDATE manager_triggers SET version=version+1,updated_at=now(),next_due=NULL WHERE id=$1`, id); e != nil {
			return e
		}
		return s.stopTriggerSessions(ctx, tx, b)
	})
}
func (s *Store) ReceiveTriggerWebhook(ctx context.Context, id, key, timestamp, signature string, body []byte, now time.Time) (service.TriggerReceipt, error) {
	var out service.TriggerReceipt
	if !service.ValidID(id) || !service.ValidID(key) {
		return out, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	b, e := lockTriggerAcceptance(ctx, tx, id)
	if e != nil {
		return out, hookLookupError(e)
	}
	var config service.TriggerWebhookConfig
	if parseErr := strictJSON(b.Config, &config); parseErr != nil {
		return out, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	if b.Kind != "webhook" || config.Adapter != "generic" {
		return out, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	var sealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT ciphertext FROM manager_trigger_credentials WHERE trigger_id=$1 AND key_id=$2 AND status='active'`, id, key).Scan(&sealed); e != nil {
		return out, hookLookupError(e)
	}
	a, e := s.triggerCipher()
	if e != nil {
		return out, e
	}
	if len(sealed) < a.NonceSize() {
		return out, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	plain, e := a.Open(nil, sealed[:a.NonceSize()], sealed[a.NonceSize():], []byte(id+":"+key))
	if e != nil {
		return out, service.Fail("trigger_authentication_failed", 401, "触发器认证失败")
	}
	digest, e := service.VerifyTriggerSignature(id, string(plain), timestamp, signature, body, now)
	if e != nil {
		return out, e
	}
	if !b.Enabled {
		return out, service.Fail("trigger_binding_changed", 403, "触发器已停用或配置已变化")
	}
	e = finishHookIngress(ctx, tx, b, func() error {
		var command service.TriggerCommand
		if err := strictJSON(body, &command); err != nil {
			return service.Fail("invalid_trigger_command", 400, "事件格式无效")
		}
		ms, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			return service.Fail("trigger_authentication_failed", 401, "时间无效")
		}
		out, err = s.acceptTriggerTx(ctx, tx, b, service.TriggerAcceptance{TriggerID: id, BindingVersion: b.Version, EventID: command.EventID, BodyHash: digest, Command: command, OccurredAt: time.UnixMilli(ms)})
		return err
	})
	return out, e
}
