package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestFirstFeishuBindingDefaultsHistoryAndPreservesExistingAccess(t *testing.T) {
	db, models, workspace, actor := modelFixture(t)
	ctx := context.Background()
	model := saveTestModel(t, models, workspace, actor)
	agents := &service.AgentService{Store: NewAgentStore(db)}
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	channels := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("t", 32))}
	secret := "synthetic-first-binding-secret"
	for _, mode := range []string{"all", "allowlist"} {
		t.Run(mode, func(t *testing.T) {
			agent, err := agents.Create(ctx, workspace, actor, service.AgentCreate{Name: "First channel " + mode, ModelProfileID: model.ID})
			if err != nil {
				t.Fatal(err)
			}
			before, err := usage.Get(ctx, workspace, actor, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "allowlist" {
				before.UsageConfig.Access = service.DefaultAgentUsage(actor).Access
				before, err = usage.Update(ctx, workspace, actor, agent.ID, before)
				if err != nil {
					t.Fatal(err)
				}
			}
			binding, err := channels.Put(ctx, workspace, actor, agent.ID, service.ChannelPut{AppID: "cli_first_" + mode, AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}})
			if err != nil || binding.Access.Mode != mode || len(binding.Access.AllowIDs) != 0 || len(binding.Access.DenyIDs) != 0 {
				t.Fatal(binding.Access, err)
			}
			after, err := usage.Get(ctx, workspace, actor, agent.ID)
			before.Version++
			before.UsageConfig.History = service.UsageHistory{Enabled: true, Limit: 20, Channels: []string{"feishu"}}
			if err != nil || !binding.History.Enabled || !reflect.DeepEqual(before, after) {
				t.Fatal("unexpected change to employee usage configuration", before, after, err)
			}
		})
	}
}

