package service

import "testing"

func TestDepartmentRulesCompatibility(t *testing.T) {
	a := ChannelAccess{AllowDepartmentIDs: []string{"old"}}
	if err := NormalizeDepartmentRules(&a); err != nil || len(a.AllowDepartmentRules) != 1 || a.AllowDepartmentRules[0].IncludeDescendants {
		t.Fatal(a, err)
	}
	a.AllowDepartmentRules = []ChannelDepartmentRule{}
	if err := NormalizeDepartmentRules(&a); err != nil || len(a.AllowDepartmentIDs) != 0 {
		t.Fatal("explicit empty rules must clear projection", a, err)
	}
	a.AllowDepartmentRules = []ChannelDepartmentRule{{DepartmentID: "x"}, {DepartmentID: "x", IncludeDescendants: true}}
	if err := NormalizeDepartmentRules(&a); err == nil {
		t.Fatal("duplicate rule ambiguity")
	}
}

func TestChannelCiphertextIsProviderScoped(t *testing.T) {
	s := ChannelService{Secret: []byte("test-provider-credential-encryption-secret")}
	b := ChannelBinding{ID: "id", WorkspaceID: "workspace", AgentID: "agent", Provider: "dingtalk"}
	cipher, err := s.seal(b, "secret")
	if err != nil {
		t.Fatal(err)
	}
	b.Ciphertext = cipher
	if plain, err := s.open(b); err != nil || plain != "secret" {
		t.Fatal(plain, err)
	}
	b.Provider = "wecom"
	if _, err := s.open(b); err == nil {
		t.Fatal("cross provider credential decrypted")
	}
	b.Provider = "feishu"
	if _, err := s.open(b); err == nil {
		t.Fatal("cross provider credential decrypted")
	}
}
