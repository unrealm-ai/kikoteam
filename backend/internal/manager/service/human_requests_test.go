package service

import (
	"strings"
	"testing"
)

func TestHumanToolCredentialBindsExactInput(t *testing.T) {
	secret, input := strings.Repeat("x", 40), NewID()
	identity := DelegationIdentity{SessionID: NewID(), EngineID: NewID(), AssignmentID: NewID(), Generation: 2}
	token, err := SignHumanRuntime(secret, input, identity)
	if err != nil {
		t.Fatal(err)
	}
	if scope, err := VerifyHumanRuntime(secret, input, token); err != nil || scope != identity {
		t.Fatalf("scope = %v, error = %v", scope, err)
	}
	if _, err := VerifyHumanRuntime(secret, NewID(), token); err == nil {
		t.Fatal("credential accepted for a different input")
	}
	if _, err := VerifyDelegation(secret, token); err == nil {
		t.Fatal("human-tool credential accepted as a delegation credential")
	}
}
