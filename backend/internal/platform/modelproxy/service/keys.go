package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"

	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Secrets struct{ Root []byte }

func (s Secrets) cipher(purpose string) (cipher.AEAD, error) {
	if len(s.Root) < 32 {
		return nil, errors.New("model proxy secret unavailable")
	}
	key := sha256.Sum256(append([]byte("kiko:model-proxy:"+purpose+":v1:"), s.Root...))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}

func (s Secrets) Seal(purpose, id, value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	a, e := s.cipher(purpose)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, []byte(value), []byte(id)), nil
}

func (s Secrets) Open(purpose, id string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	a, e := s.cipher(purpose)
	if e != nil {
		return "", e
	}
	n := a.NonceSize()
	if len(raw) < n {
		return "", errors.New("invalid model proxy credential")
	}
	v, e := a.Open(nil, raw[:n], raw[n:], []byte(id))
	return string(v), e
}

func (s Secrets) credential(agent string) ([]byte, []byte, error) {
	var random [32]byte
	if _, e := rand.Read(random[:]); e != nil {
		return nil, nil, e
	}
	key := "kiko_agent_" + base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(key))
	encrypted, e := s.Seal("employee", agent, key)
	return hash[:], encrypted, e
}

func (s Secrets) EnsureKey(ctx context.Context, tx *sql.Tx, workspace, agent string) error {
	var payer string
	e := tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members
 WHERE workspace_id=$1 AND role='owner' AND status='active' FOR SHARE`, workspace).Scan(&payer)
	if errors.Is(e, sql.ErrNoRows) {
		return platform.Fail("platform_billing_unconfigured", 422, "工作区所有者不可用，无法设置官方模型扣费账户")
	}
	if e != nil {
		return e
	}
	if e = repo.EnsureWallet(ctx, tx, payer); e != nil {
		return e
	}
	hash, raw, e := s.credential(agent)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO platform_agent_model_keys(agent_id,workspace_id,payer_account_id,key_hash,key_ciphertext)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(agent_id) DO NOTHING`, agent, workspace, payer, hash, raw)
	return e
}

func (s Secrets) RotateKey(ctx context.Context, db *sql.DB, agent string) error {
	hash, raw, e := s.credential(agent)
	if e != nil {
		return e
	}
	var id string
	return db.QueryRowContext(ctx, `UPDATE platform_agent_model_keys SET key_hash=$2,key_ciphertext=$3,updated_at=now() WHERE agent_id=$1 RETURNING agent_id`, agent, hash, raw).Scan(&id)
}

func (s Secrets) RuntimeKey(ctx context.Context, db *sql.DB, w, agent, model, protocol string) (string, error) {
	var raw []byte
	e := db.QueryRowContext(ctx, `SELECT k.key_ciphertext FROM platform_agent_model_keys k
 JOIN platform_agents a ON a.id=k.agent_id AND a.workspace_id=k.workspace_id AND a.enabled
 JOIN platform_members p ON p.workspace_id=k.workspace_id AND p.user_id=k.payer_account_id AND p.role='owner' AND p.status='active'
 JOIN platform_proxy_models m ON m.id=$3 AND m.protocol=$4 AND m.enabled AND m.validated
 WHERE k.workspace_id=$1 AND k.agent_id=$2`, w, agent, model, protocol).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return "", platform.Fail("platform_billing_unconfigured", 422, "官方模型或扣费账户不可用，请检查员工模型设置")
	}
	if e != nil {
		return "", e
	}
	return s.Open("employee", agent, raw)
}
