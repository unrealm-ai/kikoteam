package repository

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mr "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/imagegen"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	pr "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// With KIKO_TEST_ARK_MODEL set, run the compiled test binary with the Ark key
// on stdin. This sends two billable requests, one admin probe and one task.
func TestManagerRuntimeImageGeneration(t *testing.T) { testManagerRuntime(t, "images") }

type imageRuntimeFixture struct {
	service          *imagegen.Service
	base, model, key string
	calls            atomic.Int32
}

type imageTestTransport struct {
	base    http.RoundTripper
	fixture *imageRuntimeFixture
	t       *testing.T
}

func (tr imageTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if tr.fixture.calls.Add(1) > 2 {
		return nil, fmt.Errorf("unexpected third supplier call")
	}
	res, err := tr.base.RoundTrip(r)
	if err != nil {
		tr.t.Logf("image supplier transport: %v", err)
		return nil, err
	}
	tr.t.Logf("image supplier HTTP %d, request_id=%s", res.StatusCode, res.Header.Get("X-Request-ID"))
	if res.StatusCode != http.StatusOK {
		var envelope struct {
			Error struct{ Code, Message string }
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, 8192))
		_ = res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(raw))
		if e == nil && json.Unmarshal(raw, &envelope) == nil {
			msg := strings.ReplaceAll(envelope.Error.Message, tr.fixture.key, "[redacted]")
			tr.t.Logf("supplier rejection: %.120s %.400s", envelope.Error.Code, msg)
		}
	}
	return res, nil
}

func (f *imageRuntimeFixture) setup(t *testing.T, db *sql.DB, rt *service.RuntimeService, store *mr.Store, mux *http.ServeMux, auth *service.AuthService, actor string) {
	t.Helper()
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(t.Context(), db, catalog, rt.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	store.WorkAssets = rt.Skills.Assets.(manager.WorkAssets)
	client := integration.PublicHTTPClient(nil, 5*time.Minute, 5*time.Minute)
	f.model = os.Getenv("KIKO_TEST_ARK_MODEL")
	if f.model != "" {
		f.base = "https://ark.cn-beijing.volces.com/api/v3"
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			t.Fatal("expected Ark API key on stdin")
		}
		f.key = strings.TrimSpace(line)
	} else {
		f.model, f.key = "fixture", "test-only"
		var b bytes.Buffer
		if err = png.Encode(&b, image.NewGray(image.Rect(0, 0, 1024, 1024))); err != nil {
			t.Fatal(err)
		}
		mock := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-ID", "fixture")
			if err := json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": b.Bytes()}}}); err != nil {
				t.Error(err)
			}
		}))
		t.Cleanup(mock.Close)
		client, f.base = mock.Client(), mock.URL
	}
	client.Transport = imageTestTransport{client.Transport, f, t}
	root := make([]byte, 32)
	rand.Read(root)
	f.service = &imagegen.Service{Store: pr.Store{DB: db}, Secrets: ps.Secrets{Root: root}, Root: store.RuntimeRoot, HTTP: client}
	reviewExec(t, db, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", actor)
	reviewExec(t, db, "UPDATE platform_credit_settings SET signup_bonus_units=20000000 WHERE id=1")
	if err = f.service.Transaction(t.Context(), func(tx *sql.Tx) error { return pr.Signup(t.Context(), tx, actor) }); err != nil {
		t.Fatal(err)
	}
	agents := &service.AgentService{Store: &AgentStore{DB: db, EnsureKey: f.service.Secrets.EnsureKey}}
	mux.Handle("/api/", (&controller.Server{Images: f.service, Auth: auth, Agents: agents, Workspaces: &service.WorkspaceService{Store: NewWorkspaceStore(db)}, Official: &ps.Models{Store: f.service.Store}}).Handler())
	if dir := os.Getenv("KIKO_PROXY_WEB_DIST"); dir != "" {
		mux.Handle("/", http.FileServer(http.Dir(dir)))
	}
}

var generatedPath = regexp.MustCompile(`outputs/images/[0-9a-f-]{36}\.png`)

const imageProgressSummary = "已发布翠鸟水彩原图。"

func imageRuntimeReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) {
	const generate = "mcp__image_generation__generate_image"
	var results []string
	for _, m := range body.Messages {
		if m.Role == "tool" {
			results = append(results, fmt.Sprint(m.Content))
		}
	}
	var found bool
	for _, tool := range body.Tools {
		if tool.Function.Name == generate {
			found = true
		}
	}
	if !found {
		t.Error("task model did not receive the official image tool")
		humanTextStream(t, w, "Missing image tool")
		return
	}
	args := map[string]string{"prompt": "水彩插画，一只翠鸟停在雨后荷塘的一枝莲蓬上，青绿与暖橙色，细腻的纸张纹理，画面安静，无文字。", "aspect_ratio": "square", "request_key": "kingfisher-1"}
	switch len(results) {
	case 0:
		humanToolStream(t, w, []string{generate}, []any{args})
	case 1, 2:
		path := generatedPath.FindString(results[0])
		if path == "" || !strings.Contains(results[0], "settled") {
			t.Errorf("generation did not settle: %.600s", results[0])
			humanTextStream(t, w, "Generation failed")
			return
		}
		if len(results) == 1 {
			humanToolStream(t, w, []string{generate}, []any{args})
			return
		}
		if !strings.Contains(results[1], path) || !strings.Contains(results[1], "settled") {
			t.Error("replay returned a different result")
		}
		humanToolStream(t, w, []string{"mcp__work_items__artifact_publish"}, []any{map[string]string{"path": path}})
	case 3:
		humanToolStream(t, w, []string{"mcp__task_progress__update_task_progress"}, []any{map[string]string{"base_plan_version": "0", "summary": imageProgressSummary}})
	default:
		humanTextStream(t, w, "翠鸟图已发布。")
	}
}

