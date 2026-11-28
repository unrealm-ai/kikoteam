package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestManagerRuntimeInheritedImageHandoff(t *testing.T) {
	t.Setenv("KIKO_TEST_ARK_MODEL", "")
	testManagerRuntime(t, "handoff-images")
}

var imageHandoffTarget = regexp.MustCompile(`目标员工 ID=([0-9a-f-]{36})`)

// Drive only model decisions. Directory, handoff, generation, files and accounting
// all use the normal Engine/Pi tools and the authenticated application endpoints.
func handoffImageReply(t *testing.T, w http.ResponseWriter, req runtimeRequest) {
	var user string
	var results []string
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			user, results = fmt.Sprint(m.Content), nil
		case "tool":
			results = append(results, fmt.Sprint(m.Content))
		}
	}
	call := func(name string, args any) {
		t.Helper()
		for _, tool := range req.Tools {
			if tool.Function.Name == name || strings.HasSuffix(tool.Function.Name, "__"+name) {
				humanToolStream(t, w, []string{tool.Function.Name}, []any{args})
				return
			}
		}
		t.Errorf("tool missing: %s", name)
		humanTextStream(t, w, "缺少工具")
	}
	switch {
	case strings.Contains(user, "你收到一个事项处理任务"):
		switch len(results) {
		case 0:
			call("generate_image", map[string]string{"prompt": "水彩翠鸟商品海报", "request_key": "handoff-poster"})
		case 1:
			if path := generatedPath.FindString(results[0]); path != "" && strings.Contains(results[0], "settled") {
				call("artifact_publish", map[string]string{"path": path})
				return
			}
			t.Errorf("assignee image did not settle: %.600s", results[0])
			humanTextStream(t, w, "出图失败")
		case 2:
			call("update_task_progress", map[string]string{"base_plan_version": "0", "summary": imageProgressSummary})
		case 3:
			call("work_item_get_current", map[string]string{})
		case 4:
			var item modelWorkItem
			if err := json.Unmarshal([]byte(results[3]), &item); err != nil || item.Version == "" {
				t.Errorf("missing work item: %.400s", results[3])
			}
			call("work_item_submit_result", map[string]any{"expected_version": item.Version, "result": "海报已生成", "completion_evidence": "已发布 PNG 原图", "file_paths": []string{generatedPath.FindString(results[0])}})
		default:
			humanTextStream(t, w, "已提交海报")
		}
	case strings.Contains(user, "已收到交接事项的新成果"):
		if len(results) == 0 {
			call("work_item_get_current", map[string]string{})
		} else if len(results) == 1 {
			var item modelWorkItem
			if err := json.Unmarshal([]byte(results[0]), &item); err != nil || len(item.Files) != 1 {
				t.Errorf("expected a single submitted image: %.400s", results[0])
			}
			call("work_item_accept", map[string]string{"expected_version": item.Version, "completion_evidence": "已收到海报原图"})
		} else {
			humanTextStream(t, w, "电商设计师已默认继承官方文生图，无需挂载。已验收原图。")
		}
	default:
		target := imageHandoffTarget.FindStringSubmatch(user)
		if target == nil {
			humanTextStream(t, w, "完成")
			return
		}
		if len(results) == 0 {
			call("get_employee", map[string]string{"employee_id": target[1], "config_key": "capability_packages"})
			return
		}
		var read struct {
			Data struct {
				Defaults []struct{ Name, Status string } `json:"default_plugins"`
			}
		}
		if json.Unmarshal([]byte(results[0]), &read) != nil || len(read.Data.Defaults) != 1 ||
			read.Data.Defaults[0].Name != "官方文生图" || read.Data.Defaults[0].Status != "available" {
			t.Errorf("assistant could not see inherited image generation: %.600s", results[0])
			humanTextStream(t, w, "查询失败")
			return
		}
		call("work_item_handoff", map[string]string{"target_employee_id": target[1], "title": "商品海报", "goal": "生成一张 PNG", "criteria": "提交可下载原图", "summary": "官方文生图已默认继承，无需挂载。请生成并发布一张海报。"})
	}
}

func runImageHandoff(t *testing.T, db *sql.DB, call workRuntimeCall, base, source, target string) string {
	t.Helper()
	conv := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": source}, "")
	root := call("POST", base+"/sessions", map[string]any{"mode": "task", "conversation_id": conv["id"], "input": map[string]string{"text": "请帮电商设计师挂载官方文生图，若已继承直接说明，然后转派给它出一张海报。目标员工 ID=" + target}}, "handoff-image")
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		var child string
		err := db.QueryRowContext(t.Context(), `SELECT s.task_id FROM manager_work_items w JOIN manager_work_assignments a ON a.work_item_id=w.id
 JOIN manager_sessions s ON s.id=a.session_id WHERE w.root_session_id=$1 AND w.status='completed' AND s.agent_id=$2
 AND EXISTS (SELECT FROM manager_turns t WHERE t.session_id=w.root_session_id AND t.status='completed' AND t.result_summary LIKE '%已默认继承%')
 AND NOT EXISTS (SELECT FROM manager_session_events e WHERE e.session_id=w.root_session_id AND e.event->>'name' LIKE '%update_employee')`, root["id"], target).Scan(&child)
		if err == nil {
			return child
		}
		if err != sql.ErrNoRows || t.Failed() {
			t.Fatalf("handoff did not complete: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the assistant to accept the designer's image")
	return ""
}
