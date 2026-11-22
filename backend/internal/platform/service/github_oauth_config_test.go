package service

import (
	"context"
	"strings"
	"testing"
)

type oauthAppPluginTx struct {
	PluginTx
	config OAuthAppConfig
}

func (p oauthAppPluginTx) GitHubOAuthApp(context.Context) (OAuthAppConfig, error) {
	return p.config, nil
}

func TestImportedLegacyGitHubGrantSurvivesEnvironmentRemoval(t *testing.T) {
	key := []byte(strings.Repeat("k", 40))
	app := &OAuthAppService{Secret: key, LegacyClientID: "legacy-app", LegacyClientSecret: "legacy-secret"}
	plugins := &PluginService{Secret: key, OAuthApps: app}
	ctx := context.Background()
	current := func(config OAuthAppConfig) githubOAuthMaterial {
		t.Helper()
		m, err := plugins.gitHubOAuthMaterial(ctx, oauthAppPluginTx{config: config})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	before := current(OAuthAppConfig{})
	version := int64(0)
	enabled := true
	db, err := app.applyGitHubPatch(OAuthAppConfig{}, OAuthAppPatch{ExpectedVersion: &version, Enabled: &enabled})
	if err != nil || db.Version != 1 || db.LegacyGrantPin == "" {
		t.Fatal("matching legacy import failed", err)
	}
	app.LegacyClientID, app.LegacyClientSecret = "", ""
	m := current(db)
	if !m.permitsGrant("") || !m.permitsGrant(before.grantPin) || m.attemptPin == before.attemptPin {
		t.Fatal("import should preserve identical legacy grants but invalidate in-flight authorization attempts")
	}
	version = db.Version
	other := "different-secret"
	rotated, err := app.applyGitHubPatch(db, OAuthAppPatch{ExpectedVersion: &version, Enabled: &enabled, ClientSecret: &other})
	if err != nil || rotated.LegacyGrantPin != db.LegacyGrantPin {
		t.Fatal("legacy provenance pin was rewritten", err)
	}
	next := current(rotated)
	if next.permitsGrant("") || next.permitsGrant(before.grantPin) || !next.permitsGrant(next.grantPin) {
		t.Fatal("rotated active app reused grants associated with an older app")
	}
}
