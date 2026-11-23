package service

import "testing"

func TestFeishuRegistrationProfileSnapshotAndRealScopeStates(t *testing.T) {
	profile := FeishuProfile()
	if profile.Version != "conversation-business-v4" || profile.ContactRange != "all" || len(profile.Groups) != 11 {
		t.Fatal(profile)
	}
	seen := map[string]bool{}
	for _, p := range FeishuPermissions() {
		if seen[p.Scope] {
			t.Fatal("duplicate", p.Scope)
		}
		seen[p.Scope] = true
	}
	for _, scope := range []string{FeishuContactRangeScope, FeishuReceiptReactionScope, "approval:task", "minutes:minutes.transcript:export", "contact:user.employee_id:readonly"} {
		if !seen[scope] {
			t.Fatal(scope)
		}
	}
	legacy := profilePermissions(nil)
	for _, p := range legacy {
		if p.Scope == FeishuContactRangeScope {
			t.Fatal("old registration gained contact range consent")
		}
	}
	d := FeishuDiagnostics{CredentialStatus: "pass", BotStatus: "pass", DirectoryStatus: "pass", ScopeStatus: "pass", Permissions: FeishuPermissions()}
	for i := range d.Permissions {
		if d.Permissions[i].Required {
			d.Permissions[i].Status = "granted"
		}
	}
	d = registrationDiagnostics(profile, d)
	if !d.Ready {
		t.Fatal("optional scopes blocked core setup")
	}
	groups := permissionGroups(profile, &d)
	if groups[0].Status != "granted" || groups[1].Status != "unknown" || groups[2].ID != "receipt-reaction" || groups[2].Required || groups[2].Status != "unknown" {
		t.Fatal(groups)
	}
	d.Permissions[0].Status = "missing"
	if registrationDiagnostics(profile, d).Ready {
		t.Fatal("required scope silently accepted")
	}
}
