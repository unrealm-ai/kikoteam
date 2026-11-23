package repository

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func runtimeCapabilityServices(t *testing.T, db *sql.DB) *service.RuntimeService {
	t.Helper()
	return runtimeCapabilityServicesAt(t, db, t.TempDir())
}
func runtimeCapabilityServicesAt(t *testing.T, db *sql.DB, root string) *service.RuntimeService {
	t.Helper()
	assets, err := skillasset.NewStore(t.Context(), skillasset.Config{LocalRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = assets.Close() })
	return &service.RuntimeService{Packages: &service.PackageService{Store: NewPackageStore(db)}, Skills: &service.SkillService{Store: NewSkillStore(db), Assets: assets}, Plugins: &service.PluginService{Store: NewPluginStore(db), Secret: []byte(strings.Repeat("x", 40))}}
}

func publishRuntimeDirectory(t *testing.T, ctx context.Context, s *service.SkillService, w, actor string, old service.Skill, version string) service.Skill {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	files := map[string]string{
		"SKILL.md":             "---\nname: report-skill\ndescription: Create the sample report using packaged references and scripts\n---\nbody-" + version + "-only\nRead references/value.txt and run scripts/run.sh with bash.\n",
		"references/value.txt": "reference-" + version,
		"scripts/run.sh":       "#!/bin/sh\ncat \"$(dirname \"$0\")/../references/value.txt\" > skill-result.txt\n",
	}
	for name, data := range files {
		f, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write([]byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	imp, err := s.Import(ctx, w, actor, service.SkillImportRequest{Source: "zip", ZIPBase64: base64.StdEncoding.EncodeToString(buf.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	req := service.SkillPublish{ImportID: imp.ID, Version: old.Version}
	if old.ID == "" {
		req.Name = "Report directory"
	}
	out, err := s.Publish(ctx, w, actor, old.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func runtimeCapabilityAcceptance(t *testing.T, ctx context.Context, db *sql.DB, runtime *service.RuntimeService, w, actor, agent, temp string, call func(string, string, any, string) map[string]any, wait func(func() bool), restart func()) {
	t.Helper()
	var calls, forbidden atomic.Int64
	var pack service.CapabilityPackage
	var packMu sync.Mutex
	hanging := make(chan struct{}, 1)
	hangingStopped := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	firstCall := make(chan struct{})
	upgraded := make(chan struct{})
	var upgradeOnce sync.Once
	finishUpgrade := func() { upgradeOnce.Do(func() { close(upgraded) }) }
	defer finishUpgrade()
	server := sdk.NewServer(&sdk.Implementation{Name: "runtime-fixture", Version: "1"}, nil)
	type args struct {
		Query string `json:"query"`
	}
	sdk.AddTool(server, &sdk.Tool{Name: "lookup", Description: "Lookup fixture data"}, func(ctx context.Context, _ *sdk.CallToolRequest, in args) (*sdk.CallToolResult, map[string]string, error) {
		if calls.Add(1) == 1 {
			close(firstCall)
			select {
			case <-upgraded:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		if in.Query == "hang" {
			defer close(hangingStopped)
			hanging <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-release:
				return nil, map[string]string{"value": "fixture-stopped"}, nil
			}
		}
		if in.Query == "revoke" {
			packMu.Lock()
			defer packMu.Unlock()
			off := false
			var err error
			pack, err = runtime.Packages.Update(ctx, w, actor, agent, pack.ID, service.PackagePatch{Version: pack.Version, Enabled: &off})
			if err != nil {
				return nil, nil, err
			}
		}
		return nil, map[string]string{"value": "plugin-ok"}, nil
	})
	sdk.AddTool(server, &sdk.Tool{Name: "delete", Description: "Second account tool"}, func(context.Context, *sdk.CallToolRequest, args) (*sdk.CallToolResult, map[string]string, error) {
		forbidden.Add(1)
		return nil, map[string]string{"value": "forbidden"}, nil
	})
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	credential := "!literal-$HOME"
	remote := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Plugin-Key") != credential {
			http.Error(out, "unauthorized", http.StatusUnauthorized)
			return
		}
		transport.ServeHTTP(out, req)
	}))
	defer remote.Close()
	defer func() { releaseOnce.Do(func() { close(release) }) }()
	cert := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remote.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_EXTRA_CA_CERTS", cert)
	runtime.Plugins.Probe = &integration.MCPProber{Client: remote.Client()}
	p, err := runtime.Plugins.Create(ctx, w, actor, service.PluginCreate{Name: "Remote fixture", Endpoint: remote.URL, AuthType: "api_key", HeaderName: "X-Plugin-Key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", actor); err != nil {
		t.Fatal(err)
	}
	catalog := &ResourceCatalog{DB: db, Assets: runtime.Skills.Assets}
	publish := func(kind, id, source, pin string, revision int64) any {
		t.Helper()
		result, e := catalog.Publish(ctx, actor, kind, "", id, service.ResourcePublication{SourceWorkspaceID: w, ResourceID: source, VersionID: pin, Version: revision})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	p = publish("plugin", "", p.ID, p.VersionID, 0).(service.Plugin)
	account, err := runtime.Plugins.PutAccount(ctx, w, actor, agent, p.ID, service.PluginAccountPut{Name: "Employee fixture", PluginVersionID: p.VersionID, Secret: &credential})
	if err != nil {
		t.Fatal(err)
	}
	var tool service.MCPTool
	for _, v := range account.Tools {
		if v.Name == "lookup" {
			tool = v
		}
	}
	if tool.Name == "" {
		t.Fatal("real MCP discovery did not return lookup")
	}
	source := publishRuntimeDirectory(t, ctx, runtime.Skills, w, actor, service.Skill{}, "v1")
	v1 := publish("skill", "", source.ID, source.CurrentVersionID, 0).(service.Skill)
	packMu.Lock()
	pack, err = runtime.Packages.Create(ctx, w, actor, agent, service.PackageCreate{Name: "Runtime package", Skills: []service.PackageSkill{{SkillID: v1.ID, VersionID: v1.CurrentVersion.ID}}, Plugins: []service.PackagePlugin{{PluginID: p.ID, VersionID: p.VersionID}}})
	packID := pack.ID
	packMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	restart()
	base := "/api/v1/workspaces/" + w
	c := call("POST", base+"/conversations", map[string]any{"agent_id": agent}, "")["id"].(string)
	create := func(text, key string) string {
		return call("POST", base+"/sessions", map[string]any{"conversation_id": c, "mode": "task", "input": map[string]string{"text": text}}, key)["id"].(string)
	}
	id := create("capability-auto", "cap-auto")
	await := func(id string, count int) {
		t.Helper()
		wait(func() bool {
			var n int
			err := db.QueryRowContext(ctx, `SELECT count(*) FROM manager_turns WHERE session_id=$1 AND status='completed'`, id).Scan(&n)
			return err == nil && n == count
		})
	}
	select {
	case <-firstCall:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	source = publishRuntimeDirectory(t, ctx, runtime.Skills, w, actor, source, "v2")
	v2 := publish("skill", v1.ID, source.ID, source.CurrentVersionID, v1.Version).(service.Skill)
	call("POST", base+"/agents/"+agent+"/capability-upgrades", map[string]string{"kind": "skill", "resource_id": v1.ID, "from_version_id": v1.CurrentVersionID, "to_version_id": v2.CurrentVersionID}, "")
	finishUpgrade()
	await(id, 1)
	project := filepath.Join(temp, "state", "workspaces", w, "projects", "conv-"+c)
	checkFile := func(want string) {
		t.Helper()
		data, e := os.ReadFile(filepath.Join(project, "skill-result.txt"))
		if e != nil || string(data) != want {
			t.Fatalf("packaged script result=%q err=%v want=%q", data, e, want)
		}
	}
	checkFile("reference-v1")
	if calls.Load() != 1 || forbidden.Load() != 0 {
		t.Fatal("remote allowed/forbidden call counts", calls.Load(), forbidden.Load())
	}
	packMu.Lock()
	pack, err = runtime.Packages.Get(ctx, w, actor, agent, packID)
	packMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/sessions/"+id+"/inputs", map[string]string{"text": "/skill:report-skill capability-explicit"}, "cap-explicit")
	await(id, 2)
	checkFile("reference-v2")
	newID := create("capability-auto", "cap-v2")
	await(newID, 1)
	checkFile("reference-v2")
	before := calls.Load()
	call("POST", base+"/sessions/"+id+"/inputs", map[string]string{"text": "capability-revoke"}, "cap-revoke")
	await(id, 3)
	if calls.Load() != before+1 || forbidden.Load() != 0 {
		t.Fatal("revocation did not stop next remote call", calls.Load(), before)
	}
	var authorized, denied int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE status='succeeded'),count(*) FILTER(WHERE status='denied') FROM manager_capability_calls WHERE session_id=$1`, id).Scan(&authorized, &denied); err != nil || authorized != 3 || denied != 1 {
		t.Fatalf("audit=%d/%d err=%v", authorized, denied, err)
	}
	events := call("GET", base+"/sessions/"+id+"/events", nil, "")
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(credential)) {
		t.Fatal("plugin credential entered public events")
	}
	current, err := runtime.Packages.Get(ctx, w, actor, agent, packID)
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err = runtime.Packages.Update(ctx, w, actor, agent, packID, service.PackagePatch{Version: current.Version, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	unknown := create("capability-unknown", "cap-unknown")
	select {
	case <-hanging:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	call("POST", base+"/sessions/"+unknown+"/stop", map[string]any{}, "")
	wait(func() bool {
		var reason *string
		var active *string
		err := db.QueryRowContext(ctx, `SELECT block_reason,active_turn_id FROM manager_sessions WHERE id=$1`, unknown).Scan(&reason, &active)
		return err == nil && reason != nil && *reason == "remote_tool_outcome_unknown" && active == nil
	})
	call("POST", base+"/sessions/"+unknown+"/inputs", map[string]string{"text": "capability-auto"}, "cap-after-unknown")
	var turnCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM manager_turns WHERE session_id=$1`, unknown).Scan(&turnCount); err != nil || turnCount != 1 {
		t.Fatal("unknown remote outcome allowed another Turn", err)
	}
	_, archiveErr := (&manager.Service{Store: &managerrepo.Store{DB: db}}).ArchiveConversation(ctx, w, actor, c)
	assertArchiveConflict(t, archiveErr, "conversation_busy")
	call("POST", base+"/sessions/"+unknown+"/stop", map[string]any{}, "")
	waitConversationQuiescent(t, ctx, db, c, wait)
	call("POST", base+"/conversations/"+c+"/archive", map[string]any{}, "")
	var retained *string
	if err = db.QueryRowContext(ctx, `SELECT block_reason FROM manager_sessions WHERE id=$1`, unknown).Scan(&retained); err != nil || retained == nil || *retained != "remote_tool_outcome_unknown" {
		t.Fatalf("archive erased remote uncertainty: %v %v", retained, err)
	}
	// Fixture-only operator evidence: this server confirms its pending
	// operation stopped. Production does not guess this from cancellation.
	releaseOnce.Do(func() { close(release) })
	select {
	case <-hangingStopped:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_capability_calls SET status='failed',error_code='fixture_verified_stopped',finished_at=now() WHERE session_id=$1 AND status='authorized'`, unknown); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE manager_sessions SET block_reason=NULL WHERE id=$1 AND block_reason='remote_tool_outcome_unknown'`, unknown); err != nil {
		t.Fatal(err)
	}
	waitConversationQuiescent(t, ctx, db, c, wait)
	call("POST", base+"/conversations/"+c+"/archive", map[string]any{}, "")
	wait(func() bool {
		var state string
		err := db.QueryRowContext(ctx, `SELECT state FROM manager_conversation_sandboxes WHERE conversation_id=$1`, c).Scan(&state)
		return err == nil && state == "stopped"
	})
}

func runtimeCapabilityReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) bool {
	text := ""
	results := 0
	all := ""
	for _, m := range body.Messages {
		v := fmt.Sprint(m.Content)
		all += v
		if m.Role == "user" {
			text = v
			results = 0
		}
		if m.Role == "tool" {
			results++
		}
	}
	if !strings.Contains(text, "capability-") {
		return false
	}
	delta := map[string]any{"role": "assistant", "content": "capability complete"}
	reason := "stop"
	target := ""
	arguments := map[string]any{}
	if results == 0 && strings.Contains(text, "capability-auto") {
		var second bool
		for _, tool := range body.Tools {
			if strings.HasSuffix(tool.Function.Name, "__delete") {
				second = true
			}
		}
		if !second {
			t.Error("second account tool missing from the model request")
		}
	}
	if strings.Contains(text, "capability-unknown") {
		if results == 0 {
			for _, tool := range body.Tools {
				if strings.HasSuffix(tool.Function.Name, "__lookup") {
					target = tool.Function.Name
				}
			}
			arguments = map[string]any{"query": "hang"}
		}
	} else if strings.Contains(text, "capability-revoke") {
		if results < 2 {
			for _, tool := range body.Tools {
				if strings.HasSuffix(tool.Function.Name, "__lookup") {
					target = tool.Function.Name
				}
			}
			query := "normal"
			if results == 0 {
				query = "revoke"
			}
			arguments = map[string]any{"query": query}
		}
	} else if results < 4 {
		match := regexp.MustCompile(`<location>([^<]+/SKILL.md)</location>`).FindStringSubmatch(all)
		if len(match) != 2 {
			t.Error("authorized Skill metadata missing")
			w.WriteHeader(500)
			return true
		}
		dir := filepath.Dir(match[1])
		suffix := "__read"
		switch results {
		case 0:
			if strings.Contains(text, "capability-auto") && (strings.Contains(all, "body-v1-only") || strings.Contains(all, "body-v2-only")) {
				t.Error("automatic Skill body was eagerly injected")
			}
			if strings.Contains(text, "capability-explicit") && !strings.Contains(text, "body-v2-only") {
				t.Error("resumed task did not expand the upgraded Skill body")
			}
			arguments = map[string]any{"path": match[1]}
		case 1:
			arguments = map[string]any{"path": filepath.Join(dir, "references/value.txt")}
		case 2:
			suffix = "__bash_start"
			arguments = map[string]any{"command": "bash '" + filepath.Join(dir, "scripts/run.sh") + "'", "yield_time_ms": 1000}
		case 3:
			suffix = "__lookup"
			arguments = map[string]any{"query": "normal"}
		}
		for _, tool := range body.Tools {
			if strings.HasSuffix(tool.Function.Name, suffix) {
				target = tool.Function.Name
			}
		}
	}
	if target != "" {
		args, err := json.Marshal(arguments)
		if err != nil {
			t.Error(err)
		}
		delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("cap-%d", results), "type": "function", "function": map[string]any{"name": target, "arguments": string(args)}}}}
		reason = "tool_calls"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, d := range []map[string]any{delta, {}} {
		finish := any(nil)
		if len(d) == 0 {
			finish = reason
		}
		v, err := json.Marshal(map[string]any{"id": "cap-fixture", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": finish}}})
		if err != nil {
			t.Error(err)
			return true
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", v); err != nil {
			return true
		}
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return true
	}
	return true
}
