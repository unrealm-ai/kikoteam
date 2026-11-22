// Package trigger is the single matching implementation used by previews,
// Manager admission and EventHub channel filters. It has no I/O or side effects.
package trigger

import (
	"errors"
	"regexp"
	"strings"
)

type Matcher struct {
	words      []string
	expression *regexp.Regexp
}

func Compile(kind, pattern string) (*Matcher, error) {
	if pattern == "" || len(pattern) > 1024 {
		return nil, errors.New("invalid trigger pattern length")
	}
	m := &Matcher{}
	switch kind {
	case "keyword":
		for _, part := range strings.FieldsFunc(pattern, func(r rune) bool { return r == ',' || r == '，' }) {
			word := strings.TrimSpace(part)
			if word != "" {
				m.words = append(m.words, word)
			}
		}
		if len(m.words) == 0 {
			return nil, errors.New("keyword must contain a nonempty word")
		}
	case "regex":
		// Slash-delimited JS patterns are not Go RE2 syntax. Refuse instead of silently matching slashes.
		if strings.HasPrefix(pattern, "/") && strings.LastIndex(pattern, "/") > 0 {
			return nil, errors.New("use Go RE2 syntax without /pattern/flags")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, errors.New("unsupported Go RE2 expression")
		}
		m.expression = re
	default:
		return nil, errors.New("invalid trigger type")
	}
	return m, nil
}
func (m *Matcher) Match(text string) (string, bool) {
	if m.expression != nil {
		loc := m.expression.FindStringIndex(text)
		if loc != nil {
			return text[loc[0]:loc[1]], true
		}
		return "", false
	}
	lower := strings.ToLower(text)
	for _, word := range m.words {
		if strings.Contains(lower, strings.ToLower(word)) {
			return word, true
		}
	}
	return "", false
}
func ValidateText(text string) error {
	if len(text) > 65536 {
		return errors.New("message too large")
	}
	return nil
}
