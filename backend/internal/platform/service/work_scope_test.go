package service

import (
	"context"
	"testing"
)

func TestWorkPackageScopeUsesDirectSender(t *testing.T) {
	sender, other := NewID(), NewID()
	ctx := WithWorkExecution(context.Background(), WorkExecution{AssignmentID: NewID(), SourceAgentID: sender})
	pack := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "allowlist"}}
	if PackageAllowsExecution(ctx, pack, "") {
		t.Fatal("legacy restricted package became available")
	}
	for _, tc := range []struct {
		scope WorkScope
		want  bool
	}{
		{WorkScope{Mode: "inherit"}, true},
		{WorkScope{Mode: "allowlist", EmployeeIDs: []string{other}}, false},
		{WorkScope{Mode: "allowlist", EmployeeIDs: []string{sender}}, true},
		{WorkScope{Mode: "denylist", EmployeeIDs: []string{sender}}, false},
	} {
		pack.WorkScope = &tc.scope
		if err := tc.scope.Validate("inherit"); err != nil {
			t.Fatal(err)
		}
		if PackageAllowsExecution(ctx, pack, "") != tc.want || PackageAllowsExecution(ctx, pack, sender) {
			t.Fatal("unexpected package access", tc.scope.Mode)
		}
	}
}
