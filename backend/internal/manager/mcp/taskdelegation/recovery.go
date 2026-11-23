package taskdelegation

import (
	"errors"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type recoveryHint struct {
	Action       string            `json:"action"`
	Outcome      string            `json:"outcome,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Arguments    map[string]string `json:"arguments,omitempty"`
	Instructions string            `json:"instructions"`
}

func failedToolResult(op, code, message string, args manager.DelegationArgs, err error) map[string]any {
	result := map[string]any{"error": map[string]string{"code": code, "message": message}}
	if op == "artifact_select" {
		var failure *manager.ArtifactSelectionError
		switch {
		case errors.As(err, &failure) && len(failure.Candidates) > 0:
			result["artifact_candidates"] = failure.Candidates
			result["recovery"] = &recoveryHint{Action: "retry_or_reply_text", Outcome: "not_applied", Instructions: "只在交付目标明确时，用以下候选中完整的 artifact_id 重试一次。否则继续文字回复，不声称文件已发送。"}
		case code == "artifact_forbidden" || code == "invalid_artifact_selection" || code == "resource_not_found":
			result["recovery"] = &recoveryHint{Action: "reply_text", Instructions: "没有可用于本次纠错的产物。继续文字回复，不猜 ID，不声称文件已发送。"}
		case code == "dependency_unavailable":
			result["recovery"] = &recoveryHint{Action: "reply_text", Outcome: "unknown", Instructions: "本次产物选择结果未确认。不要盲目重试或声称文件已发送，继续说明可确认的文字结果。"}
		}
	} else if hint := recoveryForTask(op, code, args, err); hint != nil {
		result["recovery"] = hint
	}
	return result
}

func recoveryForTask(op, code string, args manager.DelegationArgs, err error) *recoveryHint {
	if op != "send_message" && op != "stop_session" && op != "supplement_todo" && op != "cancel_todo" && op != "get_session" {
		return nil
	}
	if code == "resource_not_found" && op == "get_session" && manager.ValidID(args.SessionID) && args.TurnID != "" {
		return queryTask(args, "", "所查任务或执行轮次不可用。先去掉 turn_id 和 cursor 查这条任务，再按最新轮次核对；不要猜轮次。")
	}
	if code == "resource_not_found" || (code == "invalid_argument" && !manager.ValidID(args.SessionID)) {
		return &recoveryHint{Action: "query", Tool: "task_delegation_list_sessions",
			Instructions: "仅查当前对话的任务，用列表每项的 id 填 session_id，不要用 task_id 替代；多个匹配请用户确认，不猜最近一条。"}
	}
	if code == "task_not_started" {
		var canceled manager.CanceledTodoError
		if errors.As(err, &canceled) {
			return &recoveryHint{Action: "stop", Outcome: "not_applied", Instructions: "这是已取消且未启动的待办，不要补充、启动或再次停止。"}
		}
		if op == "send_message" {
			return queryTask(args, "not_applied", "这是未启动待办。先核实 planning_state 和 planning_version；仍未启动且用户明确要求补充时，用 supplement_todo。")
		}
		if op == "stop_session" {
			return queryTask(args, "not_applied", "没有运行中的任务。先核实待办状态；仅在用户明确要取消这条待办时使用 cancel_todo，不能把停止自动当成取消。")
		}
	}
	switch code {
	case "version_conflict", "session_revision_conflict":
		return queryTask(args, "not_applied", "先核对最新状态与回执。普通任务由 Manager 重新读取 input_revision；待办使用查询返回的 planning_version。只在原要求仍适用、未写入且无冲突时尝试一次，不改变请求内容。")
	case "already_started", "task_already_started_or_canceled":
		return queryTask(args, "not_applied", "先确认是已启动还是已取消。不要再次修改待办；如需对已启动任务采取不同操作，须确认用户确实授权该操作。")
	case "delegation_input_changed", "access_revoked", "session_archived", "user_authorization_required":
		return &recoveryHint{Action: "stop", Instructions: "不要重试、换会话或改用其他工具绕过。说明当前操作无法继续。"}
	case "dependency_unavailable":
		if op != "get_session" {
			return queryTask(args, "unknown", "写入结果未确认。先查当前任务状态或输入回执，不能确认未写入时不要重发、换请求键或新建任务；看不到回执不代表未写入。")
		}
	}
	return nil
}

func queryTask(args manager.DelegationArgs, outcome, instructions string) *recoveryHint {
	hint := &recoveryHint{Action: "query", Outcome: outcome, Tool: "task_delegation_get_session", Instructions: instructions}
	if manager.ValidID(args.SessionID) {
		hint.Arguments = map[string]string{"session_id": args.SessionID}
	}
	return hint
}
