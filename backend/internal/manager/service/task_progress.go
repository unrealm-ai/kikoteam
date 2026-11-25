package service

// TaskProgress is display-only. It must not drive admission or commands.
type TaskProgress struct {
	Phase        string                    `json:"phase"`
	Assignee     string                    `json:"assignee,omitempty"`
	Confirmation *TaskConfirmationProgress `json:"confirmation,omitempty"`
	Handoffs     []TaskHandoffProgress     `json:"handoffs,omitempty"`
	Delivery     []TaskDeliveryProgress    `json:"delivery,omitempty"`
}

type TaskConfirmationProgress struct {
	Recipient string `json:"recipient,omitempty"`
	Question  string `json:"question,omitempty"`
}

type TaskHandoffProgress struct {
	Assignee string `json:"assignee"`
	Phase    string `json:"phase"`
}

type TaskDeliveryProgress struct {
	Channel string `json:"channel"`
	Phase   string `json:"phase"`
}

// TaskProgressPhase combines observations without changing existing control
// states. The latest completed turn is not the current phase if work is queued.
func TaskProgressPhase(planning, lifecycle, execution, lastTurn string, preparing, stopped bool) string {
	switch {
	case planning == "planned":
		return "planned"
	case planning == "canceled":
		return "todo_canceled"
	case lifecycle == "archived":
		return "archived"
	case execution == "blocked" || execution == "stopping" || execution == "running":
		return execution
	case execution == "queued" && preparing:
		return "preparing_environment"
	case execution == "queued":
		return execution
	case execution != "idle":
		return "unknown"
	case stopped:
		return "stopped"
	}
	switch lastTurn {
	case "completed", "failed", "canceled", "interrupted":
		return lastTurn
	default:
		return "idle"
	}
}
