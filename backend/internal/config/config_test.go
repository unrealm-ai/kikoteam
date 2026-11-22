package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyMatrix(t *testing.T) {
	cases := []struct {
		body string
		fail bool
	}{
		{"{}", false},
		{"{\"mail\":{\"enabled\":false,\"smtp\":{\"password\":{\"env\":\"MISSING_SECRET\"},\"address\":\"bad\"}}}", false},
		{"{\"auth\":{\"require_email_verification\":true}}", true},
		{"{\"mail\":{\"enabled\":true}}", true},
		{"{\"auth\":{\"require_email_verification\":null}}", true},
		{"{\"mail\":{\"enabled\":true,\"enabled\":false}}", true},
		{"{\"unknown\":true}", true},
		{"{} {}", true},
		{"{\"mail\":{\"enabled\":true,\"smtp\":{\"address\":\"localhost:587\",\"host\":\"localhost\",\"from\":\"noreply@example.com\"}}}", false},
		{"{\"auth\":{\"require_email_verification\":true},\"mail\":{\"enabled\":true,\"smtp\":{\"address\":\"localhost:465\",\"host\":\"localhost\",\"from\":\"noreply@example.com\",\"implicit_tls\":true}}}", false},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KIKO_CONFIG", path)
			t.Setenv("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "true")
			t.Setenv("KIKO_MAIL_ENABLED", "true")
			_, err := Load()
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v expected failure=%v", err, tc.fail)
			}
		})
	}
}
func TestReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("test-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(Reference{File: "secret"}, dir, "auth.secret")
	if err != nil || got != "test-secret" {
		t.Fatal("relative private file failed", err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = resolve(Reference{File: "secret"}, dir, "auth.secret"); err == nil {
		t.Fatal("public file accepted")
	}
	if _, err = resolve(Reference{Value: "test-secret", File: "secret"}, dir, "auth.secret"); err == nil {
		t.Fatal("conflicting sources accepted")
	}
	if _, err = resolve(Reference{Env: "KIKO_TEST_ABSENT"}, dir, "auth.secret"); err == nil {
		t.Fatal("missing reference accepted")
	}
	t.Setenv("KIKO_TEST_SECRET", "test-secret")
	if got, err = resolve(Reference{Env: "KIKO_TEST_SECRET"}, dir, "auth.secret"); err != nil || got != "test-secret" {
		t.Fatal("environment reference failed")
	}
}
func TestBasicOverrideAndLegacyMail(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "false")
	t.Setenv("KIKO_MAIL_ENABLED", "false")
	t.Setenv("SMTP_ADDRESS", "localhost:587")
	t.Setenv("SMTP_HOST", "localhost")
	t.Setenv("SMTP_FROM", "noreply@example.com")
	c, err := Load()
	if err != nil || c.Mail.Enabled {
		t.Fatal("implicit SMTP enable", err)
	}
	t.Setenv("KIKO_MAIL_ENABLED", "true")
	c, err = Load()
	if err != nil || !c.Mail.Enabled {
		t.Fatal("explicit enable", err)
	}
	path := filepath.Join(t.TempDir(), "c.json")
	if err = os.WriteFile(path, []byte("{\"database\":{\"url\":{\"env\":\"ABSENT\"}}}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIKO_CONFIG", path)
	t.Setenv("DATABASE_URL", "postgres://override")
	c, err = Load()
	if err != nil || c.DSN != "postgres://override" || c.Mail.Enabled {
		t.Fatal("precedence", err)
	}
}

func TestPublicReferenceConfigAndPrivateInline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := "{\"database\":{\"url\":{\"env\":\"DATABASE_URL\"}},\"auth\":{\"secret\":{\"env\":\"KIKO_AUTH_SECRET\"}}}"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIKO_CONFIG", path)
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("KIKO_AUTH_SECRET", "not-a-real-secret-for-tests")
	if _, err := Load(); err != nil {
		t.Fatal("reference file misclassified", err)
	}
	if err := os.WriteFile(path, []byte("{\"auth\":{\"secret\":{\"value\":\"inline-secret\"}}}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("public inline secret accepted")
	}
}
func TestEnabledMissingSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := "{\"mail\":{\"enabled\":true,\"smtp\":{\"address\":\"localhost:587\",\"host\":\"localhost\",\"from\":\"noreply@example.com\",\"username\":\"user\",\"password\":{\"env\":\"KIKO_TEST_ABSENT\"}}}}"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIKO_CONFIG", path)
	if _, err := Load(); err == nil {
		t.Fatal("missing SMTP secret accepted")
	}
}

func TestCaseFoldDuplicatePolicy(t *testing.T) {
	if err := validateJSON([]byte(`{"mail":{"enabled":true,"ENABLED":false}}`)); err == nil {
		t.Fatal("casefold duplicate accepted")
	}
}

// New deployments inject one environment configuration; no JSON is required.
func TestEnvironmentOnlyConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://unit-only")
	t.Setenv("KIKO_AUTH_SECRET", "unit-test-only-not-a-real-secret-32")
	t.Setenv("KIKO_PUBLIC_ORIGIN", "http://localhost:18080")
	t.Setenv("KIKO_HTTP_ADDRESS", "127.0.0.1:18080")
	t.Setenv("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "false")
	t.Setenv("KIKO_MAIL_ENABLED", "false")
	t.Setenv("SMTP_ADDRESS", "invalid-but-disabled")
	c, err := Load()
	if err != nil || c.DSN != "postgres://unit-only" || c.Auth.RequireEmailVerification || c.Mail.Enabled || c.Mail.SMTP.Address != "" {
		t.Fatal("environment-only configuration failed", err)
	}
	t.Setenv("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("verification without mail accepted")
	}
}

func TestMCPPrivateCIDRs(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("KIKO_MAIL_ENABLED", "false")
	t.Setenv("KIKO_REGISTRATION_VERIFICATION_REQUIRED", "false")
	t.Setenv("KIKO_MCP_PRIVATE_CIDRS", " 10.42.0.1/16, , fd00::/8 ")
	c, err := Load()
	if err != nil || len(c.MCPPrivateCIDRs) != 2 || c.MCPPrivateCIDRs[0].String() != "10.42.0.0/16" {
		t.Fatalf("CIDR configuration failed: %v", err)
	}
	t.Setenv("KIKO_MCP_PRIVATE_CIDRS", "10.0.0.1")
	if _, err := Load(); err == nil {
		t.Fatal("non-CIDR accepted")
	}
}
