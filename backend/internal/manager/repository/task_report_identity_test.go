package repository

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestTaskReportModelContextUsesTaskID(t *testing.T) {
	const task = "00000000-0000-4000-8000-000000000001"
	report := taskReportText(reportSource{task: task, status: "completed", text: "fixture result"}, service.TaskResult{})
	_, body, ok := strings.Cut(report, "Turn 结束不证明整个业务任务完成。\n")
	if !ok {
		t.Fatal("missing trusted result envelope")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	if _, exists := data["session_id"]; exists || data["task_id"] != task || data["result"] != "fixture result" {
		t.Fatalf("unexpected model report payload: %v", data)
	}
}
