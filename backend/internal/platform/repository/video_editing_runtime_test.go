package repository

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPackagedVideoEditingEndToEnd(t *testing.T) { testManagerRuntime(t, "video") }

const videoInstruction = "取视频第 1 至 3 秒，结尾显示封面 1 秒，用配乐替换原声，交付 MP4。"

var videoInputs = []string{"uploads/原始 视频.mp4", "uploads/封面.png", "uploads/配乐.wav"}

// The fixture supplies model decisions. The catalog, Skill recipe, media
// processing, artifact publication and downloads all use production code.
func videoEditingReply(t *testing.T, w http.ResponseWriter, b runtimeRequest) {
	var user string
	var results []string
	for _, m := range b.Messages {
		switch m.Role {
		case "user":
			user, results = fmt.Sprint(m.Content), nil
		case "tool":
			results = append(results, fmt.Sprint(m.Content))
		}
	}
	if !strings.Contains(user, videoInstruction) {
		humanTextStream(t, w, "OK")
		return
	}
	var ids []string
	for _, match := range regexp.MustCompile(`attachment_id=([0-9a-f-]{36})\]`).FindAllStringSubmatch(user, -1) {
		ids = append(ids, match[1])
	}
	if len(ids) != len(videoInputs) {
		t.Errorf("expected three attachment references, got %d", len(ids))
		humanTextStream(t, w, "缺少素材")
		return
	}
	tool := func(name string, args any) { humanToolStream(t, w, []string{name}, []any{args}) }
	for _, entry := range b.Tools {
		if entry.Function.Name == "mcp__task_delegation__task_delegation_create_session" {
			if len(results) == 0 {
				tool(entry.Function.Name, map[string]any{"instruction": videoInstruction, "attachment_ids": ids})
			} else {
				humanTextStream(t, w, "已安排剪辑")
			}
			return
		}
	}
	switch len(results) {
	case 0:
		loc := regexp.MustCompile(`<location>([^<]+/video-editing/SKILL\.md)</location>`).FindStringSubmatch(runtimeSystemPrompt(b))
		if loc == nil {
			t.Error("video editing Skill missing from task prompt")
			humanTextStream(t, w, "缺少视频 Skill")
			return
		}
		names := []string{"mcp__builtin__read"}
		args := []any{map[string]string{"path": loc[1]}}
		for _, id := range ids {
			names = append(names, "mcp__input_attachments__attachment_get")
			args = append(args, map[string]string{"attachment_id": id})
		}
		humanToolStream(t, w, names, args)
	case 4:
		observed := strings.Join(results, "\n")
		for _, p := range videoInputs {
			if !strings.Contains(observed, p) {
				t.Error("attachment lookup did not return", p)
			}
		}
		_, block, found := strings.Cut(observed, "```bash\n")
		recipe, _, closed := strings.Cut(block, "\n```")
		if !found || !closed {
			t.Error("mounted Skill did not supply its FFmpeg example")
			humanTextStream(t, w, "无法读取合成示例")
			return
		}
		cmd := fmt.Sprintf("set -eu\nmkdir -p outputs\nclip=%q\ncover=%q\nmusic=%q\noutput=outputs/final.mp4\n%s", videoInputs[0], videoInputs[1], videoInputs[2], recipe)
		cmd += "\nffprobe -v error -show_streams \"$output\" >/dev/null\nffmpeg -v error -xerror -i \"$output\" -f null -"
		tool("mcp__builtin__bash_start", map[string]any{"command": cmd, "yield_time_ms": 20000})
	case 5:
		if !regexp.MustCompile(`"exit_code"\s*:\s*0\b`).MatchString(results[4]) {
			t.Errorf("render did not finish successfully: %.1200s", results[4])
			humanTextStream(t, w, "编码未完成")
			return
		}
		tool("mcp__work_items__artifact_publish", map[string]string{"path": "outputs/final.mp4"})
	default:
		humanTextStream(t, w, "成片 final.mp4 已完成")
	}
}

func mediaOutput(t *testing.T, binary string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v %s", binary, err, diagnostics.String())
	}
	return out
}

func uploadVideoInput(t *testing.T, endpoint, token, name string, data []byte) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", endpoint, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
	req.Header.Set("X-CSRF-Protection", "1")
	req.Header.Set("Idempotency-Key", service.NewID())
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var uploaded struct{ Path string }
	if err = json.NewDecoder(res.Body).Decode(&uploaded); err != nil || res.StatusCode != 201 || uploaded.Path != "uploads/"+name {
		t.Fatalf("upload %s: status=%d path=%q error=%v", name, res.StatusCode, uploaded.Path, err)
	}
}

