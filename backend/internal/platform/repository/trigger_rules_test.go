package repository

import (
	"context"
	"encoding/json"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNamedTriggerRulesProjectionCASAndPreview(t *testing.T) {
	db, _, _, b, actor, cookie := directoryFixture(t)
	api := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	ctx := context.Background()
	old, err := api.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	rules := service.TriggerRulesResource{Version: old.Version, Rules: []service.TriggerRule{{Type: "keyword", Name: "日报", Pattern: "daily，日报", Description: "测试", Enabled: true, Channels: []string{"web", "feishu"}}, {Type: "regex", Name: "停用规则", Pattern: ".*", Enabled: false, Channels: []string{"feishu"}}}}
	saved, err := api.UpdateRules(ctx, b.WorkspaceID, actor, b.AgentID, rules)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rules[0].ID == "" || saved.Rules[0].Name != "日报" || saved.Rules[1].Enabled {
		t.Fatal(saved)
	}
	if _, err = api.UpdateRules(ctx, b.WorkspaceID, actor, b.AgentID, rules); err == nil {
		t.Fatal("stale CAS accepted")
	}
	if _, err = api.GetRules(ctx, b.WorkspaceID, service.NewID(), b.AgentID); err == nil {
		t.Fatal("unauthorized")
	}
	if _, err = api.GetRules(ctx, service.NewID(), actor, b.AgentID); err == nil {
		t.Fatal("cross workspace read")
	}
	for _, channel := range []string{"web", "feishu"} {
		var raw []byte
		if err = db.QueryRow(`SELECT platform_agent_usage_projection(usage_config,$2,NULL,0)->'trigger' FROM platform_agents WHERE id=$1`, b.AgentID, channel).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p struct{ Keywords, Regexes []string }
		if err = json.Unmarshal(raw, &p); err != nil || len(p.Keywords) != 1 || len(p.Regexes) != 0 {
			t.Fatal(string(raw), err)
		}
	}
	after, err := api.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || after.UsageConfig.Trigger.Mention != old.UsageConfig.Trigger.Mention || after.UsageConfig.History.Enabled != old.UsageConfig.History.Enabled {
		t.Fatal(after, err)
	}
	var beforeMessages int64
	countMessages := func() int64 {
		var n int64
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM manager_inputs)+(SELECT count(*) FROM manager_channel_inbox)+(SELECT count(*) FROM manager_web_group_messages)+(SELECT count(*) FROM manager_web_group_inbox)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeMessages = countMessages()
	mux := (&controller.Server{AgentUsage: api, Auth: &service.AuthService{Store: &AuthStore{DB: db}}, Origin: "http://localhost"}).Handler()
	req := service.TriggerRulesTest{Channel: "feishu", Text: "DAILY", Rules: saved.Rules}
	req.Rules[0].Pattern = "unpublished,DAILY"
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/workspaces/" + b.WorkspaceID + "/agents/" + b.AgentID + "/trigger-rules/test"
	r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	r.Header.Set("Cookie", cookie)
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("X-CSRF-Protection", "1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"matched_count":1`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if countMessages() != beforeMessages {
		t.Fatal("preview created an input or message")
	}
	again, err := api.GetRules(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil || again.Version != saved.Version || again.Rules[0].Pattern != "daily，日报" {
		t.Fatal("preview wrote config", again, err)
	}
}
