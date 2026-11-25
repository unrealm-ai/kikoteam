package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (q *HumanQuestion) normalizeStructured() error {
	invalid := Fail("invalid_human_question", 400, "请填写有效的结构化问题、必答项、完成标准、拟执行动作和影响范围")
	if !ValidID(q.CandidateID) || q.RecipientID != "" && q.RecipientID != q.CandidateID ||
		!humanText(strings.TrimSpace(q.QuestionScope), 1000) || q.KnownFacts == nil || len(q.KnownFacts) > 20 ||
		(len(q.Questions) == 0 && q.DecisionRequired != nil && !*q.DecisionRequired) || len(q.Questions) > 12 || len(q.CompletionCriteria) < 1 ||
		!humanQuestionTexts(q.CompletionCriteria, 16) {
		return invalid
	}
	for _, value := range []string{q.Background, q.Goal, q.ProposedAction, q.ImpactScope, q.IfNotConfirmed} {
		if !humanText(strings.TrimSpace(value), 2000) {
			return invalid
		}
	}
	for _, fact := range q.KnownFacts {
		if !humanText(strings.TrimSpace(fact.Text), 2000) || !humanQuestionTexts(fact.EvidenceRefs, 16) {
			return invalid
		}
	}
	seen := make(map[string]bool, len(q.Questions))
	lines := make([]string, 0, len(q.Questions))
	for index, item := range q.Questions {
		if item.Required == nil || !humanText(strings.TrimSpace(item.ID), 80) ||
			!humanText(strings.TrimSpace(item.Text), 2000) || seen[item.ID] ||
			!humanQuestionTexts(item.Constraints, 16) {
			return invalid
		}
		switch item.AnswerType {
		case "text", "choice", "boolean", "number", "multi_choice":
		default:
			return invalid
		}
		if !validHumanQuestionOptions(item) {
			return Fail("invalid_human_options", 400, "选项须有不重复的 ID 和名称，多选须提供选项")
		}
		seen[item.ID] = true
		line := strings.TrimSpace(item.Text)
		if len(q.Questions) > 1 {
			line = fmt.Sprintf("%d. %s", index+1, line)
		}
		lines = append(lines, line)
	}
	q.RecipientID, q.Question = q.CandidateID, strings.Join(lines, "\n")
	if len(lines) == 0 {
		q.Question = strings.TrimSpace(q.Goal)
	}
	q.AnswerType = "decision"
	if q.DecisionRequired != nil && !*q.DecisionRequired {
		q.AnswerType = "information"
	}
	return nil
}

func humanQuestionTexts(values []string, maxCount int) bool {
	if len(values) > maxCount {
		return false
	}
	for _, value := range values {
		if !humanText(strings.TrimSpace(value), 2000) {
			return false
		}
	}
	return true
}

// Structured answers are mapped to question IDs by the interaction agent or
// the form. Required fields and their identities are checked against the stored question.
func (q HumanQuestion) CheckAnswers(a HumanAnswer) ([]string, error) {
	if len(a.Answers) > len(q.Questions) {
		return nil, Fail("invalid_human_answers", 400, "答复包含未知或重复的小题")
	}
	known := make(map[string]HumanQuestionItem, len(q.Questions))
	for _, item := range q.Questions {
		known[item.ID] = item
	}
	seen := make(map[string]bool, len(a.Answers))
	for _, answer := range a.Answers {
		item, exists := known[answer.QuestionID]
		if !exists || seen[answer.QuestionID] || !validHumanAnswerValue(item, answer) {
			return nil, Fail("invalid_human_answers", 400, "答复包含未知、重复或无效的小题答案")
		}
		seen[answer.QuestionID] = true
	}
	var missing []string
	if a.Decision == "approve" || a.Decision == "information" {
		for _, item := range q.Questions {
			if item.Required != nil && *item.Required && !seen[item.ID] {
				missing = append(missing, item.ID)
			}
		}
	}
	return missing, nil
}

func validHumanQuestionOptions(item HumanQuestionItem) bool {
	if len(item.Options) == 0 {
		return item.AnswerType != "multi_choice"
	}
	if len(item.Options) > 20 || (item.AnswerType != "choice" && item.AnswerType != "multi_choice") {
		return false
	}
	ids, labels := map[string]bool{}, map[string]bool{}
	for _, opt := range item.Options {
		label := strings.TrimSpace(opt.Label)
		if !humanOptionID(opt.ID) || !humanText(label, 200) || ids[opt.ID] || labels[strings.ToLower(label)] {
			return false
		}
		ids[opt.ID], labels[strings.ToLower(label)] = true, true
	}
	return true
}

func humanOptionID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, ch := range id {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func validHumanAnswerValue(item HumanQuestionItem, answer HumanAnswerItem) bool {
	value := strings.TrimSpace(answer.Value)
	if item.AnswerType == "multi_choice" {
		if value != "" || len(answer.OptionIDs) == 0 || len(answer.OptionIDs) > len(item.Options) {
			return false
		}
		seen := map[string]bool{}
		for _, id := range answer.OptionIDs {
			if seen[id] || !hasHumanOption(item.Options, id) {
				return false
			}
			seen[id] = true
		}
		return true
	}
	if len(answer.OptionIDs) != 0 || !humanText(value, 2000) {
		return false
	}
	if item.AnswerType == "choice" && len(item.Options) != 0 {
		return hasHumanOption(item.Options, value)
	}
	if item.AnswerType == "number" {
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && !strings.ContainsAny(value, "xXpP_")
	}
	return true
}

func hasHumanOption(options []HumanQuestionOption, id string) bool {
	for _, opt := range options {
		if id == opt.ID {
			return true
		}
	}
	return false
}
