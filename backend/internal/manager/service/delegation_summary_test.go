package service

import (
	"strings"
	"testing"
)

func TestDelegatedTaskSummarySeparatesExecutionAndTurnResult(t *testing.T) {
	preview := strings.Repeat("字", delegatedResultPreviewLimit+1)
	turn := &Turn{ID: "previous-turn", Status: "completed", ResultSummary: &preview}
	background := 1
	for _, scenario := range []struct {
		execution Execution
		status    string
	}{
		{Execution{State: "queued"}, "queued"},
		{Execution{State: "idle", BackgroundProcessCount: &background}, "background_active"},
		{Execution{State: "idle", RequiredActions: []RequiredAction{{Type: "human_confirmation"}}}, "waiting_for_action"},
		{Execution{State: "stopping"}, "stopping"},
		{Execution{State: "idle"}, "idle"},
	} {
		result := summarizeDelegatedTask("started", "active", scenario.execution, turn)
		if result.Status != scenario.status || result.Result == nil || result.Result.TurnID != turn.ID ||
			result.Result.TurnStatus != "completed" || result.Result.Preview != strings.Repeat("字", delegatedResultPreviewLimit) || !result.Result.PreviewTruncated {
			t.Errorf("got %+v with result %+v, want %s and bounded previous result", result, result.Result, scenario.status)
		}
	}
	turn.HasUnknownToolResults = true
	result := summarizeDelegatedTask("started", "active", Execution{State: "blocked"}, turn)
	if result.Status != "blocked" || result.Result == nil || !result.Result.HasUnknownToolResults || result.Result.Preview != "" {
		t.Fatalf("unknown outcome surfaced a result preview: %+v", result)
	}
	turn.Status, turn.HasUnknownToolResults = "failed", false
	result = summarizeDelegatedTask("started", "active", Execution{State: "idle"}, turn)
	if result.Result == nil || result.Result.TurnStatus != "failed" || result.Result.Preview != "" {
		t.Fatalf("failed turn surfaced a result preview: %+v", result)
	}
}
