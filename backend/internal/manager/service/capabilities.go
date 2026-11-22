package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// The native extension receives a different purpose-bound credential, never
// the Engine deployment credential or the Sandbox/third-party token.
func capabilitySecret(secret string) string {
	sum := sha256.Sum256([]byte("capability-policy:" + secret))
	return hex.EncodeToString(sum[:])
}
func SignCapability(secret string, v DelegationIdentity) (string, error) {
	return SignDelegation(capabilitySecret(secret), v)
}
func VerifyCapability(secret, token string) (DelegationIdentity, error) {
	if len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "能力执行身份无效")
	}
	return VerifyDelegation(capabilitySecret(secret), token)
}

type CapabilityCall struct {
	SourceRevision int64
	Source, Actor  string
	ID             string
	InputID        string
	NativeID       string
	PluginID       string
	VersionID      *string
	AccountID      *string
	AccountVersion *int64
	Tool           string
	SchemaDigest   *string
	Status         string
	ErrorCode      *string
}
type CapabilityStore interface {
	CapabilityContext(context.Context, DelegationIdentity) (HarnessConfig, string, string, error)
	RecordCapabilityCall(context.Context, DelegationIdentity, CapabilityCall) error
	FinishCapabilityCall(context.Context, DelegationIdentity, string, string) error
}