func TestEmployeeUsageProjectsChannelsAndRejectsOldPolicyWrites(t *testing.T) {
	db, channels, store, b, actor, cookie := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	v, err := usage.Get(context.Background(), b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	v.UsageConfig = service.DefaultAgentUsage(actor)
	v.UsageConfig.Access.Allow = append(v.UsageConfig.Access.Allow, service.UsageSelector{Channel: "feishu", Kind: "department", ID: "od_team", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch, IncludeDescendants: true})
	v.UsageConfig.Access.Deny = []service.UsageSelector{{Channel: "feishu", Kind: "user", ID: "ou_b", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}}
	// Combined allow/deny is historical only. New writes must reject it, while
	// retained historical restrictions must still project without losing deny.
	if _, err = usage.Update(context.Background(), b.WorkspaceID, actor, b.AgentID, v); err == nil {
		t.Fatal("new mixed policy accepted")
	}
	legacyUsage, err := json.Marshal(v.UsageConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE platform_agents SET usage_config=$2 WHERE id=$1`, b.AgentID, legacyUsage); err != nil {
		t.Fatal(err)
	}
	v.UsageConfig.Trigger.Keywords = []service.UsagePattern{{Text: "help", Channels: []string{"web", "feishu"}}}
	v.UsageConfig.History = service.UsageHistory{Enabled: true, Limit: 30, Channels: []string{"feishu", "web"}}
	if _, err = usage.Update(context.Background(), b.WorkspaceID, actor, b.AgentID, v); err != nil {
		t.Fatal(err)
	}
	projected, err := channels.GetProvider(context.Background(), b.WorkspaceID, actor, b.AgentID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if projected.Revision <= b.Revision || projected.RulesVersion <= b.RulesVersion || len(projected.Access.AllowDepartmentRules) != 1 || !projected.Access.AllowDepartmentRules[0].IncludeDescendants || len(projected.Access.DenyIDs) != 1 || !projected.History.Enabled || projected.History.Limit != 30 || len(projected.Trigger.Keywords) != 1 {
		t.Fatal(projected)
	}
	var raw []byte
	if err = db.QueryRow(`SELECT platform_agent_usage_projection(usage_config,'dingtalk',$2,$3) FROM platform_agents WHERE id=$1`, b.AgentID, b.ID, b.IdentityEpoch).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "od_team") || strings.Contains(string(raw), "help") {
		t.Fatal("cross channel selector or keyword leaked", string(raw))
	}
	if err = db.QueryRow(`SELECT platform_agent_usage_projection(usage_config,'feishu',$2,$3) FROM platform_agents WHERE id=$1`, b.AgentID, b.ID, b.IdentityEpoch+1).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "od_team") || strings.Contains(string(raw), "ou_b") {
		t.Fatal("replacement application inherited selectors", string(raw))
	}
	mux := (&controller.Server{Channels: channels, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	path := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/channels/feishu"
	send := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("X-CSRF-Protection", "1")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	legacy, err := json.Marshal(service.ChannelPut{Version: projected.Revision, AppID: b.AppID, Enabled: true, History: b.History, Access: service.ChannelAccess{Mode: "all"}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := send(string(legacy)); rec.Code != 400 {
		t.Fatal("old policy endpoint still writable", rec.Code, rec.Body.String())
	}
	body, err := json.Marshal(map[string]any{"version": projected.Revision, "app_id": b.AppID, "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	if rec := send(string(body)); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestChannelRebindImpactAndExpiredReferences(t *testing.T) {
	db, channels, store, binding, actor, cookie := directoryFixture(t)
	publishDirectoryFixture(t, store, binding)
	ctx := context.Background()
	usage := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	config, err := usage.Get(ctx, binding.WorkspaceID, actor, binding.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	selector := service.UsageSelector{Channel: "feishu", Kind: "user", ID: "ou_a", BindingID: binding.ID, IdentityEpoch: binding.IdentityEpoch}
	config.UsageConfig.Access = service.UsageAccess{Mode: "all", Allow: []service.UsageSelector{}, Deny: []service.UsageSelector{selector}, GroupMembersEnabled: true}
	if _, err := usage.Update(ctx, binding.WorkspaceID, actor, binding.AgentID, config); err != nil {
		t.Fatal(err)
	}
	packageService := &service.PackageService{Store: NewPackageStore(db)}
	_, err = packageService.Create(ctx, binding.WorkspaceID, actor, binding.AgentID, service.PackageCreate{Name: "Reporting", ConversationScope: &service.PackageConversationScope{
		Private: service.PackageConversationRule{Enabled: true, Mode: "inherit_with_blacklist", Deny: []service.UsageSelector{selector}},
		Group:   service.PackageConversationRule{Mode: "inherit_with_blacklist"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := channels.Get(ctx, binding.WorkspaceID, actor, binding.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	cache := &ChannelGroupCache{DB: db}
	if _, err := cache.PublishGroups(ctx, current, actor, service.ChannelGroupPage{Items: []service.ChannelGroupItem{{ID: "oc_a", Name: "A"}, {ID: "oc_b", Name: "B"}}}); err != nil {
		t.Fatal(err)
	}
	groups := &service.GroupSelectorService{Store: NewGroupSelectorStore(db), Secret: []byte(strings.Repeat("g", 32))}
	page, err := groups.Search(ctx, binding.WorkspaceID, actor, binding.AgentID, service.GroupSelectionQuery{Limit: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	sharing := &service.GroupSharingService{Store: &GroupSharingStore{DB: db}}
	if _, err := sharing.Save(ctx, binding.WorkspaceID, actor, binding.AgentID, "", service.GroupSharingPut{Name: "Project", Mode: "mutual", Enabled: true, Sources: []service.SelectionRef{page.Items[0].Ref, page.Items[1].Ref}, Targets: []service.SelectionRef{}}); err != nil {
		t.Fatal(err)
	}
	server := (&controller.Server{Channels: channels, Auth: &service.AuthService{Store: &AuthStore{DB: db}}}).Handler()
	path := "/api/v1/workspaces/" + binding.WorkspaceID + "/agents/" + binding.AgentID + "/channels/feishu?include_rebind_impact=true"
	preview := func() service.ChannelBinding {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Cookie", cookie)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, r)
		var result service.ChannelBinding
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.RebindImpact == nil {
			t.Fatalf("preview: %d %s", response.Code, response.Body.String())
		}
		return result
	}
	before := preview()
	if before.RebindImpact.DenyEntries != 1 || strings.Join(before.RebindImpact.PackageDenyNames, ",") != "Reporting" || strings.Join(before.RebindImpact.SharingNames, ",") != "Project" {
		t.Fatalf("incomplete preview: %+v", before.RebindImpact)
	}
	secret := "synthetic-replacement-secret"
	_, err = channels.Put(ctx, binding.WorkspaceID, actor, binding.AgentID, service.ChannelPut{Version: before.Revision, AppID: "cli_replacement", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}})
	if err != nil {
		t.Fatal(err)
	}
	after := preview()
	if after.IdentityEpoch != before.IdentityEpoch+1 || after.Access.Mode != "all" || len(after.Access.DenyIDs) != 0 || after.RebindImpact.DenyEntries != 0 || len(after.RebindImpact.PackageNames) != 0 || len(after.RebindImpact.SharingNames) != 0 {
		t.Fatalf("replacement retained old identity restrictions: %+v", after)
	}
}

func TestFirstChannelHistoryDefaultPreservesEffectivePolicy(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		order  [2]string
		optOut bool
	}{
		{"feishu_then_wecom", [2]string{"feishu", "wecom"}, false},
		{"wecom_then_feishu", [2]string{"wecom", "feishu"}, false},
		{"wecom_after_feishu_opt_out", [2]string{"feishu", "wecom"}, true},
	} {
		order := scenario.order
		t.Run(scenario.name, func(t *testing.T) {
			db, _, _, w, actor, agent, _ := managerFixture(t)
			ctx := t.Context()
			channels := &service.ChannelService{Store: NewChannelStore(db), Secret: []byte(strings.Repeat("t", 32))}
			usageAPI := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
			secret := "synthetic-channel-history"
			save := func(provider, app string, version int64) service.ChannelBinding {
				t.Helper()
				b, err := channels.PutProvider(ctx, w, actor, agent, provider, service.ChannelPut{Version: version, AppID: app, AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			currentUsage := func() service.AgentUsageResource {
				t.Helper()
				u, err := usageAPI.Get(ctx, w, actor, agent)
				if err != nil {
					t.Fatal(err)
				}
				return u
			}
			updateHistory := func(h service.UsageHistory) {
				t.Helper()
				u := currentUsage()
				u.UsageConfig.History = h
				if _, err := usageAPI.Update(ctx, w, actor, agent, u); err != nil {
					t.Fatal(err)
				}
			}

			// A paused global policy with saved Web selection must not resume
			// Web when the first external channel is configured.
			updateHistory(service.UsageHistory{Limit: 47, Channels: []string{"web"}})
			first := save(order[0], "cli_first", 0)
			if scenario.optOut {
				updateHistory(service.UsageHistory{Limit: 47, Channels: []string{}})
			}
			second := save(order[1], "cli_second", 0)
			ding := save("dingtalk", "ding_history", 0)
			if !first.History.Enabled || !second.History.Enabled || ding.History.Enabled {
				t.Fatalf("unexpected channel defaults: %v %v %v", first.History, second.History, ding.History)
			}
			want := order[:]
			if scenario.optOut {
				want = order[1:]
				b, err := channels.GetProvider(ctx, w, actor, agent, order[0])
				if err != nil || b.History.Enabled {
					t.Fatalf("second provider changed opted-out first provider: %v %v", b.History, err)
				}
			}
			if got := currentUsage().UsageConfig.History; !got.Enabled || got.Limit != 47 || !reflect.DeepEqual(got.Channels, want) {
				t.Fatal(got)
			}

			updateHistory(service.UsageHistory{Enabled: true, Limit: 43, Channels: []string{order[1]}})
			current, err := channels.GetProvider(ctx, w, actor, agent, order[0])
			if err != nil {
				t.Fatal(err)
			}
			before := currentUsage()
			rebound := save(order[0], "cli_replacement", current.Revision)
			if rebound.History.Enabled || rebound.History.Limit != 43 || rebound.IdentityEpoch != current.IdentityEpoch+1 || !reflect.DeepEqual(before, currentUsage()) {
				t.Fatalf("rebind reseeded opted-out history: first=%v now=%v", current.History, rebound.History)
			}
		})
	}
}
