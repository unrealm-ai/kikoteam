package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChannelReplyTextTerminalStatus(t *testing.T) {
	for _, tc := range []struct{ input, turn, want string }{
		{"processed", "failed", "本次处理未成功完成，请稍后重试。"},
		{"processed", "interrupted", "本次处理未成功完成，请稍后重试。"},
		{"canceled", "", "本次处理已取消，未完成。"},
		{"processed", "canceled", "本次处理已取消，未完成。"},
		{"rejected", "", "当前权限或员工配置已变更，本条消息未执行。"},
		{"processed", "completed", "正在处理\n最终答复"},
	} {
		t.Run(tc.input+"_"+tc.turn, func(t *testing.T) {
			if got := channelReplyText(tc.input, tc.turn, []string{"正在处理", "最终答复"}); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := channelReplyText("processed", "completed", nil); got != "本次处理已结束，没有可发送的文字回复。" {
		t.Fatal(got)
	}
}

func TestChannelReplyContentFormat(t *testing.T) {
	for _, tc := range []struct{ provider, turn, body, want string }{
		{"feishu", "completed", "**完成** 🎉", "markdown"},
		{"dingtalk", "completed", "**完成** 🎉", "markdown"},
		{"wecom", "completed", "**完成** 🎉", "text"},
		{"feishu", "completed", "![截图](img_key)", "text"},
		{"dingtalk", "completed", `<IMG src="https://example.test/image">`, "text"},
		{"feishu", "completed", "", "text"},
		{"feishu", "failed", "中间输出", "text"},
	} {
		if text, got := channelReplyContent(tc.provider, "processed", tc.turn, []string{tc.body}); got != tc.want || tc.turn == "completed" && tc.body != "" && text != tc.body {
			t.Fatalf("%+v: got %q in %q", tc, text, got)
		}
	}
}

func TestDeliveryTargetDigestIncludesMarkdown(t *testing.T) {
	d := service.ChannelDelivery{ID: service.NewID(), Text: "**完成** 🎉"}
	legacy := deliveryTargetDigest(d)
	d.Format = "text"
	if deliveryTargetDigest(d) != legacy {
		t.Fatal("explicit text changed the legacy digest")
	}
	d.Format = "markdown"
	if deliveryTargetDigest(d) == legacy {
		t.Fatal("markdown and text share an authorization digest")
	}
}
