package service

import (
	"context"
	"testing"
)

func TestChannelPackageScopeNeverUsesWebMemberIDs(t *testing.T) {
	ctx := WithChannelExecution(context.Background(), ChannelExecution{PrincipalID: "channel"})
	for _, tc := range []struct {
		name    string
		scope   PackageScope
		allowed bool
	}{
		{"inherit", PackageScope{Mode: "inherit_with_blacklist"}, true},
		{"Web whitelist", PackageScope{Mode: "whitelist_with_blacklist", AllowMemberIDs: []string{"channel"}}, false},
		{"Web blacklist", PackageScope{Mode: "inherit_with_blacklist", DenyMemberIDs: []string{"different-Web-member"}}, false},
		{"mixed scope", PackageScope{Mode: "inherit_with_blacklist", AllowMemberIDs: []string{"owner"}}, false},
		{"unknown", PackageScope{Mode: "feishu"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := CapabilityPackage{Enabled: true, Scope: tc.scope}
			if got := PackageAllowsExecution(ctx, p, "channel"); got != tc.allowed {
				t.Fatalf("allowed=%v", got)
			}
			p.Enabled = false
			if PackageAllowsExecution(ctx, p, "channel") {
				t.Fatal("disabled package allowed")
			}
		})
	}
}
func TestChannelExecutionRequiresStoreVerification(t *testing.T) {
	ctx := WithChannelExecution(context.Background(), ChannelExecution{PrincipalID: "channel", Generation: 1, ConversationID: "conversation", InputID: "input"})
	webCalled := false
	fallback := func() error { webCalled = true; return nil }
	if err := authorizeRuntime(ctx, struct{}{}, "channel", "employee", fallback); err == nil {
		t.Fatal("context authorized without store proof")
	}
	if err := authorizeRuntime(ctx, struct{}{}, "owner", "employee", fallback); err == nil {
		t.Fatal("owner could stand in for channel principal")
	}
	if webCalled {
		t.Fatal("channel fell back to Web owner authorization")
	}
	if err := authorizeRuntime(context.Background(), struct{}{}, "member", "", fallback); err != nil || !webCalled {
		t.Fatal("Web path changed", err)
	}
}

func TestChannelExecutionWithoutRuntimeStoreFailsClosed(t *testing.T) {
	ctx := WithChannelExecution(context.Background(), ChannelExecution{PrincipalID: "channel"})
	if _, err := (&RuntimeService{}).Effective(ctx, "workspace", "channel", "employee", nil); err == nil {
		t.Fatal("unconfigured runtime accepted channel")
	}
}
