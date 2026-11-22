package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strings"
)

const githubOAuthAAD = "kiko:oauth-app:plugin/github:v1"

type OAuthAppConfig struct {
	Version        int64
	Enabled        bool
	ClientID       string
	Ciphertext     []byte
	LegacyGrantPin string
}

type OAuthAppStatus struct {
	Provider        string `json:"provider"`
	Purpose         string `json:"purpose"`
	Version         int64  `json:"version"`
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"client_id"`
	HasClientSecret bool   `json:"has_client_secret"`
	Source          string `json:"source"`
	RedirectURI     string `json:"redirect_uri"`
}

type OAuthAppPatch struct {
	ExpectedVersion   *int64  `json:"expected_version"`
	Enabled           *bool   `json:"enabled"`
	ClientID          *string `json:"client_id,omitempty"`
	ClientSecret      *string `json:"client_secret,omitempty"`
	ClearClientSecret bool    `json:"clear_client_secret,omitempty"`
}

type OAuthAppRepository interface {
	ReadGitHubAdmin(context.Context, string) (OAuthAppConfig, error)
	UpdateGitHubAdmin(context.Context, string, func(OAuthAppConfig) (OAuthAppConfig, error)) (OAuthAppConfig, error)
}

type OAuthAppService struct {
	Store                                           OAuthAppRepository
	Secret                                          []byte
	LegacyClientID, LegacyClientSecret, RedirectURI string
}

func (s *OAuthAppService) GitHubStatus(ctx context.Context, actor string) (OAuthAppStatus, error) {
	c, err := s.Store.ReadGitHubAdmin(ctx, actor)
	if err != nil {
		return OAuthAppStatus{}, err
	}
	return s.status(c), nil
}

func (s *OAuthAppService) status(c OAuthAppConfig) OAuthAppStatus {
	out := OAuthAppStatus{Provider: "github", Purpose: "plugin", Version: c.Version, Enabled: c.Enabled,
		ClientID: c.ClientID, HasClientSecret: len(c.Ciphertext) > 0, Source: "database", RedirectURI: s.RedirectURI}
	if c.Version == 0 {
		out.Source = "unset"
		out.ClientID = s.LegacyClientID
		out.HasClientSecret = s.LegacyClientSecret != ""
		out.Enabled = out.ClientID != "" && out.HasClientSecret
		if out.ClientID != "" || out.HasClientSecret {
			out.Source = "legacy_env"
		}
	}
	return out
}

func (s *OAuthAppService) UpdateGitHub(ctx context.Context, actor string, p OAuthAppPatch) (OAuthAppStatus, error) {
	c, err := s.Store.UpdateGitHubAdmin(ctx, actor, func(c OAuthAppConfig) (OAuthAppConfig, error) {
		return s.applyGitHubPatch(c, p)
	})
	if err != nil {
		return OAuthAppStatus{}, err
	}
	return s.status(c), nil
}

func (s *OAuthAppService) applyGitHubPatch(c OAuthAppConfig, p OAuthAppPatch) (OAuthAppConfig, error) {
	if p.ExpectedVersion == nil || *p.ExpectedVersion < 0 || p.Enabled == nil ||
		p.ClearClientSecret && (p.ClientSecret != nil || *p.Enabled) {
		return c, invalidOAuthApp()
	}
	if c.Version != *p.ExpectedVersion {
		return c, Fail("oauth_app_version_conflict", 409, "OAuth 应用配置已变化，请刷新")
	}
	var secret string
	if c.Version == 0 {
		c.ClientID = s.LegacyClientID
		if !p.ClearClientSecret && p.ClientSecret == nil {
			secret = s.LegacyClientSecret
		}
	}
	if p.ClientID != nil {
		next := strings.TrimSpace(*p.ClientID)
		if next != c.ClientID {
			c.Ciphertext = nil
			secret = ""
			if *p.Enabled && p.ClientSecret == nil {
				return c, Fail("oauth_app_client_secret_required", 400, "更换 GitHub Client ID 时必须填写新应用的 Client Secret")
			}
		}
		c.ClientID = next
	}
	if p.ClientSecret != nil {
		secret = *p.ClientSecret
		if secret == "" || strings.TrimSpace(secret) != secret {
			return c, invalidOAuthApp()
		}
	}
	if len(c.ClientID) > 256 || strings.ContainsAny(c.ClientID, " \t\r\n\x00") ||
		len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") {
		return c, invalidOAuthApp()
	}
	if p.ClearClientSecret {
		c.Ciphertext = nil
	}
	if secret != "" {
		var err error
		c.Ciphertext, err = s.encrypt(secret)
		if err != nil {
			return c, err
		}
	}
	c.Enabled = *p.Enabled
	if c.Enabled && (c.ClientID == "" || len(c.Ciphertext) == 0) {
		return c, invalidOAuthApp()
	}
	if c.Version == 0 && s.LegacyClientID != "" && s.LegacyClientSecret != "" &&
		c.ClientID == s.LegacyClientID && secret == s.LegacyClientSecret && !p.ClearClientSecret {
		c.LegacyGrantPin = gitHubAppPin(s.Secret, "grant", c.ClientID, secret)
	}
	c.Version++
	return c, nil
}

func invalidOAuthApp() error {
	return Fail("invalid_oauth_app_config", 400, "请填写有效的 GitHub OAuth 应用配置")
}
func (s *OAuthAppService) aead() (cipher.AEAD, error) {
	if len(s.Secret) < 32 {
		return nil, errors.New("OAuth app encryption unavailable")
	}
	key := sha256.Sum256(append([]byte(githubOAuthAAD+":"), s.Secret...))
	b, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
func (s *OAuthAppService) encrypt(secret string) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, []byte(secret), []byte(githubOAuthAAD)), nil
}
func (s *OAuthAppService) decrypt(raw []byte) (string, error) {
	a, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(raw) < a.NonceSize() {
		return "", errors.New("invalid OAuth app ciphertext")
	}
	value, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(githubOAuthAAD))
	if err != nil {
		return "", errors.New("OAuth app credential authentication failed")
	}
	return string(value), nil
}
