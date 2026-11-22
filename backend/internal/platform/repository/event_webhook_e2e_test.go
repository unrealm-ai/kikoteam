package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const eventTestPrompt = "根据 metadata.event 只执行对应的一项。\n1. push：调用 GitHub 的 get_commit，sha 参数逐字复制 body.after。阅读返回的 shipping.go 补丁，用一句中文告诉研发群免运费门槛发生了什么变化。回复中写出减号和加号两行中各自的具体数字。"
const issueTestPrompt = "2. issues 且 body.action 为 opened：只回复一行“新建 Issue #编号：标题”。用 body.issue.number 替换编号，用 body.issue.title 替换标题，原样保留标题。"
const eventFileTestPrompt = "读取本次事件原件，返回其中的校验词。"

var eventTestFilename = regexp.MustCompile(`webhook-[0-9a-f-]{36}\.body`)

type eventTestRepo struct {
	dir, before, after string
}

func newEventTestRepo(t *testing.T) eventTestRepo {
	t.Helper()
	r := eventTestRepo{dir: t.TempDir()}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = r.dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("test git repository: %s: %v", output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	commit := func(value string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(r.dir, "shipping.go"), []byte("package orders\n\nconst freeShippingThreshold = "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", "shipping.go")
		git("-c", "user.name=Webhook test", "-c", "user.email=webhook@example.test", "-c", "core.hooksPath="+os.DevNull, "commit", "-m", "Update order settings")
		return git("rev-parse", "HEAD")
	}
	r.before = commit("100")
	r.after = commit("80")
	return r
}

func installEventTestPlugin(t *testing.T, ctx context.Context, db *sql.DB, runtime *service.RuntimeService, workspace, actor, employee string, repo eventTestRepo) string {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "github-test", Version: "1"}, nil)
	type args struct {
		SHA string `json:"sha"`
	}
	sdk.AddTool(server, &sdk.Tool{Name: "get_commit", Description: "Read a commit diff for the orders repository by SHA"}, func(ctx context.Context, _ *sdk.CallToolRequest, in args) (*sdk.CallToolResult, map[string]string, error) {
		if in.SHA != repo.after {
			return nil, nil, fmt.Errorf("unknown test commit")
		}
		patch, err := exec.CommandContext(ctx, "git", "-C", repo.dir, "diff", "--no-ext-diff", "--no-textconv", repo.before, in.SHA, "--", "shipping.go").Output()
		return nil, map[string]string{"filename": "shipping.go", "patch": string(patch)}, err
	})
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	remote := httptest.NewTLSServer(transport)
	t.Cleanup(remote.Close)
	cert := filepath.Join(t.TempDir(), "event-mcp-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remote.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_EXTRA_CA_CERTS", cert)
	runtime.Plugins.Probe = &integration.MCPProber{Client: remote.Client()}
	p, err := runtime.Plugins.Create(ctx, workspace, actor, service.PluginCreate{Name: "GitHub test repository", Endpoint: remote.URL, AuthType: "none"})
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	catalog := &ResourceCatalog{DB: db, Assets: runtime.Skills.Assets}
	v, err := catalog.Publish(ctx, actor, "plugin", "", "", service.ResourcePublication{SourceWorkspaceID: workspace, ResourceID: p.ID, VersionID: p.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	p = v.(service.Plugin)
	_, err = runtime.Plugins.PutAccount(ctx, workspace, actor, employee, p.ID, service.PluginAccountPut{Name: "Test GitHub", PluginVersionID: p.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Packages.Create(ctx, workspace, actor, employee, service.PackageCreate{Name: "GitHub read", Plugins: []service.PackagePlugin{{PluginID: p.ID, VersionID: p.VersionID}}})
	if err != nil {
		t.Fatal(err)
	}
	return "mcp__plugin_" + strings.ReplaceAll(p.ID, "-", "_") + "__get_commit"
}

func eventModelReply(t *testing.T, w http.ResponseWriter, body runtimeRequest, after string) bool {
	t.Helper()
	var input, result string
	for _, m := range body.Messages {
		if m.Role == "user" {
			input = fmt.Sprint(m.Content)
		}
		if m.Role == "tool" {
			result += fmt.Sprint(m.Content)
		}
	}
	if strings.Contains(input, eventFileTestPrompt) {
		if result == "" {
			path := eventTestFilename.FindString(input)
			if path == "" || strings.Contains(input, "蓝莓") {
				t.Error("file body was lost or inlined")
			}
			humanToolStream(t, w, []string{"mcp__builtin__read"}, []any{map[string]string{"path": path}})
		} else {
			if !strings.Contains(result, "蓝莓") {
				t.Error("authorized read did not return the original file")
			}
			humanTextStream(t, w, "已读取原件")
		}
		return true
	}
	if !strings.Contains(input, eventTestPrompt) || !strings.Contains(input, issueTestPrompt) {
		return false
	}
	if strings.Contains(input, "运费文档遗漏") {
		humanTextStream(t, w, "新建 Issue #42：运费文档遗漏")
		return true
	}
	if !strings.Contains(input, after) {
		t.Error("missing push SHA in model input")
		return false
	}
	if result != "" {
		if !strings.Contains(result, "freeShippingThreshold") {
			t.Errorf("missing commit diff in tool result: %.180s", result)
		}
		humanTextStream(t, w, "代码变更：免运费门槛从 100 降到 80。")
		return true
	}
	for _, tool := range body.Tools {
		if strings.HasSuffix(tool.Function.Name, "__get_commit") {
			humanToolStream(t, w, []string{tool.Function.Name}, []any{map[string]string{"sha": after}})
			return true
		}
	}
	t.Error("authorized get_commit tool missing")
	humanTextStream(t, w, "读取失败")
	return true
}

func sendEventRequest(t *testing.T, server *httptest.Server, id string, headers http.Header, body string, want int) manager.TriggerReceipt {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/triggers/events/"+id, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = headers
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != want {
		t.Fatalf("native hook HTTP status=%d want=%d body=%s err=%v", res.StatusCode, want, raw, err)
	}
	var receipt manager.TriggerReceipt
	if want == 202 || want == 200 {
		if err = json.Unmarshal(raw, &receipt); err != nil {
			t.Fatal(err)
		}
	}
	return receipt
}

func githubHeaders(secret, kind, id, body string) http.Header {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("X-GitHub-Delivery", id)
	h.Set("X-GitHub-Event", kind)
	h.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

func eventWebhookAcceptance(t *testing.T, ctx context.Context, db *sql.DB, store *managerrepo.Store, mux http.Handler, w, actor, employee, commitTool string, repo eventTestRepo, wait func(func() bool)) {
	t.Helper()
	server := httptest.NewServer(mux)
	defer server.Close()
	group := service.NewID()
	reviewExec(t, db, `INSERT INTO platform_web_groups(id,workspace_id,title,created_by) VALUES($1,$2,'Webhook 研发测试群',$3)`, group, w, actor)
	reviewExec(t, db, `INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status)VALUES($1,$2,$3,'active')`, group, w, employee)
	target, err := store.ResolveTriggerGroupCandidate(ctx, w, actor, employee, manager.TriggerGroupResolve{Provider: "web", CandidateID: group})
	if err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(manager.EventHookConfig{Adapter: "event", Source: "github", Prompt: eventTestPrompt + " 提交读取工具全名是 " + commitTool + "。\n" + issueTestPrompt})
	if err != nil {
		t.Fatal(err)
	}
	write := manager.TriggerWrite{Kind: "webhook", AllowedActions: []string{"create_task"}, Config: config, NotificationTargets: json.RawMessage(`[{"conversation_id":"` + target.ConversationID + `"}]`)}
	hook, err := store.SaveTrigger(ctx, w, actor, employee, "", write)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateTriggerCredential(ctx, w, actor, employee, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	ping := url.Values{"payload": {`{"zen":"hello","hook_id":456}`}}.Encode()
	pingHeaders := githubHeaders(cred.Secret, "ping", service.NewID(), ping)
	pingHeaders.Set("Content-Type", "application/x-www-form-urlencoded")
	sendEventRequest(t, server, hook.ID, pingHeaders, ping, 204)
	write.Version, write.Enabled = hook.Version, true
	hook, err = store.SaveTrigger(ctx, w, actor, employee, hook.ID, write)
	if err != nil {
		t.Fatal(err)
	}
	push := fmt.Sprintf(`{"repository":{"id":123,"full_name":"acme/orders"},"ref":"refs/heads/main","before":"%s","after":"%s"}`, repo.before, repo.after)
	issue := url.Values{"payload": {`{"action":"opened","issue":{"number":42,"title":"运费文档遗漏","html_url":"https://github.com/acme/orders/issues/42"}}`}}.Encode()
	for _, item := range []struct{ name, body, expected string }{{"push", push, "免运费门槛从 100 降到 80"}, {"issues", issue, "新建 Issue #42：运费文档遗漏"}} {
		headers := githubHeaders(cred.Secret, item.name, service.NewID(), item.body)
		if item.name == "issues" {
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		got := sendEventRequest(t, server, hook.ID, headers, item.body, 202)
		again := sendEventRequest(t, server, hook.ID, headers, item.body, 200)
		if !again.Duplicate || again.TaskID != got.TaskID {
			t.Fatal("duplicate created a new task")
		}
		var text string
		wait(func() bool {
			if e := store.MaterializeTriggerNotifications(ctx); e != nil {
				t.Fatal(e)
			}
			e := db.QueryRowContext(ctx, `SELECT m.text FROM manager_trigger_notifications n JOIN manager_web_group_messages m ON m.id=n.web_message_id WHERE n.task_id=$1 AND n.source_input_id=$2 AND n.status='sent' AND m.group_id=$3`, got.TaskID, got.InputID, group).Scan(&text)
			return e == nil
		})
		ok := strings.Contains(text, item.expected)
		if os.Getenv("KIKO_EVENT_MODEL_BASE_URL") != "" {
			if item.name == "push" {
				ok = strings.Contains(text, "100") && strings.Contains(text, "80")
			} else {
				ok = strings.Contains(text, "42") && strings.Contains(text, "运费文档遗漏")
			}
		}
		if !ok {
			t.Errorf("wrong %s group message: %s", item.name, text)
		}
		t.Logf("%s same hook, actual Web group message: %s", item.name, text)
		var calls int
		var diffCalls int
		if err = db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER (WHERE event::text LIKE '%freeShippingThreshold%') FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.tool_result' AND event->>'status'='completed' AND event->>'name' LIKE '%get_commit'`, got.SessionID).Scan(&calls, &diffCalls); err != nil || item.name == "issues" && calls != 0 || item.name == "push" && diffCalls == 0 {
			t.Errorf("%s get_commit calls=%d with actual diff=%d err=%v", item.name, calls, diffCalls, err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_web_group_messages WHERE group_id=$1`, group).Scan(&count); err != nil || count != 2 {
		t.Fatalf("group message count=%d err=%v", count, err)
	}
	if os.Getenv("KIKO_EVENT_MODEL_BASE_URL") != "" {
		return
	}
	// Edit the prompt and clear the result group while keeping the public hook and key.
	write.Version, write.NotificationTargets = hook.Version, json.RawMessage(`[]`)
	write.Config, err = json.Marshal(manager.EventHookConfig{Adapter: "event", Source: "github", Prompt: eventFileTestPrompt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveTrigger(ctx, w, actor, employee, hook.ID, write); err != nil {
		t.Fatal(err)
	}
	large := `{"repository":{"id":123},"word":"蓝莓","extra":"` + strings.Repeat("x", 9000) + `"}`
	fileEvent := sendEventRequest(t, server, hook.ID, githubHeaders(cred.Secret, "push", service.NewID(), large), large, 202)
	wait(func() bool {
		var finished bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_turns WHERE session_id=$1 AND status='completed' AND result_summary LIKE '%已读取原件%')`, fileEvent.SessionID).Scan(&finished)
		return err == nil && finished
	})
}
