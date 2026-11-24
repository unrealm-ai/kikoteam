package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const (
	ChatToolCatalogDelegation = "task_delegation"
	ChatToolCatalogHuman      = "human_confirmation"
)

// A catalog grant only advertises static schemas. It is a separate purpose
// from every callable MCP credential and never authorizes business access.
type ChatToolCatalogGrant struct {
	Identity  DelegationIdentity `json:"identity"`
	InputID   string             `json:"input_id"`
	Family    string             `json:"family"`
	Channel   bool               `json:"channel"`
	ExpiresAt int64              `json:"expires_at"`
}

func (v ChatToolCatalogGrant) valid() bool {
	return (v.Family == ChatToolCatalogDelegation || v.Family == ChatToolCatalogHuman) &&
		ValidID(v.InputID) && ValidID(v.Identity.SessionID) && ValidID(v.Identity.EngineID) &&
		ValidID(v.Identity.AssignmentID) && v.Identity.Generation > 0
}

func SignChatToolCatalog(secret string, grant ChatToolCatalogGrant) (string, error) {
	if len(secret) < 32 || !grant.valid() {
		return "", invalidChatToolCatalog()
	}
	grant.ExpiresAt = time.Now().Add(30 * time.Minute).Unix()
	return encodeChatToolCatalog(secret, grant)
}

func VerifyChatToolCatalog(secret, input, token string) (ChatToolCatalogGrant, error) {
	var grant ChatToolCatalogGrant
	data, _, ok := strings.Cut(token, ".")
	if len(secret) < 32 || !ok || len(token) > 2048 {
		return ChatToolCatalogGrant{}, invalidChatToolCatalog()
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil || json.Unmarshal(raw, &grant) != nil || !grant.valid() ||
		grant.InputID != input || grant.ExpiresAt <= time.Now().Unix() {
		return ChatToolCatalogGrant{}, invalidChatToolCatalog()
	}
	expected, err := encodeChatToolCatalog(secret, grant)
	if err != nil || !hmac.Equal([]byte(expected), []byte(token)) {
		return ChatToolCatalogGrant{}, invalidChatToolCatalog()
	}
	return grant, nil
}

func invalidChatToolCatalog() error {
	return Fail("unauthenticated", 401, "对话工具目录身份无效")
}

func encodeChatToolCatalog(secret string, grant ChatToolCatalogGrant) (string, error) {
	data, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err = mac.Write(append([]byte("chat-tool-catalog:"), data...)); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
