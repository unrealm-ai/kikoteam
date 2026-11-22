package service

import "context"

type TaskResult struct {
	TurnID  string `json:"turn_id,omitempty"`
	InputID string `json:"input_id,omitempty"`
}
type TaskReport struct {
	ID            string  `json:"report_id"`
	Status        string  `json:"status"`
	TargetInputID *string `json:"target_input_id"`
	Reason        *string `json:"reason"`
}
type TaskReportStore interface {
	ReportTaskResult(context.Context, TaskResult) (TaskReport, error)
	ReconcileTaskReports(context.Context, int) (int, error)
}
type TaskReports struct{ Store TaskReportStore }

func (s TaskReports) Report(ctx context.Context, r TaskResult) (TaskReport, error) {
	if (r.TurnID == "") == (r.InputID == "") || r.TurnID != "" && !ValidID(r.TurnID) || r.InputID != "" && !ValidID(r.InputID) {
		return TaskReport{}, Fail("invalid_argument", 400, "需要唯一的轮次或未执行输入身份")
	}
	return s.Store.ReportTaskResult(ctx, r)
}
func (s TaskReports) Reconcile(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, Fail("invalid_argument", 400, "limit 必须在 1 到 100 之间")
	}
	return s.Store.ReconcileTaskReports(ctx, limit)
}
