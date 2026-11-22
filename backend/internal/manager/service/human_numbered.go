package service

import (
	"regexp"
	"strconv"
	"strings"
)

var humanNumberedLine = regexp.MustCompile(`^([1-9][0-9]?)[.、)）]\s*(\S.*)$`)
var humanOrdinalAnswer = regexp.MustCompile(`^第([1-9][0-9]?|[一二三四五六七八九十])(?:(?:个问题|个|题)\s*[:：]?\s*|[:：]\s*)(\S.*)$`)

// NumberedHumanQuestions opts in only when every question line is explicitly
// and consecutively numbered. An incidental number in prose is never a field.
func NumberedHumanQuestions(question string) []string {
	lines := strings.Split(strings.TrimSpace(question), "\n")
	if len(lines) < 2 || len(lines) > 10 {
		return nil
	}
	questions := make([]string, 0, len(lines))
	for i, line := range lines {
		m := humanNumberedLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || m[1] != strconv.Itoa(i+1) {
			return nil
		}
		questions = append(questions, m[2])
	}
	return questions
}

func ParseNumberedHumanAnswers(text string, count int) (map[int]string, bool) {
	text = strings.ReplaceAll(text, "；", "\n")
	text = strings.ReplaceAll(text, ";", "\n")
	text = strings.ReplaceAll(text, "，第", "\n第")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 || len(lines) > count {
		return nil, false
	}
	out := make(map[int]string, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		parts := humanNumberedLine.FindStringSubmatch(line)
		if parts == nil {
			parts = humanOrdinalAnswer.FindStringSubmatch(line)
		}
		if parts == nil {
			return nil, false
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			for i, numeral := range []rune("一二三四五六七八九十") {
				if parts[1] == string(numeral) {
					n = i + 1
					break
				}
			}
		}
		value := strings.TrimSpace(parts[2])
		if n < 1 || n > count || out[n] != "" || !humanText(value, min(1000, (4000-8*count)/count)) {
			return nil, false
		}
		out[n] = value
	}
	return out, true
}
