package service

import "testing"

func TestPluginDefinitionValidation(t *testing.T) {
	for _, h := range []string{"Host", "Authorization", "Cookie", "X-Api-Key\r\nX-Bad", "Mcp-Session-Id"} {
		p := Plugin{Name: "test", Endpoint: "https://example.com/mcp", AuthType: "api_key", HeaderName: h}
		if ValidatePlugin(&p) == nil {
			t.Fatal(h)
		}
	}
	p := Plugin{Name: "test", Endpoint: "https://example.com/mcp", AuthType: "api_key", HeaderName: "X-Api-Key"}
	if e := ValidatePlugin(&p); e != nil {
		t.Fatal(e)
	}
	p.Endpoint = "http://example.com"
	if ValidatePlugin(&p) == nil {
		t.Fatal("http accepted")
	}
}
func TestPluginCredentialsBoundToOwnerAndPurpose(t *testing.T) {
	s := &PluginService{Secret: []byte("01234567890123456789012345678901")}
	p := PluginAccount{ID: "account", PluginID: "plugin"}
	secret := "credential"
	b, e := s.encryptAccount("ws", p, secret)
	if e != nil {
		t.Fatal(e)
	}
	p.Credential = b
	got, e := s.accountSecret("ws", p, nil)
	if e != nil || got != secret {
		t.Fatal(e)
	}
	if _, e = s.accountSecret("other", p, nil); e == nil {
		t.Fatal("cross-workspace decrypt")
	}
	p.PluginID = "other"
	if _, e = s.accountSecret("ws", p, nil); e == nil {
		t.Fatal("cross-plugin decrypt")
	}
}
