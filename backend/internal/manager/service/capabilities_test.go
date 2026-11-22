package service

import (
	"strings"
	"testing"
)

func TestCapabilityCredentialCannotAuthorizeOtherCredentialPurposes(t *testing.T) {
	secret := strings.Repeat("s", 40)
	identity := DelegationIdentity{SessionID: NewID(), EngineID: NewID(), AssignmentID: NewID(), Generation: 1}
	token, err := SignCapability(secret, identity)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyCapability(secret, token)
	if err != nil || verified != identity {
		t.Fatal("scoped policy credential rejected", err)
	}
	if _, err = VerifyDelegation(secret, token); err == nil {
		t.Fatal("task policy credential became chat delegation authority")
	}
	delegation, err := SignDelegation(secret, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{secret, delegation, token + "modified"} {
		if _, err = VerifyCapability(secret, invalid); err == nil {
			t.Fatal("wrong-purpose or modified credential accepted")
		}
	}
}
