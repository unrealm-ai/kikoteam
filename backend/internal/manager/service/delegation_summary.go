package service

import "strings"

const delegatedResultPreviewLimit = 1200

// DelegatedTaskSummary separates the current execution state from the selected
// Turn, which may be an older Turn requested explicitly or through pagination.
type DelegatedTaskSummary struct {
	Status     string               `json:"status"`
	StatusText string               `json:"status_text"`
	Result     *delegatedTurnResult `json:"result"`
}

type delegatedTurnResult struct {
	TurnID                string `json:"turn_id"`
	TurnStatus            string `json:"turn_status"`
	Preview               string `json:"preview,omitempty"`
	PreviewTruncated      bool   `json:"preview_truncated,omitempty"`
	HasUnknownToolResults bool   `json:"has_unknown_tool_results,omitempty"`
}

func summarizeDelegatedTask(planningState, lifecycle string, execution Execution, turn *Turn) DelegatedTaskSummary {
	status, statusText := delegatedTaskStatus(planningState, lifecycle, execution)
	out := DelegatedTaskSummary{Status: status, StatusText: statusText}
	if turn == nil {
		return out
	}
	result := &delegatedTurnResult{TurnID: turn.ID, TurnStatus: turn.Status, HasUnknownToolResults: turn.HasUnknownToolResults}
	if turn.Status == "completed" && !turn.HasUnknownToolResults && turn.ResultSummary != nil {
		preview := strings.TrimSpace(*turn.ResultSummary)
		chars := []rune(preview)
		if len(chars) > delegatedResultPreviewLimit {
			preview = string(chars[:delegatedResultPreviewLimit])
			result.PreviewTruncated = true
		}
		result.Preview = preview
	}
	out.Result = result
	return out
}

func delegatedTaskStatus(planningState, lifecycle string, execution Execution) (string, string) {
	switch {
	case planningState == "planned":
		return "planned", "待办已保存，尚未启动。"
	case planningState == "canceled":
		return "todo_canceled", "未启动的待办已取消。"
	case lifecycle == "archived":
		return "archived", "任务会话已归档。"
	case execution.State == "blocked":
		return "blocked", "执行状态受阻，请查看阻塞原因。"
	case execution.State == "stopping":
		return "stopping", "停止已受理，正在等待停稳确认。"
	case execution.State == "running":
		return "running", "当前有一轮正在执行。"
	case len(execution.RequiredActions) > 0:
		return "waiting_for_action", "有待处理的人工确认或事项，请查看待处理动作。"
	case execution.State == "queued":
		return "queued", "有输入正在排队等待执行。"
	case execution.BackgroundProcessCount != nil && *execution.BackgroundProcessCount > 0:
		return "background_active", "仍有后台进程未确认结束。"
	case execution.State == "idle":
		return "idle", "当前没有活跃执行或待处理输入。"
	default:
		return "unknown", "当前执行状态暂未确认。"
	}
}
