package channelhistory

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func job() Job {
	return Job{ID: "job", LeaseToken: "lease", BindingID: "binding", Generation: 1, Revision: 2, RulesVersion: 3, TenantID: "tenant", ChatID: "chat", TriggerID: "anchor", TriggerTimeMS: 10000, Limit: 2, Mode: "since_last_trigger"}
}
func message(id string, at string) wireMessage {
	m := wireMessage{ID: id, ChatID: "chat", Created: at, Type: "text"}
	m.Sender.ID = "person"
	m.Sender.Type = "user"
	m.Body.Content = `{"text":"body"}`
	return m
}
func page(items []wireMessage, more bool, token string) []byte {
	p := wirePage{}
	p.Data.Items = items
	p.Data.HasMore = &more
	p.Data.Token = token
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return b
}
func allow(context.Context, Job) error { return nil }
func TestAdmissionBeforeAnyNetwork(t *testing.T) {
	for _, gate := range []Gate{nil, func(context.Context, Job) error { return errors.New("no") }} {
		calls := 0
		r := Reader{Authorize: gate, Get: func(context.Context, string) ([]byte, error) { calls++; return nil, nil }}
		if _, err := r.Fetch(context.Background(), job()); err == nil || calls != 0 {
			t.Fatalf("read without permission: %v %d", err, calls)
		}
	}
}
func TestPinnedAnchorPaginationSnapshot(t *testing.T) {
	calls := 0
	r := Reader{Authorize: allow, Get: func(_ context.Context, path string) ([]byte, error) {
		calls++
		u, err := url.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("end_time") != "11" || u.Query().Get("sort_type") != "ByCreateTimeDesc" {
			t.Fatal(path)
		}
		if calls == 1 {
			return page([]wireMessage{message("later", "10000"), message("anchor", "10000"), message("a", "9999")}, true, "p2"), nil
		}
		if u.Query().Get("page_token") != "p2" {
			t.Fatal(path)
		}
		return page([]wireMessage{message("a", "9999"), message("b", "9998"), message("c", "9997")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), job())
	if err != nil || !got.Truncated || len(got.Messages) != 2 || got.Messages[0].ID != "b" || got.Messages[1].ID != "a" || len(got.Digest) != 64 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestFailClosedCases(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		edit func(*Job)
	}{{"anchor absent", page([]wireMessage{message("other", "9999")}, false, ""), nil}, {"scope", page([]wireMessage{{ID: "x", ChatID: "wrong"}}, false, ""), nil}, {"thread scope", page([]wireMessage{message("anchor", "10000")}, false, ""), func(j *Job) { j.ThreadID = "thread" }}, {"budget", nil, func(j *Job) { j.Limit = 101 }}, {"mode", nil, func(j *Job) { j.Mode = "recent" }}, {"missing pagination", []byte(`{"code":0,"data":{"items":[]}}`), nil}, {"permission", []byte(`{"code":99991672}`), nil}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := job()
			if tc.edit != nil {
				tc.edit(&j)
			}
			r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) { return tc.body, nil }}
			got, err := r.Fetch(context.Background(), j)
			if err == nil || len(got.Messages) != 0 {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
func TestRevocationAfterReadDiscardsSnapshot(t *testing.T) {
	checks := 0
	r := Reader{Authorize: func(context.Context, Job) error {
		checks++
		if checks > 1 {
			return errors.New("revoked")
		}
		return nil
	}, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("a", "9999")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), job())
	if err == nil || got.Digest != "" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestRepeatedCursorAndLargeContextFail(t *testing.T) {
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000")}, true, "same"), nil
	}}
	if _, err := r.Fetch(context.Background(), job()); err == nil {
		t.Fatal("loop")
	}
	m := message("a", "9999")
	body, err := json.Marshal(map[string]string{"text": strings.Repeat("x", 32769)})
	if err != nil {
		t.Fatal(err)
	}
	m.Body.Content = string(body)
	r.Get = func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), m}, false, ""), nil
	}
	if _, err := r.Fetch(context.Background(), job()); err == nil {
		t.Fatal("oversized context")
	}
}
func TestSnapshotDigestDoesNotDependOnLease(t *testing.T) {
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("a", "9999")}, false, ""), nil
	}}
	a := job()
	first, e := r.Fetch(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	a.LeaseToken = "second-lease"
	second, e := r.Fetch(context.Background(), a)
	if e != nil || first.Digest != second.Digest {
		t.Fatal("lease changed snapshot digest", e)
	}
}

