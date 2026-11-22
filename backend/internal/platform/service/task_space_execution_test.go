package service

import (
	"context"
	"testing"
)

func TestTaskSpaceRuntimeRequiresDatabaseProofAndKeepsPackageScope(t *testing.T) {
	ctx := WithTaskSpaceExecution(context.Background(), TaskSpaceExecution{TaskSpaceID: "space-conversation", InputID: "running-input"})
	if err := authorizeRuntime(ctx, struct{}{}, "actor", "employee", func() error { return nil }); err == nil {
		t.Fatal("context alone authorized space execution")
	}
	if _, err := (&RuntimeService{}).Effective(ctx, "workspace", "actor", "employee", nil); err == nil {
		t.Fatal("unconfigured runtime accepted space")
	}
	p := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{"actor"}}}
	if !PackageAllowsExecution(ctx, p, "actor") {
		t.Fatal("space lost individual package grant")
	}
	if PackageAllowsExecution(ctx, p, "other") {
		t.Fatal("space granted another user's package")
	}
	p.Scope.DenyMemberIDs = []string{"actor"}
	if PackageAllowsExecution(ctx, p, "actor") {
		t.Fatal("space bypassed package deny")
	}
}
