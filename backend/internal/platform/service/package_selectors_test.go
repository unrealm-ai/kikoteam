package service

import "testing"

func TestPackageSelectorsWebAndValidation(t *testing.T) {
	id := NewID()
	p := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "whitelist_with_blacklist", Allow: []UsageSelector{{Channel: "web", Kind: "user", ID: id}}}}
	if !PackageAllowsMember(p, id, true) || PackageAllowsMember(p, id, false) || PackageAllowsMember(p, NewID(), true) {
		t.Fatal("Web selector grant wrong")
	}
	p.Scope.Deny = append(p.Scope.Deny, p.Scope.Allow[0])
	if PackageAllowsMember(p, id, true) {
		t.Fatal("deny did not win")
	}
	for _, ref := range []UsageSelector{{Channel: "web", Kind: "department", ID: id}, {Channel: "web", Kind: "group", ID: id}, {Channel: "wecom", Kind: "department", ID: "dept", BindingID: id, IdentityEpoch: 1}, {Channel: "feishu", Kind: "user", ID: "x"}} {
		s := PackageScope{Allow: []UsageSelector{ref}}
		if normalizePackageSelectors(&s) == nil {
			t.Fatal("invalid ref", ref)
		}
	}
}