func verifyVideoEditing(t *testing.T, db *sql.DB, runtime *service.RuntimeService, workspace, actor, model, base, server, token string,
	call func(string, string, any, string) map[string]any, wait func(func() bool)) {
	t.Helper()
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err = SyncCatalog(t.Context(), db, catalog, runtime.Skills.Assets); err != nil {
		t.Fatal(err)
	}
	templates := &service.TemplateService{Store: &TemplateStore{DB: db}, Assets: runtime.Skills.Assets}
	editor := createCatalogEmployee(t, t.Context(), templates, workspace, actor, model, "内容运营编辑", true)
	conv := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": editor.ID}, "")["id"].(string)
	attachments := base + "/conversations/" + conv + "/attachments"
	sources := [][]string{
		{"-f", "lavfi", "-i", "color=red:s=144x96:r=25:d=4,drawbox=c=blue:t=fill:enable='between(t,1,2.99)'", "-c:v", "libx264", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4"},
		{"-f", "lavfi", "-i", "color=yellow:s=64x96", "-frames:v", "1", "-c:v", "png", "-f", "image2pipe"},
		{"-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-f", "wav"},
	}
	var ids []string
	for i, args := range sources {
		args = append([]string{"-v", "error"}, args...)
		data := mediaOutput(t, "ffmpeg", append(args, "pipe:1")...)
		name := filepath.Base(videoInputs[i])
		id := call("POST", attachments, map[string]any{"name": name, "size": len(data)}, service.NewID())["id"].(string)
		uploadVideoInput(t, server+attachments+"/"+id+"/upload", token, name, data)
		ids = append(ids, id)
	}
	call("POST", base+"/sessions", map[string]any{"conversation_id": conv, "mode": "chat", "input": map[string]any{"text": videoInstruction, "attachment_ids": ids}}, service.NewID())
	var task, artifact string
	wait(func() bool {
		items, _ := call("GET", base+"/tasks?conversation_id="+conv, nil, "")["items"].([]any)
		if len(items) != 1 {
			return false
		}
		task = items[0].(map[string]any)["task_id"].(string)
		detail := call("GET", base+"/tasks/"+task, nil, "")
		published, _ := detail["artifacts"].([]any)
		turn, _ := detail["latest_turn"].(map[string]any)
		if turn["status"] != "completed" || len(published) != 1 {
			return false
		}
		item := published[0].(map[string]any)
		artifact = item["id"].(string)
		return item["name"] == "final.mp4"
	})
	endpoint := server + base + "/tasks/" + task + "/artifacts/" + artifact
	req, err := http.NewRequestWithContext(t.Context(), "GET", endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "kiko_session", Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	disposition, params, mimeErr := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil || mimeErr != nil || res.StatusCode != 200 || disposition != "attachment" || params["filename"] != "final.mp4" {
		t.Fatalf("artifact download: %s %s %v %v", res.Status, disposition, err, mimeErr)
	}
	output := filepath.Join(t.ArtifactDir(), "final.mp4")
	if err = os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
	verifyDownloadedEdit(t, output)
	t.Logf("Downloaded %d bytes from the published task artifact", len(data))
}

func verifyDownloadedEdit(t *testing.T, output string) {
	t.Helper()
	var probe struct {
		Format  struct{ Duration string }
		Streams []struct {
			Codec         string `json:"codec_name"`
			Width, Height int
		}
	}
	raw := mediaOutput(t, "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", output)
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || duration < 2.95 || duration > 3.1 || len(probe.Streams) != 2 {
		t.Fatalf("unexpected duration or stream count: %v", probe)
	}
	video := probe.Streams[0]
	if video.Codec != "h264" || video.Width != 640 || video.Height != 360 || probe.Streams[1].Codec != "aac" {
		t.Fatalf("unexpected streams: %v", probe.Streams)
	}
	// Decode one center pixel per second. The red lead-in and tail must be
	// trimmed, and the yellow portrait still must follow the two blue seconds.
	pixels := mediaOutput(t, "ffmpeg", "-v", "error", "-xerror", "-i", output, "-vf", "fps=1,crop=2:2,scale=1:1", "-an", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
	if len(pixels) != 9 || pixels[0] > 10 || pixels[2] < 240 || pixels[3] > 10 || pixels[5] < 240 || pixels[6] < 240 || pixels[7] < 240 || pixels[8] > 10 {
		t.Fatalf("incorrect clip or still: sampled RGB=%v", pixels)
	}
	pcm := mediaOutput(t, "ffmpeg", "-v", "error", "-xerror", "-i", output, "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "pipe:1")
	// 16,000 bytes per second. Sample the tone and the padded silence.
	if len(pcm) < 47000 || len(bytes.Trim(pcm[4000:8000], "\x00")) == 0 || len(bytes.Trim(pcm[32000:40000], "\x00")) > 0 {
		t.Fatal("download lost the supplied soundtrack or its padded ending")
	}
}
