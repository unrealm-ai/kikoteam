package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var projectTestPaths = []string{"outputs/地区　汇总.csv", "uploads/趋势图.png"}

// Exercise the provider-facing vision transport as well as the task's real read tool.
func projectModelReply(t *testing.T, w http.ResponseWriter, b runtimeRequest) bool {
	lastUser := ""
	task := false
	toolResults := 0
	for _, m := range b.Messages {
		if m.Role == "tool" {
			toolResults++
		}
		if m.Role == "user" {
			lastUser = fmt.Sprint(m.Content)
		}
	}
	raw, err := json.Marshal(b.Messages)
	if err != nil {
		t.Errorf("model request: %v", err)
		return false
	}
	for _, tool := range b.Tools {
		if strings.HasSuffix(tool.Function.Name, "__read") {
			task = true
		}
	}
	if task && bytes.Contains(raw, []byte("project-analysis")) {
		for _, p := range projectTestPaths {
			if !bytes.Contains(raw, []byte(p)) {
				t.Errorf("task prompt missing %s", p)
			}
		}
		if !bytes.Contains(raw, []byte("data:image/")) && !bytes.Contains(raw, []byte("region-proof-731")) {
			humanToolStream(t, w, []string{"mcp__builtin__read", "mcp__builtin__read"}, []any{map[string]string{"path": projectTestPaths[0]}, map[string]string{"path": projectTestPaths[1]}})
			return true
		}
		if !bytes.Contains(raw, []byte("region-proof-731")) || bytes.Contains(raw, []byte("wrong spreadsheet")) {
			t.Error("provider did not receive the selected spreadsheet")
		}
		_, encoded, ok := bytes.Cut(raw, []byte(";base64,"))
		encoded, _, _ = bytes.Cut(encoded, []byte{'"'})
		if !ok || len(encoded) > 5<<20 {
			t.Error("provider did not receive a bounded image")
		} else {
			decoded, _, e := image.Decode(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded)))
			if e != nil {
				t.Errorf("provider received an unreadable image: %v", e)
			} else if bounds := decoded.Bounds(); bounds.Dx() != 2000 || bounds.Dy() != 2000 {
				t.Errorf("unexpected provider image dimensions: %v", bounds)
			}
		}
		if toolResults == 2 {
			humanToolStream(t, w, []string{"mcp__work_items__artifact_publish"}, []any{map[string]string{"path": projectTestPaths[0]}})
			return true
		}
		humanTextStream(t, w, "两份文件均已读取")
		return true
	}
	if !task && strings.Contains(lastUser, "核对汇总表与趋势图") {
		if bytes.Contains(raw, []byte("data:image/")) || bytes.Contains(raw, []byte("region-proof-731")) {
			t.Error("attachment contents reached main chat")
		}
		if b.Messages[len(b.Messages)-1].Role == "tool" {
			humanTextStream(t, w, "已委派")
			return true
		}
		for _, p := range projectTestPaths {
			if !strings.Contains(lastUser, p) {
				t.Errorf("chat prompt missing %s", p)
			}
		}
		humanToolStream(t, w, []string{"mcp__task_delegation__task_delegation_create_session"}, []any{map[string]any{"instruction": "project-analysis", "attachment_paths": projectTestPaths}})
		return true
	}
	return false
}
func testRuntimeProjectFiles(t *testing.T, ctx context.Context, db *sql.DB, base, workspace, agent, root, server, token string, call func(string, string, any, string) map[string]any, wait func(func() bool)) {
	t.Helper()
	c := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": agent}, "")["id"].(string)
	project := filepath.Join(root, "workspaces", workspace, "projects", "conv-"+c)
	if e := os.MkdirAll(filepath.Join(project, "outputs"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(project, "outputs/地区 汇总.csv"), []byte("wrong spreadsheet"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(project, projectTestPaths[0]), []byte("region,total\nregion-proof-731,731\n"), 0600); e != nil {
		t.Fatal(e)
	}
	// The deterministic test profile supports image input.
	if _, e := db.ExecContext(ctx, `UPDATE platform_model_profile_versions SET vision_status='passed' WHERE workspace_id=$1`, workspace); e != nil {
		t.Fatal(e)
	}
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	f, e := mp.CreateFormFile("file", "趋势图.png")
	if e != nil {
		t.Fatal(e)
	}
	// An ordinary 9 MB PNG exceeds the former 8 MiB input limit. Its 3000 px
	// sides require Pi to downscale the model copy while preserving the upload.
	photo := image.NewGray(image.Rect(0, 0, 3000, 3000))
	if _, e = rand.NewChaCha8([32]byte{1}).Read(photo.Pix); e != nil {
		t.Fatal(e)
	}
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if e = encoder.Encode(f, photo); e != nil {
		t.Fatal(e)
	}
	if e = mp.Close(); e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequestWithContext(ctx, "POST", server+base+"/conversations/"+c+"/files", &buf)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Cookie", "kiko_session="+token)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set("Idempotency-Key", "upload-chart")
	req.Header.Set("X-CSRF-Protection", "1")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if e != nil || res.StatusCode != 201 || !bytes.Contains(data, []byte(projectTestPaths[1])) {
		t.Fatalf("upload: %s %s %v", res.Status, data, e)
	}
	info, e := os.Stat(filepath.Join(project, projectTestPaths[1]))
	if e != nil || info.Size() <= 8<<20 {
		t.Fatal("original image missing or unexpectedly small", info, e)
	}
	chat := call("POST", base+"/sessions", map[string]any{"conversation_id": c, "mode": "chat", "input": map[string]any{"text": "核对汇总表与趋势图", "attachment_paths": projectTestPaths}}, "first-files")["id"].(string)
	wait(func() bool {
		var ok bool
		e := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_session_events e JOIN manager_sessions s ON e.session_id=s.id WHERE s.conversation_id=$1 AND s.mode='task' AND e.event->>'type'='agent.message' AND e.event::text LIKE '%两份文件均已读取%')`, c).Scan(&ok)
		return e == nil && ok
	})
	wait(func() bool {
		var ok bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM manager_report_artifacts r JOIN manager_task_artifacts a ON a.id=r.artifact_id JOIN manager_turns t ON t.id=a.turn_id JOIN manager_sessions s ON s.id=t.session_id WHERE s.conversation_id=$1 AND a.path=$2)`, c, projectTestPaths[0]).Scan(&ok)
		return err == nil && ok
	})
	for _, mode := range []string{"chat", "task"} {
		var paths string
		if e = db.QueryRowContext(ctx, `SELECT (SELECT jsonb_agg(a->'path' ORDER BY n) FROM jsonb_array_elements(i.attachments) WITH ORDINALITY f(a,n))::text FROM manager_inputs i JOIN manager_sessions s ON s.initial_input_id=i.id WHERE s.conversation_id=$1 AND s.mode=$2`, c, mode).Scan(&paths); e != nil {
			t.Fatal(e)
		}
		var v []string
		if e = json.Unmarshal([]byte(paths), &v); e != nil || fmt.Sprint(v) != fmt.Sprint(projectTestPaths) {
			t.Fatal(mode, paths, e)
		}
	}
	if out := call("GET", base+"/sessions/"+chat+"/events?type=user.message", nil, ""); !strings.Contains(fmt.Sprint(out), projectTestPaths[1]) {
		t.Fatal("history lost image")
	}
}
