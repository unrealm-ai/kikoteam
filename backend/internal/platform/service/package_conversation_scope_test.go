package service

import (
	"context"
	"testing"
)

func TestPackageConversationScopeValidation(t *testing.T) {
	user := UsageSelector{Channel: "web", Kind: "user", ID: NewID()}
	group := UsageSelector{Channel: "web", Kind: "group", ID: NewID()}
	for _, tc := range []struct {
		name  string
		r     PackageConversationRule
		group bool
		ok    bool
	}{
		{"inherit", PackageConversationRule{Enabled: true, Mode: "inherit_with_blacklist", Deny: []UsageSelector{user}}, false, true},
		{"allow", PackageConversationRule{Enabled: true, Mode: "allowlist", Allow: []UsageSelector{user}}, false, true},
		{"mixed", PackageConversationRule{Mode: "allowlist", Deny: []UsageSelector{user}}, false, false},
		{"inherit allow", PackageConversationRule{Mode: "inherit_with_blacklist", Allow: []UsageSelector{user}}, false, false},
		{"private group", PackageConversationRule{Mode: "allowlist", Allow: []UsageSelector{group}}, false, false},
		{"group", PackageConversationRule{Mode: "allowlist", Allow: []UsageSelector{group, user}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &PackageConversationScope{Private: PackageConversationRule{Mode: "allowlist"}, Group: PackageConversationRule{Mode: "allowlist"}}
			if tc.group {
				s.Group = tc.r
			} else {
				s.Private = tc.r
			}
			p := CapabilityPackage{ConversationScope: s}
			if e := normalizePackageConversationScope(&p); (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
}
func TestPackageSceneDoesNotChangeLegacyOrTrigger(t *testing.T) {
	id := NewID()
	p := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "inherit_with_blacklist"}}
	if !PackageAllowsExecution(context.Background(), p, id) {
		t.Fatal("legacy changed")
	}
	p.ConversationScope = &PackageConversationScope{}
	if PackageAllowsExecution(context.Background(), p, id) {
		t.Fatal("unverified scene granted")
	}
	p.TriggerScope = DefaultPackageTriggerScope()
	if !PackageAllowsTrigger(p, id) {
		t.Fatal("conversation scope blocked trigger")
	}
	off := false
	p.TriggerScope.Enabled = &off
	if PackageAllowsTrigger(p, id) {
		t.Fatal("disabled trigger allowed")
	}
	p.TriggerScope.Mode = ""
	if e := normalizePackageTriggerScope(&p); e != nil || p.TriggerScope.Enabled == nil || *p.TriggerScope.Enabled {
		t.Fatal("disabled trigger lost", e)
	}
}

type sceneReferenceTx struct {
	PackageTx
	checks int
}

func (t *sceneReferenceTx) ValidatePackageSelector(context.Context, string, UsageSelector) error {
	t.checks++
	return Fail("invalid_package_selector", 409, "stale")
}
func TestPackageConversationMovedStaleReferenceRevalidates(t *testing.T) {
	ref := UsageSelector{Channel: "feishu", Kind: "user", ID: "stale", BindingID: NewID(), IdentityEpoch: 1}
	old := &PackageConversationScope{Private: PackageConversationRule{Deny: []UsageSelector{ref}}}
	same := *old
	p := CapabilityPackage{AgentID: NewID(), ConversationScope: &same}
	tx := &sceneReferenceTx{}
	if e := validateConversationReferences(context.Background(), tx, p, old); e != nil || tx.checks != 0 {
		t.Fatal("unchanged reference unexpectedly validated", e)
	}
	moved := PackageConversationScope{Private: PackageConversationRule{Allow: []UsageSelector{ref}}}
	p.ConversationScope = &moved
	if e := validateConversationReferences(context.Background(), tx, p, old); e == nil || tx.checks != 1 {
		t.Fatal("deny to allow skipped validation", e)
	}
	moved = PackageConversationScope{Group: PackageConversationRule{Deny: []UsageSelector{ref}}}
	p.ConversationScope = &moved
	if e := validateConversationReferences(context.Background(), tx, p, old); e == nil || tx.checks != 2 {
		t.Fatal("scene move skipped validation", e)
	}
}
