package channel

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWecomControlReplyOnlyUsesCommittedTypedStatusAndCallback(t *testing.T) {
	for _, tc := range []struct{ status, text string }{
		{"verified", "验证成功。"},
		{"used", "验证码已使用，请返回工作台查看验证结果。"},
		{"expired", "验证码已过期，请返回工作台重新发起验证。"},
		{"revoked", "验证码已撤销，请返回工作台重新发起验证。"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			code := "123456"
			var attempts atomic.Int32
			f := setupWecomWithReceipt(t, true, false, func(v Inbound) inboundReceipt {
				r := inboundReceipt{Status: "accepted"}
				if v.MessageID == "control-msg" {
					r.ControlReply = tc.status
					if attempts.Add(1) != 1 {
						r.Status = "duplicate"
					}
				}
				return r
			})
			f.push(t, wecomIncoming("control-msg", "original-callback", code))
			assertControlReplyIngress(t, f, "control-msg")
			frame := nextWecomFrame(t, f, "aibot_respond_msg")
			var body struct {
				Type   string `json:"msgtype"`
				Stream struct {
					ID      string `json:"id"`
					Content string `json:"content"`
					Finish  bool   `json:"finish"`
				} `json:"stream"`
			}
			if frame.Headers.ReqID != "original-callback" || json.Unmarshal(frame.Body, &body) != nil ||
				body.Type != "stream" || !body.Stream.Finish || body.Stream.ID == "" || body.Stream.Content != tc.text ||
				strings.Contains(string(frame.Body), code) {
				t.Fatal("verification did not use the original callback and fixed final reply")
			}
			f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0})
			f.push(t, wecomIncoming("control-msg", "duplicate-callback", code))
			assertControlReplyIngress(t, f, "control-msg")
			assertControlReplySuppressed(t, f)
		})
	}
}

func TestWecomControlReplyRejectsUnknownStatusGroupAndMismatchedSocketContext(t *testing.T) {
	for _, mode := range []string{"unknown", "group", "callback_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			f := setupWecomWithReceipt(t, true, false, func(v Inbound) inboundReceipt {
				r := inboundReceipt{Status: "accepted"}
				if v.MessageID == "control-msg" && v.Text == "verify" {
					r.ControlReply = "verified"
					if mode == "unknown" {
						r.ControlReply = "server-supplied arbitrary text"
					}
				}
				return r
			})
			if mode == "callback_mismatch" {
				f.push(t, wecomIncoming("control-msg", "old-callback", "ordinary"))
				assertControlReplyIngress(t, f, "control-msg")
			}
			in := wecomIncoming("control-msg", "original-callback", "verify")
			if mode == "group" {
				body := in["body"].(map[string]any)
				body["chattype"] = "group"
				body["chatid"] = "group-chat"
			}
			f.push(t, in)
			assertControlReplyIngress(t, f, "control-msg")
			assertControlReplySuppressed(t, f)
		})
	}
}

func TestWecomControlReplyDoesNotRetryAfterMissingBusinessACK(t *testing.T) {
	var attempts atomic.Int32
	f := setupWecomWithReceipt(t, true, false, func(v Inbound) inboundReceipt {
		if attempts.Add(1) == 1 {
			return inboundReceipt{Status: "accepted", ControlReply: "verified"}
		}
		return inboundReceipt{Status: "duplicate", ControlReply: "verified"}
	})
	f.push(t, wecomIncoming("control-msg", "original-callback", "123456"))
	assertControlReplyIngress(t, f, "control-msg")
	nextWecomFrame(t, f, "aibot_respond_msg")
	// No business ACK. A later callback may be forwarded once the bounded
	// ACK wait ends, but neither the old nor the new callback may be sent to again.
	f.push(t, wecomIncoming("control-msg", "duplicate-callback", "123456"))
	select {
	case <-f.inbound:
	case <-time.After(5 * time.Second):
		t.Fatal("missing business ACK blocked later callbacks")
	}
	assertControlReplySuppressed(t, f)
}

func assertControlReplyIngress(t *testing.T, f *wecomFixture, id string) {
	t.Helper()
	select {
	case received := <-f.inbound:
		if received.MessageID != id {
			t.Fatalf("unexpected message %s", received.MessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not forward callback")
	}
}

func assertControlReplySuppressed(t *testing.T, f *wecomFixture) {
	t.Helper()
	// The ordinary event proves ingest consumed the preceding event; an
	// unintended control write cannot remain hidden behind a missing ACK.
	f.push(t, wecomIncoming("after-control", "after-callback", "ordinary"))
	assertControlReplyIngress(t, f, "after-control")
	for len(f.frames) > 0 {
		fm := <-f.frames
		if fm.Cmd != "aibot_subscribe" && fm.Cmd != "ping" {
			t.Fatalf("unexpected outbound frame %s", fm.Cmd)
		}
	}
}
