package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestUsageSimplifiedReplacementGuard(t *testing.T) {
	person := service.UsageSelector{Channel: "web", Kind: "user", ID: service.NewID()}
	group := service.UsageSelector{Channel: "web", Kind: "group", ID: service.NewID()}
	normal := service.DefaultAgentUsage(person.ID).Access
	for _, legacy := range []service.UsageAccess{
		{Mode: "all", Allow: []service.UsageSelector{person}, Deny: []service.UsageSelector{}},
		{Mode: "allowlist", Allow: []service.UsageSelector{person}, Deny: []service.UsageSelector{person}},
		{Mode: "allowlist", Allow: []service.UsageSelector{group}, Deny: []service.UsageSelector{}},
	} {
		if service.SimplifiedUsageAccess(legacy) {
			t.Fatal("legacy marked simple")
		}
		if err := validateUsageAccessReplacement(legacy, legacy, false); err != nil {
			t.Fatal(err)
		}
		if err := validateUsageAccessReplacement(legacy, normal, false); err == nil {
			t.Fatal("silent legacy removal")
		}
		if err := validateUsageAccessReplacement(legacy, normal, true); err != nil {
			t.Fatal(err)
		}
		if err := validateUsageAccessReplacement(normal, legacy, false); err == nil {
			t.Fatal("new legacy accepted")
		}
		if err := validateUsageAccessReplacement(legacy, legacy, true); err == nil {
			t.Fatal("confirmation accepted invalid target")
		}
	}
	legacy := service.UsageAccess{Mode: "all", Allow: []service.UsageSelector{person, group}, Deny: []service.UsageSelector{}}
	reordered := legacy
	reordered.Allow = []service.UsageSelector{group, person}
	if err := validateUsageAccessReplacement(legacy, reordered, false); err != nil {
		t.Fatal("reorder requires review", err)
	}
	if err := validateUsageAccessReplacement(normal, normal, true); err == nil {
		t.Fatal("unnecessary acknowledgement")
	}
	all := service.UsageAccess{Mode: "all", Allow: []service.UsageSelector{}, Deny: []service.UsageSelector{person}, GroupMembersEnabled: true}
	if err := validateUsageAccessReplacement(normal, all, false); err != nil {
		t.Fatal(err)
	}
}

func TestAgentUsageLegacyReviewTransaction(t *testing.T) {
	db, models, w, owner := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, w, owner)
	agents := &service.AgentService{Store: NewAgentStore(db)}
	a, err := agents.Create(ctx, w, owner, service.AgentCreate{Name: "Legacy review", ModelProfileID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	api := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	v, err := api.Get(ctx, w, owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed historical mixed rules, rather than constructing them through new APIs.
	v.UsageConfig.Access = service.DefaultAgentUsage(owner).Access
	v.UsageConfig.Access.Deny = []service.UsageSelector{{Channel: "web", Kind: "user", ID: owner}}
	v.UsageConfig.Access.GroupMembersEnabled = false
	raw, err := json.Marshal(v.UsageConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE platform_agents SET usage_config=$2 WHERE id=$1`, a.ID, raw); err != nil {
		t.Fatal(err)
	}
	v.UsageConfig.History.Limit = 25
	v, err = api.Update(ctx, w, owner, a.ID, v)
	if err != nil {
		t.Fatal("unrelated edit", err)
	}
	if v.UsageConfig.Access.GroupMembersEnabled {
		t.Fatal("reset explicit false")
	}
	v.UsageConfig.Access.Deny = []service.UsageSelector{}
	if _, err = api.Update(ctx, w, owner, a.ID, v); err == nil {
		t.Fatal("silently dropped deny")
	}
	got, err := api.Get(ctx, w, owner, a.ID)
	if err != nil || got.Version != v.Version || len(got.UsageConfig.Access.Deny) != 1 {
		t.Fatal(got, err)
	}
	v.ReplaceLegacyAccess = true
	if _, err = api.Update(ctx, w, service.NewID(), a.ID, v); err == nil {
		t.Fatal("nonadmin migration")
	}
	got, err = api.Update(ctx, w, owner, a.ID, v)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReplaceLegacyAccess || got.Version != v.Version+1 || len(got.UsageConfig.Access.Deny) != 0 || got.UsageConfig.Access.GroupMembersEnabled {
		t.Fatal(got)
	}
	if _, err = api.Update(ctx, w, owner, a.ID, v); err == nil {
		t.Fatal("stale migration replay")
	}
}
