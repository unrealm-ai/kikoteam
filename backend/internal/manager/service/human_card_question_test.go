package service

import "testing"

func TestHumanCardStructuredAnswers(t *testing.T) {
	yes := true
	options := []HumanQuestionOption{{ID: "a", Label: "上午"}, {ID: "b", Label: "下午"}}
	q := HumanQuestion{Questions: []HumanQuestionItem{
		{ID: "one", AnswerType: "choice", Required: &yes, Options: options},
		{ID: "many", AnswerType: "multi_choice", Required: &yes, Options: options},
		{ID: "plain", AnswerType: "text", Required: &yes},
		{ID: "numeric", AnswerType: "number", Required: &yes},
		{ID: "legacy", AnswerType: "choice", Required: &yes},
	}}
	valid := []HumanAnswerItem{
		{QuestionID: "one", Value: "a"}, {QuestionID: "many", OptionIDs: []string{"a", "b"}},
		{QuestionID: "plain", Value: "请明天上线"}, {QuestionID: "numeric", Value: "22.5"},
		{QuestionID: "legacy", Value: "由原文字题决定"},
	}
	if missing, err := q.CheckAnswers(HumanAnswer{Decision: "information", Answers: valid}); err != nil || len(missing) != 0 {
		t.Fatalf("valid answers: missing=%v err=%v", missing, err)
	}
	for _, invalid := range []HumanAnswerItem{
		{QuestionID: "one", Value: "上午"}, {QuestionID: "many", Value: "a,b"}, {QuestionID: "many", OptionIDs: []string{"a", "a"}},
		{QuestionID: "many", OptionIDs: []string{"unknown"}}, {QuestionID: "numeric", Value: "NaN"}, {QuestionID: "numeric", Value: "0x1p1"},
	} {
		if _, err := q.CheckAnswers(HumanAnswer{Decision: "information", Answers: []HumanAnswerItem{invalid}}); err == nil {
			t.Errorf("accepted %+v", invalid)
		}
	}
}

func TestHumanCardPureDecisionAndLegacyText(t *testing.T) {
	id := "c5f36c88-91db-4dac-ae91-027c97f5bee1"
	yes, no := true, false
	base := HumanQuestion{CandidateID: id, RulesVersion: 1, QuestionScope: "上线", Goal: "是否上线？",
		Background: "变更已验收", KnownFacts: []HumanFact{}, Questions: []HumanQuestionItem{}, DecisionRequired: &yes,
		CompletionCriteria: []string{"收到决定"}, ProposedAction: "上线", ImpactScope: "灰度环境", IfNotConfirmed: "暂不变更"}
	if err := base.Validate(); err != nil || base.Question != "是否上线？" || base.AnswerType != "decision" {
		t.Fatalf("pure decision: %+v, %v", base, err)
	}
	if err := base.ValidateNewMode(); err != nil {
		t.Fatal(err)
	}
	base.DecisionRequired = &no
	if err := base.Validate(); err == nil {
		t.Fatal("accepted empty information form")
	}
	old := HumanQuestion{RecipientID: id, RulesVersion: 1, Question: "请回复延期原因。", AnswerType: "information"}
	if err := old.Validate(); err != nil {
		t.Fatal(err)
	}
	reply := HumanAnswer{Version: 1, Decision: "information", Text: "验证尚未完成"}
	if err := reply.Validate(old.AnswerType); err != nil {
		t.Fatal(err)
	}
}

func TestHumanCardEachFormTypeCanStandAlone(t *testing.T) {
	yes, no := true, false
	options := []HumanQuestionOption{{ID: "am", Label: "上午"}, {ID: "pm", Label: "下午"}}
	for _, test := range []struct {
		kind    string
		options []HumanQuestionOption
		value   string
		ids     []string
	}{
		{kind: "choice", options: options, value: "am"},
		{kind: "multi_choice", options: options, ids: []string{"am", "pm"}},
		{kind: "text", value: "灰度环境还没验证"},
		{kind: "number", value: "22"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			q := HumanQuestion{CandidateID: "c5f36c88-91db-4dac-ae91-027c97f5bee1", RulesVersion: 1,
				QuestionScope: "上线", Goal: "补充上线信息", Background: "评估灰度", KnownFacts: []HumanFact{},
				DecisionRequired: &no, Questions: []HumanQuestionItem{{ID: "q", Text: "请补充", Required: &yes, AnswerType: test.kind, Options: test.options}},
				CompletionCriteria: []string{"收到答复"}, ProposedAction: "收集数据", ImpactScope: "灰度", IfNotConfirmed: "不继续"}
			if err := q.Validate(); err != nil || q.AnswerType != "information" {
				t.Fatalf("question=%+v err=%v", q, err)
			}
			if err := q.ValidateNewMode(); err != nil {
				t.Fatal(err)
			}
			answer := HumanAnswer{Version: 1, Decision: "information", Answers: []HumanAnswerItem{{QuestionID: "q", Value: test.value, OptionIDs: test.ids}}}
			if err := answer.Validate(q.AnswerType); err != nil {
				t.Fatal(err)
			}
			if missing, err := q.CheckAnswers(answer); err != nil || len(missing) != 0 {
				t.Fatalf("missing=%v err=%v", missing, err)
			}
		})
	}
}

func TestModelAnswersFollowStoredType(t *testing.T) {
	options := []HumanQuestionOption{{ID: "1", Label: "一"}, {ID: "2", Label: "二"}}
	q := HumanQuestion{Questions: []HumanQuestionItem{
		{ID: "single", AnswerType: "choice", Options: options},
		{ID: "multi", AnswerType: "multi_choice", Options: options},
		{ID: "text", AnswerType: "text"},
		{ID: "number", AnswerType: "number"},
	}}
	items := []HumanModelAnswerItem{
		{QuestionID: "single", Values: []string{"1"}},
		{QuestionID: "multi", Values: []string{"1", "2"}},
		{QuestionID: "text", Values: []string{"实际说明"}},
		{QuestionID: "number", Values: []string{"42"}},
	}
	converted, err := q.ModelAnswers(items)
	if err != nil || converted[0].Value != "1" || len(converted[0].OptionIDs) != 0 ||
		len(converted[1].OptionIDs) != 2 || converted[1].Value != "" ||
		converted[2].Value != "实际说明" || converted[3].Value != "42" {
		t.Fatal("model answers were not normalized by stored type", converted, err)
	}
	if _, err := q.CheckAnswers(HumanAnswer{Decision: "information", Answers: converted}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []HumanModelAnswerItem{
		{QuestionID: "single", Values: []string{"1", "2"}},
		{QuestionID: "number", Values: []string{"not a number"}},
		{QuestionID: "multi", Values: []string{"1", "1"}},
		{QuestionID: "multi", Values: []string{"3"}},
		{QuestionID: "text", Values: nil},
		{QuestionID: "unknown", Values: []string{"1"}},
	} {
		converted, err := q.ModelAnswers([]HumanModelAnswerItem{invalid})
		if err == nil {
			_, err = q.CheckAnswers(HumanAnswer{Decision: "information", Answers: converted})
		}
		if err == nil {
			t.Fatal("invalid answer was accepted", invalid)
		}
	}
}
