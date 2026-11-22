package service

import (
	"context"
	"testing"
)

func TestWebGroupRuntimeRequiresDatabaseProofAndKeepsPackageScope(t *testing.T) {
	ctx := WithWebGroupExecution(context.Background(), WebGroupExecution{ConversationID: "group-conversation", InputID: "running-input"})
	if err := authorizeRuntime(ctx, struct{}{}, "actor", "employee", func() error { return nil }); err == nil {
		t.Fatal("context alone authorized group execution")
	}
	if _, err := (&RuntimeService{}).Effective(ctx, "workspace", "actor", "employee", nil); err == nil {
		t.Fatal("unconfigured runtime accepted group")
	}
	p := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{"actor"}}}
	if !PackageAllowsExecution(ctx, p, "actor") {
		t.Fatal("group lost individual package grant")
	}
	if PackageAllowsExecution(ctx, p, "other") {
		t.Fatal("group granted another user's package")
	}
	p.Scope.DenyMemberIDs = []string{"actor"}
	if PackageAllowsExecution(ctx, p, "actor") {
		t.Fatal("group bypassed package deny")
	}
}