func TestProviderOutOfOrderPageCannotProduceRecentSnapshot(t *testing.T) {
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("a", "9000"), message("b", "9500")}, false, ""), nil
	}}
	if _, err := r.Fetch(context.Background(), job()); err == nil {
		t.Fatal("accepted out-of-order history")
	}
}

func TestSinceLastTriggerStrictIDBoundaries(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "prior"
	j.PreviousTriggerTimeMS = 9998
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("between", "9999"), message("prior", "9998"), message("old", "9997")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), j)
	if err != nil || len(got.Messages) != 1 || got.Messages[0].ID != "between" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestMissingPreviousAnchorNeverFallsBackToRecent(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "deleted-prior"
	j.PreviousTriggerTimeMS = 9998
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("between", "9999"), message("old", "9997")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), j)
	if err == nil || len(got.Messages) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestTruncationStillVerifiesPreviousAnchor(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "prior"
	j.PreviousTriggerTimeMS = 9995
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("a", "9999"), message("b", "9998"), message("c", "9997"), message("prior", "9995")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), j)
	if err != nil || !got.Truncated || len(got.Messages) != 2 || got.Messages[0].ID != "b" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestSameTimeAnchorsUseProviderPositionNotLexicalID(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "z-prior"
	j.PreviousTriggerTimeMS = 10000
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("later", "10000"), message("anchor", "10000"), message("a-between", "10000"), message("z-prior", "10000")}, false, ""), nil
	}}
	got, err := r.Fetch(context.Background(), j)
	if err != nil || len(got.Messages) != 1 || got.Messages[0].ID != "a-between" {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestConsecutiveTriggersExcludeBothAnchors(t *testing.T) {
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("second", "11000"), message("between", "10500"), message("anchor", "10000"), message("older", "9000")}, false, ""), nil
	}}
	j := job()
	first, err := r.Fetch(context.Background(), j)
	if err != nil || len(first.Messages) != 1 || first.Messages[0].ID != "older" {
		t.Fatalf("first %+v %v", first, err)
	}
	j.TriggerID = "second"
	j.TriggerTimeMS = 11000
	j.PreviousTriggerID = "anchor"
	j.PreviousTriggerTimeMS = 10000
	second, err := r.Fetch(context.Background(), j)
	if err != nil || len(second.Messages) != 1 || second.Messages[0].ID != "between" {
		t.Fatalf("second %+v %v", second, err)
	}
}
func TestFullWindowNewerThanPreviousCanTruncateWithoutOldAnchor(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "far-prior"
	j.PreviousTriggerTimeMS = 1000
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("anchor", "10000"), message("a", "9999"), message("b", "9998"), message("c", "9997")}, true, "more"), nil
	}}
	got, err := r.Fetch(context.Background(), j)
	if err != nil || !got.Truncated || len(got.Messages) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestPriorBoundaryChangesDigestAndInvalidOrderIsRejected(t *testing.T) {
	j := job()
	j.PreviousTriggerID = "prior"
	j.PreviousTriggerTimeMS = 9998
	r := Reader{Authorize: allow, Get: func(context.Context, string) ([]byte, error) {
		return page([]wireMessage{message("prior", "9998"), message("anchor", "10000")}, false, ""), nil
	}}
	if _, err := r.Fetch(context.Background(), j); err == nil {
		t.Fatal("accepted reversed anchors")
	}
	j.PreviousTriggerID = j.TriggerID
	if _, err := r.Fetch(context.Background(), j); err == nil {
		t.Fatal("accepted same anchors")
	}
}

func TestHistoryPostUsesProviderMentionMetadata(t *testing.T) {
	m := wireMessage{Type: "post", Mentions: []Mention{{ID: "ou_bot", Key: "@_user_1", Name: "员工", IDType: "open_id"}}}
	m.Body.Content = `{"content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"text","text":" 正文"}]]}`
	got, err := messageText(m)
	if err != nil || got != "@_user_1 正文" {
		t.Fatal(got, err)
	}
	m.Deleted = true
	got, err = messageText(m)
	if err != nil || got != "[消息已撤回]" {
		t.Fatal(got, err)
	}
}
