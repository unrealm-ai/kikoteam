package repository

import (
	"slices"
	"strings"
	"testing"
)

func TestNoticeSummaryPreservesMarkdownAndFallbacks(t *testing.T) {
	const label = "[系统消息]本次执行已结束（不代表整项任务完成）"
	for _, tc := range []struct {
		name, status, input, want string
		markdown                  bool
	}{
		{"layout", "completed", "**变更**\r\n\r\n- 第一项\r\n  - `main`", label + "\n**变更**\n\n- 第一项\n  - `main`", true},
		{"image", "completed", "![图](key)", label + "\n![图](key)", false},
		{"empty", "completed", " \n<think>private</think> ", label, false},
		{"failed", "failed", "**中间结果**", "[系统消息]本次执行未正常完成（failed），请在任务记录查看。", false},
		{"long line", "completed", strings.Repeat("好", 1001), label + "\n" + strings.Repeat("好", 1000) + "…", true},
		{"long list", "completed", strings.Repeat("好", 980) + "\n- **" + strings.Repeat("好", 40) + "**", label + "\n" + strings.Repeat("好", 980) + "…", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, providers := noticeSummary(tc.status, tc.input)
			var wantProviders []string
			if tc.markdown {
				wantProviders = []string{"feishu", "dingtalk"}
			}
			if body != tc.want || !slices.Equal(providers, wantProviders) {
				t.Fatalf("summary = %q, markdown providers = %v", body, providers)
			}
		})
	}
}
