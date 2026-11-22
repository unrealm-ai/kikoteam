package service

import (
	"strings"
	"testing"
)

func TestGroupHistoryPurposeBoundCredential(t *testing.T) {
	secret := strings.Repeat("x", 40)
	id := DelegationIdentity{SessionID: NewID(), EngineID: NewID(), AssignmentID: NewID(), Generation: 1}
	token, e := SignGroupHistory(secret, id)
	if e != nil {
		t.Fatal(e)
	}
	got, e := VerifyGroupHistory(secret, token)
	if e != nil || got != id {
		t.Fatal(got, e)
	}
	if _, e = VerifyDelegation(secret, token); e == nil {
		t.Fatal("history token became delegation token")
	}
	if _, e = VerifyCapability(secret, token); e == nil {
		t.Fatal("history token became capability token")
	}
	other, e := SignDelegation(secret, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyGroupHistory(secret, other); e == nil {
		t.Fatal("delegation token accepted")
	}
	if _, e = VerifyGroupHistory("short", token); e == nil {
		t.Fatal("short secret accepted")
	}
	if _, e = VerifyGroupHistory(secret, token+"x"); e == nil {
		t.Fatal("tampered token accepted")
	}
}
