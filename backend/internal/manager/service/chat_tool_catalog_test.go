package service

import (
	"strings"
	"testing"
	"time"
)

func TestChatToolCatalogIsNotAnExecutionCredential(t *testing.T) {
	secret := strings.Repeat("unit", 12)
	grant := ChatToolCatalogGrant{
		Identity: DelegationIdentity{SessionID: NewID(), EngineID: NewID(), AssignmentID: NewID(), Generation: 1},
		InputID:  NewID(), Family: ChatToolCatalogHuman, Channel: true,
	}
	token, err := SignChatToolCatalog(secret, grant)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := VerifyChatToolCatalog(secret, grant.InputID, token)
	if err != nil || actual.Identity != grant.Identity || actual.Family != ChatToolCatalogHuman || !actual.Channel {
		t.Fatal("could not verify schema-only identity", err)
	}
	for _, invalid := range []string{token + "x", "", token[:len(token)-4]} {
		if _, err := VerifyChatToolCatalog(secret, grant.InputID, invalid); err == nil {
			t.Fatal("accepted invalid catalog credential")
		}
	}
	if _, err := VerifyChatToolCatalog(secret, NewID(), token); err == nil {
		t.Fatal("accepted a catalog credential from a different input")
	}
	if _, err := VerifyDelegation(secret, token); err == nil {
		t.Fatal("catalog credential is callable in task delegation")
	}
	if _, err := VerifyHumanMCP(secret, "chat", grant.InputID, token); err == nil {
		t.Fatal("catalog credential is callable in human confirmation")
	}
	grant.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	expired, err := encodeChatToolCatalog(secret, grant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChatToolCatalog(secret, grant.InputID, expired); err == nil {
		t.Fatal("accepted expired catalog credential")
	}
}