func (f *imageRuntimeFixture) verify(t *testing.T, call func(string, string, any, string) map[string]any, server, token, workspace, actor string, agent service.Agent) {
	t.Helper()
	const config = "/api/v1/platform/default-plugins/image-generation"
	base := "/api/v1/workspaces/" + workspace
	call("PUT", base+"/agents/"+agent.ID+"/payer-binding", map[string]any{"version": agent.Version, "confirm_self": true}, "")
	body := map[string]any{"version": call("GET", config, nil, "")["version"], "enabled": true, "base_url": f.base, "upstream_model_id": f.model, "api_key": f.key, "credits_per_image": "5", "timeout_seconds": 300}
	probe := call("POST", config+"/test", body, "")
	thumb, err := base64.StdEncoding.DecodeString(fmt.Sprint(probe["preview_png"]))
	if err != nil {
		t.Fatal(err)
	}
	size, err := png.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || size.Width != 192 || size.Height != 192 {
		t.Fatal("invalid admin preview", size, err)
	}
	body["receipt"] = probe["receipt"]
	saved := call("PATCH", config, body, "")
	if saved["credential_configured"] != true || saved["enabled"] != true || saved["api_key"] != nil {
		t.Fatal("admin configuration not enabled or key exposed")
	}
	balance := func() int64 {
		var v int64
		if err := f.service.DB.QueryRow("SELECT balance_units FROM platform_credit_wallets WHERE account_id=$1", actor).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if balance() != 20000000 || f.calls.Load() != 1 {
		t.Fatal("unexpected admin probe charge or supplier calls")
	}
	conversation := call("POST", base+"/conversations", map[string]any{"agent_id": agent.ID}, "")["id"].(string)
	task := call("POST", base+"/sessions", map[string]any{"conversation_id": conversation, "mode": "task", "input": map[string]string{"text": "请生成并发布一张翠鸟水彩画。"}}, "image-task")
	taskPath := base + "/tasks/" + task["task_id"].(string)
	deadline := time.Now().Add(6 * time.Minute)
	var detail map[string]any
	for time.Now().Before(deadline) {
		detail = call("GET", taskPath, nil, "")
		if turn, ok := detail["latest_turn"].(map[string]any); ok {
			switch turn["status"] {
			case "completed":
				goto completed
			case "failed", "cancelled":
				t.Fatalf("task ended with %v", turn["status"])
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("image task timed out")
completed:
	progress, ok := detail["reported_progress"].(map[string]any)
	if !ok || progress["summary"] != imageProgressSummary {
		t.Fatal("image task lost its reported progress", progress)
	}
	images, ok := detail["images"].([]any)
	if !ok || len(images) != 1 {
		t.Fatal("task did not publish exactly one PNG")
	}
	artifact := images[0].(map[string]any)["id"].(string)
	usage := call("GET", "/api/v1/image-generations", nil, "")["items"].([]any)
	if len(usage) != 1 {
		t.Fatal("expected one usage record", len(usage))
	}
	record := usage[0].(map[string]any)
	if record["billing_status"] != "settled" || record["charged_credits"] != "5.000000" || record["image_count"] != float64(1) || record["agent_id"] != agent.ID || record["payer_account_id"] != actor {
		t.Fatal("wrong settlement", record)
	}
	ledger := call("GET", "/api/v1/accounts/"+actor+"/credit-ledger?kind=image_charge", nil, "")["items"].([]any)
	if len(ledger) != 1 || f.calls.Load() != 2 || balance() != 15000000 {
		t.Fatal("replay duplicated a charge or upstream dispatch", len(ledger), f.calls.Load(), balance())
	}
	var digest string
	if err := f.service.DB.QueryRow("SELECT asset_version FROM platform_image_generations WHERE id=$1", record["generation_id"]).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "?download=1"} {
		req, err := http.NewRequestWithContext(t.Context(), "GET", server+taskPath+"/artifacts/"+artifact+"/content"+suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") != (suffix != "") || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			t.Fatal("artifact response mismatched archived PNG", res.StatusCode, err)
		}
		if suffix != "" {
			meta, err := png.DecodeConfig(bytes.NewReader(raw))
			if err != nil || meta.Width != 1024 || meta.Height != 1024 {
				t.Fatal("download is not the requested image", meta, err)
			}
			t.Logf("task=%v generation=%v PNG=%dx%d bytes=%d sha256=%s wallet=20->15 image_charges=1 supplier_posts=2", task["task_id"], record["generation_id"], meta.Width, meta.Height, len(raw), digest)
			if dir := os.Getenv("KIKO_TEST_IMAGE_OUTPUT"); dir != "" {
				if err = os.WriteFile(filepath.Join(dir, record["generation_id"].(string)+".png"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if script := os.Getenv("KIKO_TEST_IMAGE_BROWSER"); script != "" {
		cmd := exec.CommandContext(t.Context(), "node", script, server, token, workspace, task["task_id"].(string), digest)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("browser: %v %s", err, out)
		} else {
			t.Log(strings.TrimSpace(string(out)))
		}
	}
}
