package service

import "testing"

func TestPackageScopeOnlyNarrowsEmployeeAccess(t *testing.T) {
	p := CapabilityPackage{Enabled: true, Scope: PackageScope{Mode: "inherit_with_blacklist", DenyMemberIDs: []string{"denied"}}}
	if PackageAllowsMember(p, "allowed", false) || PackageAllowsMember(p, "denied", true) || !PackageAllowsMember(p, "allowed", true) {
		t.Fatal("inherit scope widened or bypassed denial")
	}
	p.Scope.Mode = "whitelist_with_blacklist"
	p.Scope.AllowMemberIDs = []string{"allowed", "denied"}
	if PackageAllowsMember(p, "other", true) || PackageAllowsMember(p, "denied", true) || !PackageAllowsMember(p, "allowed", true) {
		t.Fatal("whitelist/deny precedence")
	}
	p.Enabled = false
	if PackageAllowsMember(p, "allowed", true) {
		t.Fatal("disabled package admitted")
	}
}
func TestPackageValidationRequiresConcreteUniquePins(t *testing.T) {
	valid := func() CapabilityPackage {
		return CapabilityPackage{Name: "Test", Scope: PackageScope{Mode: "inherit_with_blacklist"}, Skills: []PackageSkill{{SkillID: NewID(), VersionID: NewID()}}}
	}
	p := valid()
	if err := validatePackage(&p); err != nil {
		t.Fatal(err)
	}
	p.Skills[0].VersionID = "latest"
	if validatePackage(&p) == nil {
		t.Fatal("symbolic latest accepted")
	}
	p = valid()
	p.Skills = append(p.Skills, p.Skills[0])
	if validatePackage(&p) == nil {
		t.Fatal("duplicate skill accepted")
	}
	p = valid()
	p.Scope.Mode = "slack"
	if validatePackage(&p) == nil {
		t.Fatal("unsupported channel accepted")
	}
}
