package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestHumanNoticeText(t *testing.T) {
	const code = "H-123456789ABC"
	required := true
	for _, tc := range []struct {
		name string
		q    service.HumanQuestion
		want string
	}{
		{
			name: "decision with required fields and conditions",
			q: service.HumanQuestion{
				AnswerType: "decision", Background: "订单已取消。", ProposedAction: "原路退款。", ImpactScope: "仅此订单。",
				Questions: []service.HumanQuestionItem{
					{Text: "退款金额是多少？", Required: &required, Constraints: []string{"含税", "含税", "YES"}},
					{Text: "到账日期（北京时间）？", Constraints: []string{"北京时间"}},
				},
			},
			want: "人工确认 H-123456789ABC\n订单已取消。\n1. 退款金额是多少？（必答，含税）\n2. 到账日期（北京时间）？\n同意后：原路退款。\n影响范围：仅此订单。\n如同意，请同时回答必答项，并明确表示同意。也可以直接拒绝或说明需要调整的地方。",
		},
		{
			name: "single information question",
			q: service.HumanQuestion{
				AnswerType: "information", Background: "请提供日期。", ProposedAction: "更新排期。",
				Questions: []service.HumanQuestionItem{{Text: "请提供日期。", Required: &required, Constraints: []string{"北京时间"}}},
			},
			want: "人工确认 H-123456789ABC\n请提供日期。（必答，北京时间）\n收到答复后：更新排期。\n请直接回复答案，或告诉我需要补充什么。",
		},
		{
			name: "legacy single decision",
			q:    service.HumanQuestion{AnswerType: "decision", Question: "同意退款吗？"},
			want: "人工确认 H-123456789ABC\n同意退款吗？\n请直接回复同意、拒绝，或说明需要调整的地方。",
		},
		{
			name: "legacy numbered decision",
			q:    service.HumanQuestion{AnswerType: "decision", Question: "1. 金额？\n2. 日期？"},
			want: "人工确认 H-123456789ABC\n1. 金额？\n2. 日期？\n如同意，请先逐项回复，再单独明确同意。也可以直接拒绝或说明需要调整的地方。",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanNoticeText(code, tc.q); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
