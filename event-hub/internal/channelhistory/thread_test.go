package channelhistory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func threadMessage(id, at string) wireMessage {
	m := message(id, at)
	m.ThreadID = "thread"
	return m
}
func threadJob() Job { j := job(); j.ThreadID = "thread"; return j }

func TestThreadHistoryPaginationAndExactAnchors(t *testing.T) {
	j := threadJob()
	j.PreviousTriggerID = "prior"
	j.PreviousTriggerTimeMS = 9997
	calls, checks := 0, 0
	r := Reader{Authorize: func(_ context.Context, got Job) error {
		checks++
		if got != j {
			t.Fatal("scope changed")
		}
		return nil
	}, Get: func(_ context.Context, path string) ([]byte, error) {
		calls++
		u, err := url.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("container_id_type") != "thread" || q.Get("container_id") != "thread" || q.Has("start_time") || q.Has("end_time") || q.Get("sort_type") != "ByCreateTimeDesc" {
			t.Fatal(path)
		}
		switch calls {
		case 1:
			return page([]wireMessage{threadMessage("later", "11000"), threadMessage("same-time-later", "10000")}, true, "page2"), nil
		case 2:
			if q.Get("page_token") != "page2" {
				t.Fatal(path)
			}
			return page([]wireMessage{threadMessage("anchor", "10000"), threadMessage("a", "9999")}, true, "page3"), nil
		default:
			if q.Get("page_token") != "page3" {
				t.Fatal(path)
			}
			return page([]wireMessage{threadMessage("a", "9999"), threadMessage("b", "9998"), threadMessage("prior", "9997"), threadMessage("older", "9996")}, false, ""), nil
		}
	}}
	got, err := r.Fetch(context.Background(), j)
	if err != nil || len(got.Messages) != 2 || got.Messages[0].ID != "b" || got.Messages[1].ID != "a" || got.Truncated || calls != 3 || checks != 4 {
		t.Fatalf("got=%+v err=%v calls=%d checks=%d", got, err, calls, checks)
	}
}

func TestThreadHistoryRejectsUnprovenContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []wireMessage
		prior bool
	}{
		{"missing current", []wireMessage{threadMessage("other", "9999")}, false},
		{"missing previous", []wireMessage{threadMessage("anchor", "10000"), threadMessage("between", "9999")}, true},
		{"wrong thread after anchor", []wireMessage{threadMessage("anchor", "10000"), message("other", "9999")}, false},
		{"wrong thread anchor", []wireMessage{message("anchor", "10000")}, false},
		{"out of order", []wireMessage{threadMessage("anchor", "10000"), threadMessage("older", "9998"), threadMessage("newer", "9999")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := threadJob()
			if tc.prior {
				j.PreviousTriggerID = "prior"
				j.PreviousTriggerTimeMS = 9998
			}
			r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) { return page(tc.items, false, ""), nil }}
			got, err := r.Fetch(context.Background(), j)
			if err == nil || len(got.Messages) != 0 || got.Digest != "" {
				t.Fatalf("leaked unproven context: %+v %v", got, err)
			}
		})
	}
}

func TestThreadHistoryBoundsAndFailures(t *testing.T) {
	for _, mode := range []string{"truncated", "same_time", "repeated_cursor", "page_budget", "revoked", "provider_error", "wrong_chat"} {
		t.Run(mode, func(t *testing.T) {
			j := threadJob()
			calls := 0
			checks := 0
			r := Reader{Authorize: func(context.Context, Job) error {
				checks++
				if mode == "revoked" && checks > 1 {
					return errors.New("revoked")
				}
				return nil
			}, Get: func(_ context.Context, path string) ([]byte, error) {
				calls++
				u, err := url.Parse(path)
				if err != nil {
					t.Fatal(err)
				}
				if u.Query().Get("container_id_type") != "thread" {
					t.Fatal("fallback to main chat")
				}
				switch mode {
				case "truncated":
					return page([]wireMessage{threadMessage("anchor", "10000"), threadMessage("a", "9999"), threadMessage("b", "9998"), threadMessage("c", "9997")}, true, "more"), nil
				case "same_time":
					return page([]wireMessage{threadMessage("later", "10000"), threadMessage("anchor", "10000"), threadMessage("between", "10000"), threadMessage("prior", "10000")}, false, ""), nil
				case "repeated_cursor":
					return page([]wireMessage{threadMessage("later", "11000")}, true, "same"), nil
				case "page_budget":
					return page([]wireMessage{threadMessage(fmt.Sprintf("later-%d", calls), "11000")}, true, fmt.Sprintf("page-%d", calls)), nil
				case "provider_error":
					return nil, errors.New("provider offline")
				case "wrong_chat":
					m := threadMessage("anchor", "10000")
					m.ChatID = "other"
					return page([]wireMessage{m}, false, ""), nil
				default:
					return page([]wireMessage{threadMessage("anchor", "10000"), threadMessage("a", "9999")}, false, ""), nil
				}
			}}
			if mode == "same_time" {
				j.PreviousTriggerID = "prior"
				j.PreviousTriggerTimeMS = 10000
			}
			got, err := r.Fetch(context.Background(), j)
			switch mode {
			case "truncated":
				if err != nil || !got.Truncated || len(got.Messages) != 2 || calls != 1 {
					t.Fatalf("%+v %v calls=%d", got, err, calls)
				}
			case "same_time":
				if err != nil || len(got.Messages) != 1 || got.Messages[0].ID != "between" {
					t.Fatalf("%+v %v", got, err)
				}
			default:
				if err == nil || len(got.Messages) != 0 || got.Digest != "" {
					t.Fatalf("%+v %v", got, err)
				}
			}
			if mode == "page_budget" && calls != 10 {
				t.Fatalf("unbounded requests: %d", calls)
			}
		})
	}
}

func TestMainChatHistoryNeverIncludesTopicMessages(t *testing.T) {
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), threadMessage("topic-message", "9999"), message("main-message", "9998")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), job())
	if err != nil || len(got.Messages) != 1 || got.Messages[0].ID != "main-message" {
		t.Fatalf("%+v %v", got, err)
	}
	r.Get = func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{threadMessage("anchor", "10000")}, false, ""), nil
	}
	if _, err = r.Fetch(context.Background(), job()); err == nil {
		t.Fatal("main chat accepted a topic anchor")
	}
}

func TestHistoryPreservesSpeakerAndMentions(t *testing.T) {
	m := threadMessage("a", "9999")
	m.Sender.Name = "张三"
	m.Sender.IDType = "open_id"
	m.Sender.TenantKey = "tenant"
	m.Mentions = []Mention{{ID: "bot", Key: "@_user_1", Name: "机器人", IDType: "open_id", TenantKey: "tenant"}}
	m.Body.Content = `{"text":"@_user_1 hi"}`
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{threadMessage("anchor", "10000"), m}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), threadJob())
	if err != nil || len(got.Messages) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	v := got.Messages[0]
	if v.SenderName != "张三" || v.SenderTenant != "tenant" || v.SenderIDType != "open_id" || len(v.Mentions) != 1 || v.Mentions[0].Name != "机器人" || v.Text != "@_user_1 hi" {
		t.Fatalf("lost raw metadata: %+v", v)
	}
}
