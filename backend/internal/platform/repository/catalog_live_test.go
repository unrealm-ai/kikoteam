package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"os"
	"strings"
	"testing"
	"time"
)

func verifyCatalogChat(t *testing.T, ctx context.Context, db *sql.DB, runtime *service.RuntimeService, w, owner, model, base string, call func(string, string, any, string) map[string]any, connectGitHub func(service.Plugin)) {
	t.Helper()
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(ctx, db, catalog, runtime.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: runtime.Skills.Assets}
	if err = ProvisionCatalogStarters(ctx, db, templates); err != nil {
		t.Fatal(err)
	}
	var employee string
	if err = db.QueryRowContext(ctx, "SELECT id FROM platform_agents WHERE workspace_id=$1 AND name='行情简报员'", w).Scan(&employee); err != nil {
		t.Fatal(err)
	}
	key, operations := "stock", []string{"query_index"}
	prompt := "请安排一次后台核对，汇总最近一个交易日 A 股三大指数的点位、涨跌幅、行情时间和数据来源，把简报交付回这个对话。"
	github := t.Name() == "TestLiveBuiltinGitHub"
	if github {
		key, operations = "github", []string{"list_commits", "get_commit"}
		var tid, version string
		if err = db.QueryRowContext(ctx, "SELECT id,published_version_id FROM platform_agent_templates WHERE builtin_key='release-assistant'").Scan(&tid, &version); err != nil {
			t.Fatal(err)
		}
		a, err := templates.CreateFromTemplate(ctx, w, owner, service.TemplateCreation{TemplateID: tid, TemplateVersionID: version, Name: "研发交付助理", ModelProfileID: model})
		if err != nil {
			t.Fatal(err)
		}
		employee = a.ID
		prompt = "请安排一次后台核对，查公开项目 octocat/Hello-World 默认分支最近一次提交，把完整 SHA 和提交标题回到本对话。"
	}
	var target service.Plugin
	for _, p := range catalog.Plugins {
		if p.Key == key {
			target = p.Definition()
		}
	}
	if github {
		connectGitHub(target)
	}
	conversation := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": employee}, "")["id"].(string)
	chat := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "chat", "input": map[string]string{"text": prompt}}, service.NewID())["id"].(string)
	for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		var task, turn string
		err = db.QueryRowContext(ctx, `SELECT s.id,reply.id FROM manager_tasks mt JOIN manager_sessions s ON s.task_id=mt.id AND s.mode='task'
 JOIN manager_capability_calls c ON c.session_id=s.id AND c.plugin_id=$2 AND c.tool_name=ANY($3::text[]) AND c.status='succeeded'
 JOIN manager_turns report_turn ON report_turn.session_id=s.id JOIN manager_task_reports r ON r.source_turn_id=report_turn.id AND r.target_session_id=$1
 JOIN manager_turns reply ON reply.input_id=r.target_input_id WHERE mt.source_chat_session_id=$1 ORDER BY reply.started_at DESC LIMIT 1`, chat, target.ID, operations).Scan(&task, &turn)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		events := call("GET", base+"/sessions/"+chat+"/events?type=agent.message", nil, "")["items"].([]any)
		for _, entry := range events {
			ev := entry.(map[string]any)
			if ev["turn_id"] != turn {
				continue
			}
			parts, _ := ev["content"].([]any)
			for _, part := range parts {
				msg, _ := part.(map[string]any)["text"].(string)
				if github {
					expected := os.Getenv("KIKO_LIVE_GITHUB_COMMIT")
					if len(expected) != 40 {
						t.Fatal("independent GitHub commit SHA required")
					}
					if strings.Contains(msg, expected) {
						t.Logf("github chat=%s task=%s verified_commit=%s", chat, task, expected)
						return
					}
					continue
				}
				if !strings.Contains(msg, "上证") {
					continue
				}
				data, e := (&integration.MCPProber{}).CallTool(ctx, service.RuntimePlugin{PluginID: target.ID, Endpoint: target.Endpoint}, "query_index", json.RawMessage("{}"))
				if e != nil {
					t.Fatal(e)
				}
				var source struct {
					Data struct {
						Indices []struct {
							Name   string      `json:"index_name"`
							Point  json.Number `json:"current_point"`
							Change json.Number `json:"change_percent"`
							Time   string      `json:"update_time"`
						} `json:"indices"`
					} `json:"structuredContent"`
				}
				if e = json.Unmarshal(data, &source); e != nil || len(source.Data.Indices) != 3 {
					t.Fatal("incomplete upstream quote", e)
				}
				cleaned := strings.ReplaceAll(msg, ",", "")
				for _, quote := range source.Data.Indices {
					if !strings.Contains(cleaned, quote.Point.String()) || !strings.Contains(cleaned, strings.TrimPrefix(quote.Change.String(), "-")) {
						t.Fatalf("quote mismatch for %s: %s", quote.Name, msg)
					}
				}
				var reads int
				if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_session_events WHERE session_id=$1 AND event::text LIKE '%market-close%' AND event::text LIKE '%SKILL.md%'`, task).Scan(&reads); err != nil {
					t.Fatal(err)
				}
				if reads == 0 {
					t.Fatal("market skill was not read in the delegated task")
				}
				t.Logf("market skill file events=%d", reads)
				t.Logf("chat=%s task=%s upstream_date=%s original_reply=%s", chat, task, source.Data.Indices[0].Time, msg)
				return
			}
		}
	}
	t.Fatalf("missing audited market report in original chat %s", chat)
}
