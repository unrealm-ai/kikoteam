package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
)

func TestSharedFeishuRangeScopeAndNoImplicitThreads(t *testing.T) {
	s := sharedHistorySource{Provider: "feishu", ExternalID: "group"}
	j := sharedHistoryJob{StartMS: 1000, EndMS: 5000, Limit: 3}
	rows := []map[string]any{{"message_id": "one", "chat_id": "group", "create_time": "2000", "msg_type": "text", "body": map[string]string{"content": `{"text":"allowed"}`}, "sender": map[string]string{"id": "u", "sender_type": "user"}}, {"message_id": "topic", "chat_id": "group", "thread_id": "thread", "create_time": "3000"}, {"message_id": "future", "chat_id": "group", "create_time": "5000"}}
	get := func(_ context.Context, path string) ([]byte, error) {
		u, e := url.Parse(path)
		if e != nil {
			t.Fatal(e)
		}
		if u.Query().Get("container_id") != "group" || u.Query().Get("start_time") != "1" || u.Query().Get("end_time") != "5" {
			t.Fatal(path)
		}
		return json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": rows, "has_more": false}})
	}
	page, e := readSharedFeishuPage(context.Background(), s, j, func(context.Context) error { return nil }, get)
	if e != nil || len(page.Items) != 1 || page.Items[0].Text != "allowed" {
		t.Fatal(page, e)
	}
	rows[0]["msg_type"] = "post"
	rows[0]["body"] = map[string]string{"content": `{"title":"题目","content":[[{"tag":"text","text":"rich"}]]}`}
	page, e = readSharedFeishuPage(context.Background(), s, j, func(context.Context) error { return nil }, get)
	if e != nil || len(page.Items) != 1 || page.Items[0].Text != "# 题目\nrich" {
		t.Fatal(page, e)
	}
	rows[0]["chat_id"] = "other"
	if _, e = readSharedFeishuPage(context.Background(), s, j, func(context.Context) error { return nil }, get); e == nil {
		t.Fatal("cross group accepted")
	}
	calls := 0
	if _, e = readSharedFeishuPage(context.Background(), s, j, func(context.Context) error { return errors.New("revoked") }, func(context.Context, string) ([]byte, error) { calls++; return nil, nil }); e == nil || calls != 0 {
		t.Fatal("revoked fetch", calls, e)
	}
}
func TestSharedFeishuCursorAndProviderFailure(t *testing.T) {
	s := sharedHistorySource{ExternalID: "group"}
	j := sharedHistoryJob{StartMS: 1000, EndMS: 5000, Limit: 2, Cursor: "same"}
	for _, raw := range []string{`{"code":1,"data":{"items":[],"has_more":false}}`, `{"code":0,"data":{"items":[],"has_more":true,"page_token":"same"}}`, `{"code":0,"data":{"items":[],"has_more":true}}`} {
		if _, e := readSharedFeishuPage(context.Background(), s, j, func(context.Context) error { return nil }, func(context.Context, string) ([]byte, error) { return []byte(raw), nil }); e == nil {
			t.Fatal("bad page accepted", raw)
		}
	}
}
