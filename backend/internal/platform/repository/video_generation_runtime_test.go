package repository

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	pr "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	ps "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/videogen"
)

func TestManagerRuntimeVideoGeneration(t *testing.T) { testManagerRuntime(t, "video-generation") }

type videoRuntimeFixture struct {
	service *videogen.Service
	live    bool
	base    string
	calls   atomic.Int32
}

func videoSample(t *testing.T, size string, seconds int) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s="+size, "-t", fmt.Sprint(seconds), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart", p)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type videoTestTransport struct {
	base   http.RoundTripper
	posts  *atomic.Int32
	output string
}

func (tr videoTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != "POST" {
		return tr.base.RoundTrip(r)
	}
	if tr.posts.Add(1) != 1 {
		return nil, fmt.Errorf("duplicate billable submit")
	}
	res, e := tr.base.RoundTrip(r)
	if e == nil && res.StatusCode/100 == 2 && tr.output != "" {
		raw, err := io.ReadAll(io.LimitReader(res.Body, 65536))
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		var job struct{ ID string }
		if json.Unmarshal(raw, &job) == nil {
			if err = os.WriteFile(filepath.Join(tr.output, "upstream-id"), []byte(job.ID), 0600); err != nil {
				return nil, err
			}
		}
	}
	return res, e
}
func (f *videoRuntimeFixture) setup(t *testing.T, db *sql.DB, rt *service.RuntimeService, store *mr.Store, mux *http.ServeMux, auth *service.AuthService, actor string) {
	t.Helper()
	catalog, e := service.BuiltinCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncCatalog(t.Context(), db, catalog, rt.Skills.Assets); e != nil {
		t.Fatal(e)
	}
	store.WorkAssets = rt.Skills.Assets.(manager.WorkAssets)
	root := make([]byte, 32)
	rand.Read(root)
	f.live = os.Getenv("KIKO_TEST_NBILITY_VIDEO") == "1"
	client := integration.PublicHTTPClient(nil, 30*time.Second, 45*time.Second)
	if f.live {
		f.base = "https://api.nbility.ai"
		client.Transport = videoTestTransport{client.Transport, &f.calls, os.Getenv("KIKO_TEST_VIDEO_OUTPUT")}
	} else {
		probe, clip := videoSample(t, "1344x768", 1), videoSample(t, "768x1344", 4)
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/media/") {
				if r.Header.Get("Authorization") != "" {
					t.Error("credential sent to media URL")
				}
				if r.URL.Path == "/media/probe" {
					if _, err := w.Write(probe); err != nil {
						t.Error(err)
					}
				} else {
					if _, err := w.Write(clip); err != nil {
						t.Error(err)
					}
				}
				return
			}
			if r.Header.Get("Authorization") != "Bearer fixture-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Method == "POST" {
				var a struct {
					Duration int
					Ratio    string
				}
				if json.NewDecoder(r.Body).Decode(&a) != nil {
					t.Error("invalid submit")
				}
				id := "probe"
				if a.Duration == 4 && a.Ratio == "9:16" {
					id = "clip"
					f.calls.Add(1)
				}
				if err := json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "queued"}); err != nil {
					t.Error(err)
				}
				return
			}
			id := filepath.Base(r.URL.Path)
			if err := json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "completed", "url": "https://" + r.Host + "/media/" + id}); err != nil {
				t.Error(err)
			}
		}))
		t.Cleanup(server.Close)
		client, f.base = server.Client(), server.URL
	}
	f.service = &videogen.Service{Store: pr.Store{DB: db}, Root: store.RuntimeRoot, Secrets: ps.Secrets{Root: root}, HTTP: client, Notify: mr.NotifyVideo}
	reviewExec(t, db, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", actor)
	reviewExec(t, db, "UPDATE platform_credit_settings SET signup_bonus_units=20000000 WHERE id=1")
	if e = f.service.Transaction(t.Context(), func(tx *sql.Tx) error { return pr.Signup(t.Context(), tx, actor) }); e != nil {
		t.Fatal(e)
	}
	agents := &service.AgentService{Store: &AgentStore{DB: db, EnsureKey: f.service.Secrets.EnsureKey}}
	mux.Handle("/api/", (&controller.Server{Videos: f.service, Auth: auth, Agents: agents, Workspaces: &service.WorkspaceService{Store: NewWorkspaceStore(db)}, Official: &ps.Models{Store: f.service.Store}}).Handler())
	if dir := os.Getenv("KIKO_PROXY_WEB_DIST"); dir != "" {
		mux.Handle("/", http.FileServer(http.Dir(dir)))
	}
}

var videoOutputPath = regexp.MustCompile(`outputs/videos/[0-9a-f-]{36}\.mp4`)

func videoGenerationReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) {
	const name = "mcp__video_generation__generate_video"
	var path string
	count := 0
	for _, m := range body.Messages {
		if m.Role == "user" {
			count = 0
			path = videoOutputPath.FindString(fmt.Sprint(m.Content))
		}
		if m.Role == "tool" {
			count++
		}
	}
	if path != "" {
		if count == 0 {
			humanToolStream(t, w, []string{"mcp__work_items__artifact_publish"}, []any{map[string]string{"path": path}})
		} else {
			humanTextStream(t, w, "MP4 已发布。")
		}
		return
	}
	if count < 2 {
		for _, tool := range body.Tools {
			if tool.Function.Name == name {
				humanToolStream(t, w, []string{name}, []any{map[string]any{"prompt": "A portrait close-up of a jade-green ceramic teacup on a wooden table, a delicate curl of steam rises in morning light, steady camera.", "aspect_ratio": "portrait", "seconds": 4, "request_key": "morning-tea-v1"}})
				return
			}
		}
		t.Error("video tool missing")
	}
	humanTextStream(t, w, "视频已提交，等待成片。")
}
func (f *videoRuntimeFixture) verify(t *testing.T, call func(string, string, any, string) map[string]any, server, token, w, actor string, agent service.Agent) {
	t.Helper()
	base := "/api/v1/workspaces/" + w
	const config = "/api/v1/platform/default-plugins/video-generation"
	call("PUT", base+"/agents/"+agent.ID+"/payer-binding", map[string]any{"version": agent.Version, "confirm_self": true}, "")
	if f.live {
		// Seed only the disposable database so the live check consumes one employee render.
		key, e := bufio.NewReader(os.Stdin).ReadString('\n')
		if e != nil || strings.TrimSpace(key) == "" {
			t.Fatal("supply the live key on stdin")
		}
		plugin := service.CatalogID("plugin", videogen.Key)
		secret, e := f.service.Secrets.Seal("video-provider", plugin, strings.TrimSpace(key))
		if e != nil {
			t.Fatal(e)
		}
		reviewExec(t, f.service.DB, `INSERT INTO platform_video_generation_configs(plugin_id,version,base_url,upstream_model_id,credential_ciphertext,unit_price_units,max_concurrent_generations,workspace_daily_seconds,created_by) VALUES($1,1,$2,'MiniMax-H3',$3,2000000,1,10,$4)`, plugin, f.base, secret, actor)
		reviewExec(t, f.service.DB, "UPDATE platform_default_plugin_settings SET enabled=true,active_config_version=1,version=1 WHERE plugin_id=$1", plugin)
	} else {
		p := map[string]any{"version": call("GET", config, nil, "")["version"], "base_url": f.base, "api_key": "fixture-key", "enabled": true, "credits_per_second": "2"}
		p["receipt"] = call("POST", config+"/test", p, "")["receipt"]
		call("PATCH", config, p, "")
	}
	conv := call("POST", base+"/conversations", map[string]any{"agent_id": agent.ID}, "")["id"].(string)
	task := call("POST", base+"/sessions", map[string]any{"conversation_id": conv, "mode": "task", "input": map[string]string{"text": "生成一条 4 秒竖屏茶杯短片并发布 MP4。"}}, "tea-video")["task_id"].(string)
	path := base + "/tasks/" + task
	var artifact, last string
	deadline := time.Now().Add(9 * time.Minute)
	for time.Now().Before(deadline) {
		if e := f.service.Reconcile(t.Context()); e != nil {
			t.Fatal(e)
		}
		detail := call("GET", path, nil, "")
		if states, ok := detail["video_generations"].([]any); ok && len(states) > 0 {
			state := states[0].(map[string]any)["status"].(string)
			if state != last {
				t.Log("video status", state)
				last = state
			}
			if state == "failed" || state == "pending_review" {
				var code string
				if err := f.service.DB.QueryRow("SELECT coalesce(error_code,'') FROM platform_video_generations WHERE task_id=$1", task).Scan(&code); err != nil {
					t.Fatal(err)
				}
				t.Fatalf("generation %s: %s", state, code)
			}
		}
		if videos, ok := detail["videos"].([]any); ok && len(videos) == 1 {
			artifact = videos[0].(map[string]any)["id"].(string)
			break
		}
		time.Sleep(time.Second)
	}
	if artifact == "" {
		t.Fatal("MP4 was not published", last)
	}
	ledger := call("GET", "/api/v1/accounts/"+actor+"/credit-ledger?kind=video_charge", nil, "")["items"].([]any)
	if len(ledger) != 1 || f.calls.Load() != 1 {
		t.Fatal("expected one billable submit and one charge", len(ledger), f.calls.Load())
	}
	entry := ledger[0].(map[string]any)
	if entry["amount"] != "-8.000000" || entry["balance_after"] != "12.000000" {
		t.Fatal("incorrect wallet settlement", entry)
	}
	var digest string
	if e := f.service.DB.QueryRow("SELECT asset_version FROM platform_video_generations WHERE task_id=$1 AND notified AND billing_status='settled'", task).Scan(&digest); e != nil {
		t.Fatal(e)
	}
	endpoint := server + path + "/artifacts/" + artifact + "/content"
	ranged, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	ranged.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
	ranged.Header.Set("Range", "bytes=0-15")
	part, err := http.DefaultClient.Do(ranged)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := io.ReadAll(part.Body)
	_ = part.Body.Close()
	if err != nil || part.StatusCode != http.StatusPartialContent || len(prefix) != 16 || !strings.HasPrefix(part.Header.Get("Content-Range"), "bytes 0-15/") {
		t.Fatal("video range response", part.StatusCode, len(prefix), err)
	}
	for _, suffix := range []string{"", "?download=1"} {
		req, e := http.NewRequestWithContext(t.Context(), "GET", endpoint+suffix, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if e != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "video/mp4" || strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") != (suffix != "") || fmt.Sprintf("%x", sha256.Sum256(b)) != digest {
			t.Fatal("incorrect MP4 response", res.StatusCode)
		}
		if dir := os.Getenv("KIKO_TEST_VIDEO_OUTPUT"); dir != "" && suffix != "" {
			if e = os.WriteFile(filepath.Join(dir, "morning-tea.mp4"), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	if script := os.Getenv("KIKO_TEST_VIDEO_BROWSER"); script != "" {
		cmd := exec.CommandContext(t.Context(), "node", script, server, token, w, task, digest)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(out))
		}
		t.Log(string(out))
	}
	t.Logf("task=%s sha256=%s wallet=20->12 one video_charge, one employee POST", task, digest)
}
