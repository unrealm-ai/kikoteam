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
	var m githubOAuthMaterial
	reader, ok := t.(gitHubOAuthConfigReader)
	if !ok || s.OAuthApps == nil {
		return m, Fail("github_oauth_unconfigured", 503, "GitHub OAuth 应用配置不可用")
	}
	config, err := reader.GitHubOAuthApp(ctx)
	if err != nil {
		return m, err
	}
	if !config.Enabled || config.ClientID == "" || len(config.Ciphertext) == 0 {
		return m, Fail("github_oauth_unconfigured", 503, "请联系平台管理员在平台管理中配置并启用 GitHub OAuth 应用")
	}
	m.clientID = config.ClientID
	m.secret, err = s.OAuthApps.decrypt(config.Ciphertext)
	if err != nil {
		return m, err
	}
	m.grantPin = gitHubAppPin(s.Secret, "grant", m.clientID, m.secret)
	m.attemptPin = gitHubAppPin(s.Secret, fmt.Sprintf("attempt:%d", config.Version), m.clientID, m.secret)
	// Keep accepting unpinned grants already migrated to the same application.
	m.legacyCompatible = hmac.Equal([]byte(m.grantPin), []byte(config.LegacyGrantPin))
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
