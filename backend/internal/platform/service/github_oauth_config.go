package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type gitHubOAuthConfigReader interface {
	GitHubOAuthApp(context.Context) (OAuthAppConfig, error)
}

type githubOAuthMaterial struct {
	clientID, secret, attemptPin, grantPin string
	legacyCompatible                       bool
}

func (m githubOAuthMaterial) permitsGrant(pin string) bool {
	if m.grantPin == "" { // Not the fixed GitHub integration.
		return true
	}
	if pin == "" {
		return m.legacyCompatible
	}
	return hmac.Equal([]byte(pin), []byte(m.grantPin))
}

func (s *PluginService) gitHubOAuthMaterial(ctx context.Context, t PluginTx) (githubOAuthMaterial, error) {
	m := githubOAuthMaterial{clientID: s.GitHubClientID, secret: s.GitHubClientSecret, legacyCompatible: true}
	var revision int64
	var legacyPin string
	if app := s.OAuthApps; app != nil {
		reader, ok := t.(gitHubOAuthConfigReader)
		if !ok {
			return m, Fail("github_oauth_unconfigured", 503, "GitHub OAuth 应用配置不可用")
		}
		config, err := reader.GitHubOAuthApp(ctx)
		if err != nil {
			return m, err
		}
		revision = config.Version
		m.clientID, m.secret = app.LegacyClientID, app.LegacyClientSecret
		if revision != 0 {
			if !config.Enabled {
				return m, Fail("github_oauth_unconfigured", 503, "平台管理员未启用 GitHub OAuth 应用")
			}
			m.clientID = config.ClientID
			m.secret, err = app.decrypt(config.Ciphertext)
			if err != nil {
				return m, err
			}
			legacyPin = config.LegacyGrantPin
		}
	}
	if m.clientID == "" || m.secret == "" {
		return m, Fail("github_oauth_unconfigured", 503, "请联系平台管理员配置 GitHub OAuth 应用")
	}
	m.grantPin = gitHubAppPin(s.Secret, "grant", m.clientID, m.secret)
	m.attemptPin = gitHubAppPin(s.Secret, fmt.Sprintf("attempt:%d", revision), m.clientID, m.secret)
	if revision != 0 {
		m.legacyCompatible = hmac.Equal([]byte(m.grantPin), []byte(legacyPin))
	}
	return m, nil
}

func gitHubAppPin(key []byte, purpose, id, secret string) string {
	hash := hmac.New(sha256.New, key)
	// Length prefixes preserve field boundaries even for migrated legacy values.
	_, _ = hash.Write(fmt.Appendf(nil, "%s:%s:%d:%s:%d:%s", githubOAuthAAD, purpose, len(id), id, len(secret), secret)) // hash.Hash writes never return errors.
	return hex.EncodeToString(hash.Sum(nil))
}
func githubOAuthAppChanged() error {
	return Fail("oauth_client_changed", 409, "GitHub OAuth 应用已更新，请重新授权")
}
func githubOAuthReconnect() error {
	return Fail("oauth_reconnect", 409, "GitHub OAuth 应用已更新，需要重新连接该账号")
}
