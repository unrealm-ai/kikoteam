package service

import "context"

const (
	TaskDetailCardPresentation = "task_detail_card"
	TaskDetailLinkPresentation = "task_detail_link"
)

type TaskDetailCard struct {
	Title string `json:"title"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

type TaskDetailDispatchReceipt struct {
	Status string `json:"status"`
}

type TaskDetailDispatcher interface {
	TaskDetailLinksEnabled(context.Context) (bool, error)
	QueueTaskDetailCard(context.Context, DelegationScope, string) (TaskDetailDispatchReceipt, error)
}

func (d *Delegation) taskDetailDispatch(ctx context.Context, scope DelegationScope, task string) (TaskDetailDispatchReceipt, error) {
	if scope.ReportOnly || scope.Channel == nil {
		return TaskDetailDispatchReceipt{}, Fail("user_authorization_required", 403, "只有当前渠道的普通用户对话可以发送任务详情卡片")
	}
	if !ValidID(task) {
		return TaskDetailDispatchReceipt{}, Fail("invalid_argument", 400, "task_id 无效")
	}
	dispatch, ok := d.Store.(TaskDetailDispatcher)
	if !ok {
		return TaskDetailDispatchReceipt{}, Fail("task_detail_unavailable", 503, "任务详情卡片暂不可用")
	}
	return dispatch.QueueTaskDetailCard(ctx, scope, task)
}
