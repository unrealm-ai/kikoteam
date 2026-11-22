package service

import "testing"

func TestHumanDMVersionedCodes(t *testing.T) {
	id := NewID()
	first, second := HumanShortCode(id, 1), HumanShortCode(id, 2)
	if first == second || !IsHumanCode(first) || !IsHumanCode(second) || IsHumanCode("H-NotARealCode") {
		t.Fatal(first, second)
	}
}

func TestNumberedHumanAnswers(t *testing.T) {
	questions := NumberedHumanQuestions("1. 金额？\n2. 日期？")
	if len(questions) != 2 || NumberedHumanQuestions("说明\n1. 金额") != nil {
		t.Fatal(questions)
	}
	for _, tc := range []struct {
		text string
		ok   bool
		size int
	}{
		{"第一个 500 元，第二个周五", true, 2},
		{"1. 500\n2、周五", true, 2},
		{"第2个问题：周五", true, 1},
		{"第3个 500", false, 0},
		{"第一个 500；第一个 600", false, 0},
		{"我同意", false, 0},
		{"第一批订单先发", false, 0},
	} {
		got, ok := ParseNumberedHumanAnswers(tc.text, 2)
		if ok != tc.ok || len(got) != tc.size {
			t.Errorf("%q: %v, %v", tc.text, got, ok)
		}
	}
}
