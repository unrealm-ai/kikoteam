package service

import (
	"fmt"
	"strings"
)

func (q *HumanQuestion) normalizeStructured() error {
	invalid := Fail("invalid_human_question", 400, "请填写有效的结构化问题、必答项、完成标准、拟执行动作和影响范围")
	if !ValidID(q.CandidateID) || q.RecipientID != "" && q.RecipientID != q.CandidateID ||
		!humanText(strings.TrimSpace(q.QuestionScope), 1000) || q.KnownFacts == nil || len(q.KnownFacts) > 20 ||
		len(q.Questions) < 1 || len(q.Questions) > 12 || len(q.CompletionCriteria) < 1 ||
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
		case "text", "choice", "boolean", "number":
		default:
			return invalid
		}
		seen[item.ID] = true
		line := strings.TrimSpace(item.Text)
		if len(q.Questions) > 1 {
			line = fmt.Sprintf("%d. %s", index+1, line)
		}
		lines = append(lines, line)
	}
	q.RecipientID, q.Question = q.CandidateID, strings.Join(lines, "\n")
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
	known := make(map[string]bool, len(q.Questions))
	for _, item := range q.Questions {
		known[item.ID] = true
	}
	seen := make(map[string]bool, len(a.Answers))
	for _, answer := range a.Answers {
		if !known[answer.QuestionID] || seen[answer.QuestionID] || !humanText(strings.TrimSpace(answer.Value), 2000) {
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
