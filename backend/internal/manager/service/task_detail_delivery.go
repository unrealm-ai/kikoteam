package service

import "context"

const (
	TaskDetailCardPresentation = "task_detail_card"
	TaskDetailLinkPresentation = "task_detail_link"
)

type TaskDetailCard struct {
	Description string `json:"description,omitempty"`
	Title       string `json:"title"`
	State       string `json:"state"`
	URL         string `json:"url,omitempty"`
}

type TaskDetailDispatchReceipt struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type TaskDetailDispatcher interface {
	TaskDetailLinksEnabled(context.Context) (bool, error)
	QueueTaskDetailCard(context.Context, DelegationScope, string, string) (TaskDetailDispatchReceipt, error)
}

func (d *Delegation) taskDetailDispatch(ctx context.Context, scope DelegationScope, task, description string) (TaskDetailDispatchReceipt, error) {
	if scope.Channel == nil {
		return TaskDetailDispatchReceipt{}, Fail("user_authorization_required", 403, "只能向当前有权限的渠道对话发送任务详情链接")
	}
	if !ValidID(task) {
		return TaskDetailDispatchReceipt{}, Fail("invalid_argument", 400, "task_id 无效")
	}
	dispatch, ok := d.Store.(TaskDetailDispatcher)
	if !ok {
		return TaskDetailDispatchReceipt{}, Fail("task_detail_unavailable", 503, "任务详情链接暂不可用")
	}
	return dispatch.QueueTaskDetailCard(ctx, scope, task, description)
}
