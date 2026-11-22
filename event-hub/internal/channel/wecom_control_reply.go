package channel

import (
	"context"
	"time"
)

// A control reply is a one-shot response on the authenticated callback socket.
// It does not enter the outbox, and loss of the socket or business ACK is not
// grounds to retry. Only Backend's committed typed disposition can select text.
func (s *wecomSession) replyToControl(ctx context.Context, backend *Backend, v Inbound, reply wecomReply, receipt inboundReceipt) {
	if receipt.Status != "accepted" || !v.ControlEligible || v.ChatType != "direct" || v.ThreadID != "" ||
		!validWecomID(reply.RequestID) || currentWecomSession(s.binding) != s {
		return
	}
	text := controlReplyText(receipt.ControlReply)
	if text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	got := s.sendBound(ctx, Delivery{
		BindingID: s.binding.ID, Generation: s.binding.Generation,
		ChatID: v.ChatID, ChatType: "direct", ReplyToMessageID: v.MessageID, Text: text,
		BeforeDispatch: func() error {
			if currentWecomSession(s.binding) != s {
				return errWecomNotDispatched
			}
			return nil
		},
	}, reply.RequestID)
	backend.traceInbound(ctx, "channel.control_reply.result", "wecom", v, got.Status)
}

func controlReplyText(status string) string {
	switch status {
	case "verified":
		return "验证成功。"
	case "used":
		return "验证码已使用，请返回工作台查看验证结果。"
	case "expired":
		return "验证码已过期，请返回工作台重新发起验证。"
	case "revoked":
		return "验证码已撤销，请返回工作台重新发起验证。"
	default:
		return ""
	}
}
