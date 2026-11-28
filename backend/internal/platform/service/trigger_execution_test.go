package service

import (
	"context"
	"testing"
)

type triggerProof struct {
	called bool
	err    error
}

func (p *triggerProof) AuthorizeTriggerExecution(context.Context, TriggerExecution, string, string, bool) error {
	p.called = true
	return p.err
}
func TestTriggerRuntimeRequiresIndependentProof(t *testing.T) {
	g := TriggerExecution{TriggerID: NewID(), Version: 1, EventID: NewID(), InputID: NewID()}
	ctx := WithTriggerExecution(context.Background(), g)
	if authorizeRuntime(ctx, struct{}{}, "", "employee", func() error { return nil }) == nil {
		t.Fatal("context granted access")
	}
	p := &triggerProof{}
	if err := authorizeRuntime(ctx, p, "", "", func() error { t.Fatal("trigger borrowed human identity"); return nil }); err != nil || !p.called {
		t.Fatal(err)
	}
	for _, bad := range []context.Context{WithTaskSpaceExecution(ctx, TaskSpaceExecution{}), WithChannelExecution(ctx, ChannelExecution{}), WithWebGroupExecution(ctx, WebGroupExecution{})} {
		if authorizeRuntime(bad, p, "", "", func() error { return nil }) == nil {
			t.Fatal("mixed provenance accepted")
		}
	}
	if authorizeRuntime(ctx, p, "human", "", func() error { return nil }) == nil {
		t.Fatal("human spoof accepted")
	}
	if _, err := (&RuntimeService{}).Effective(ctx, "w", "", "employee", nil); err == nil {
		t.Fatal("empty snapshot skipped proof")
	}
}
func TestPackageTriggerScopeIndependentAndExclusive(t *testing.T) {
	a, b := NewID(), NewID()
	p := CapabilityPackage{AgentID: NewID(), Enabled: true, Scope: PackageScope{Mode: "whitelist_with_blacklist"}, TriggerScope: DefaultPackageTriggerScope()}
	ctx := WithTriggerExecution(context.Background(), TriggerExecution{TriggerID: a})
	if !PackageAllowsExecution(ctx, p, "") {
		t.Fatal("default trigger blocked by human whitelist")
	}
	p.TriggerScope = PackageTriggerScope{Mode: "allowlist", AllowTriggerIDs: []string{a}, DenyTriggerIDs: []string{a}}
	if normalizePackageTriggerScope(&p) == nil || PackageAllowsTrigger(p, a) || PackageAllowsTrigger(p, b) {
		t.Fatal("mixed allowlist and denylist must be rejected")
	}
	p.TriggerScope.DenyTriggerIDs = nil
	if !PackageAllowsTrigger(p, a) || PackageAllowsTrigger(p, b) {
		t.Fatal("trigger selection wrong")
	}
	p.TriggerScope = PackageTriggerScope{Mode: "all", DenyTriggerIDs: []string{a}}
	if normalizePackageTriggerScope(&p) != nil || PackageAllowsTrigger(p, a) || !PackageAllowsTrigger(p, b) {
		t.Fatal("all-except-deny scope evaluated incorrectly")
	}
	p.Enabled = false
	if PackageAllowsTrigger(p, b) {
		t.Fatal("disabled package admitted")
	}
	p.Enabled = true
	if PackageAllowsExecution(ctx, p, "human") {
		t.Fatal("trigger borrowed human")
	}
	p.TriggerScope = PackageTriggerScope{AllowTriggerIDs: []string{a}}
	if normalizePackageTriggerScope(&p) == nil || PackageAllowsTrigger(p, a) {
		t.Fatal("malformed scope expanded")
	}
}
