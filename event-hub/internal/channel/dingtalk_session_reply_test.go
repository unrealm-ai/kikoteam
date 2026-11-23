package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const syntheticSession = "https://oapi.dingtalk.com/robot/sendBySession?session=fixture-only"

func sessionReplyFixture() (Binding, Inbound, Delivery) {
	b := Binding{ID: "b1", Provider: "dingtalk", Enabled: true, Generation: 1, Revision: 7, RulesVersion: 3, TenantID: "corp", BotOpenID: "bot"}
	v := Inbound{BindingID: b.ID, Generation: b.Generation, Revision: b.Revision, RulesVersion: b.RulesVersion, TenantID: b.TenantID, BotID: b.BotOpenID,
		ChatID: "private", ChatType: "direct", SenderID: "person", MessageID: "business-message", EventID: "stream-event",
		Text: "incoming", MessageType: "text", LogProvider: "dingtalk"}
	d := Delivery{SessionWebhookEligible: true, BindingID: v.BindingID, Generation: v.Generation, Provider: b.Provider, TenantID: v.TenantID,
		ChatType: v.ChatType, RecipientID: v.SenderID, ChatID: v.ChatID, ReplyToMessageID: v.MessageID, Text: "reply"}
	return b, v, d
}

func sessionReplyRaw(address string, expires time.Time) string {
	return fmt.Sprintf(`{"sessionWebhook":%q,"sessionWebhookExpiredTime":%d}`, address, expires.UnixMilli())
}

func TestDingTalkSessionReplyURL(t *testing.T) {
	if !dingSessionURL(syntheticSession) {
		t.Fatal("official URL refused")
	}
	for _, url := range []string{
		"http://oapi.dingtalk.com/robot/sendBySession?session=fixture",
		"https://oapi.dingtalk.com.example.invalid/robot/sendBySession?session=fixture",
		"https://oapi.dingtalk.com:444/robot/sendBySession?session=fixture",
		"https://dummy@oapi.dingtalk.com/robot/sendBySession?session=fixture",
		"https://oapi.dingtalk.com/robot/send?session=fixture",
		"https://oapi.dingtalk.com/robot/%73endBySession?session=fixture",
		"https://oapi.dingtalk.com/robot/sendBySession?session=one&session=two",
		"https://oapi.dingtalk.com/robot/sendBySession?session=one&key=two",
		"https://oapi.dingtalk.com/robot/sendBySession?session=%0D",
		"https://oapi.dingtalk.com/robot/sendBySession?session=",
		"https://oapi.dingtalk.com/robot/sendBySession?session=fixture#metadata",
		"/robot/sendBySession?session=fixture",
	} {
		if dingSessionURL(url) {
			t.Fatal("unsafe or not supported target accepted")
		}
	}
}

func TestDingTalkSessionCacheOnlyAfterDurableCallback(t *testing.T) {
	for _, state := range []string{"accepted", "duplicate", "failure", "invalid"} {
		t.Run(state, func(t *testing.T) {
			b, v, d := sessionReplyFixture()
			callback := sessionReplyRaw(syntheticSession, time.Now().Add(time.Minute))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || strings.Contains(string(body), "sessionWebhook") || strings.Contains(string(body), "fixture-only") {
					t.Error("capability was persisted or forwarded")
				}
				if state == "failure" {
					w.WriteHeader(503)
					return
				}
				if e := json.NewEncoder(w).Encode(map[string]string{"id": "435ac751-2c45-4b6e-901e-6cccfd6716dc", "status": state}); e != nil {
					t.Error(e)
				}
			}))
			defer server.Close()
			backend, err := NewBackend(server.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			err = backend.forwardDingInbound(t.Context(), v, callback)
			if (err != nil) != (state == "failure" || state == "invalid") {
				t.Fatal("incorrect durable receipt", state, err)
			}
			_, cached := backend.dingReplies.lookup(b, d, time.Now())
			if cached != (state == "accepted") {
				t.Fatal("wrong cache state", state, cached)
			}
		})
	}
}

func TestDingTalkSessionCacheExactScopeAndExpiry(t *testing.T) {
	b, v, d := sessionReplyFixture()
	var c dingSessionReplies
	now := time.Now().Truncate(time.Millisecond)
	c.remember(v, sessionReplyRaw(syntheticSession, now.Add(8*time.Hour)), now)
	c.remember(v, sessionReplyRaw(strings.ReplaceAll(syntheticSession, "fixture-only", "replacement"), now.Add(time.Minute)), now)
	if orig, ok := c.lookup(b, d, now); !ok || orig.address != syntheticSession || !orig.expires.Equal(now.Add(dingSessionTTL)) {
		t.Fatal("original cap replaced or ttl not bounded")
	}
	for name, alter := range map[string]func(*Binding, *Delivery){
		"source binding":          func(b *Binding, d *Delivery) { b.ID = "another" },
		"advanced generation":     func(b *Binding, d *Delivery) { b.Generation++; d.Generation++ },
		"advanced revision":       func(b *Binding, d *Delivery) { b.Revision++ },
		"wrong delivery revision": func(_ *Binding, d *Delivery) { d.ConfigRevision++ },
		"advanced rules":          func(b *Binding, d *Delivery) { b.RulesVersion++ },
		"wrong tenant":            func(b *Binding, d *Delivery) { b.TenantID = "another"; d.TenantID = b.TenantID },
		"wrong bot":               func(b *Binding, _ *Delivery) { b.BotOpenID = "another" },
		"wrong chat":              func(_ *Binding, d *Delivery) { d.ChatID = "different" },
		"wrong kind":              func(_ *Binding, d *Delivery) { d.ChatType, d.RecipientID = "group", "" },
		"wrong sender":            func(_ *Binding, d *Delivery) { d.RecipientID = "different" },
		"envelope anchor":         func(_ *Binding, d *Delivery) { d.ReplyToMessageID = v.EventID },
		"no explicit eligibility": func(_ *Binding, d *Delivery) { d.SessionWebhookEligible = false },
		"no anchor":               func(_ *Binding, d *Delivery) { d.ReplyToMessageID = "" },
		"artifact":                func(_ *Binding, d *Delivery) { d.ArtifactID = "artifact" },
		"not text markdown":       func(_ *Binding, d *Delivery) { d.Format = "file" },
	} {
		t.Run(name, func(t *testing.T) {
			copyB, copyD := b, d
			alter(&copyB, &copyD)
			if _, ok := c.lookup(copyB, copyD, now); ok {
				t.Fatal("cache escaped intended identity")
			}
		})
	}
	v.ChatType, v.ChatID = "group", "room"
	d.ChatType, d.ChatID, d.RecipientID = v.ChatType, v.ChatID, ""
	c.remember(v, sessionReplyRaw(syntheticSession, now.Add(time.Minute)), now)
	if _, ok := c.lookup(b, d, now); !ok {
		t.Fatal("missing matching group capability")
	}
	if _, ok := c.lookup(b, d, now.Add(time.Minute)); ok {
		t.Fatal("expired provider capability")
	}
	c.prune(now.Add(dingSessionTTL + time.Minute))
	if len(c.items) != 0 {
		t.Fatal("expired capabilities retained after cleanup")
	}
}

type sessionReplyTransport func(*http.Request) (*http.Response, error)

func (f sessionReplyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDingTalkSessionReplyOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, format, raw, wantStatus, wantEvidence string
		httpCode                                    int
		expired, veto, fault                        bool
		wantOutgoing, wantFallback                  int
	}{
		{name: "text success", format: "text", raw: `{"errcode":0,"errmsg":"ok"}`, httpCode: 200, wantStatus: "sent", wantEvidence: "session_webhook", wantOutgoing: 1},
		{name: "markdown success", format: "markdown", raw: `{"errcode":0}`, httpCode: 200, wantStatus: "sent", wantEvidence: "session_webhook", wantOutgoing: 1},
		{name: "business denied at HTTP 200", raw: `{"errcode":310000}`, httpCode: 200, wantStatus: "failed", wantEvidence: "send_response", wantOutgoing: 1},
		{name: "denied", raw: `{"errcode":403,"errmsg":"private data"}`, httpCode: 400, wantStatus: "failed", wantEvidence: "send_response", wantOutgoing: 1},
		{name: "body missing", raw: `{}`, httpCode: 200, wantStatus: "unknown", wantOutgoing: 1},
		{name: "bad body", raw: `{"errcode":"0"}`, httpCode: 200, wantStatus: "unknown", wantOutgoing: 1},
		{name: "http conflict", raw: `{"errcode":0}`, httpCode: 400, wantStatus: "unknown", wantOutgoing: 1},
		{name: "provider uncertain", raw: `{"errcode":0}`, httpCode: 500, wantStatus: "unknown", wantOutgoing: 1},
		{name: "redirect", raw: `{"errcode":0}`, httpCode: 307, wantStatus: "unknown", wantOutgoing: 1},
		{name: "network uncertain", fault: true, wantStatus: "unknown", wantOutgoing: 1},
		{name: "expired active fallback", expired: true, wantStatus: "accepted", wantFallback: 1},
		{name: "scope changed no fallback", veto: true, wantStatus: "failed", wantEvidence: "not_dispatched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, v, d := sessionReplyFixture()
			d.Format = tc.format
			exp := time.Now().Add(time.Minute)
			if tc.expired {
				exp = time.Now().Add(-time.Second)
			}
			c := new(dingSessionReplies)
			c.remember(v, sessionReplyRaw(syntheticSession, exp), time.Now())
			checks, outgoing, fallback := 0, 0, 0
			d.BeforeDispatch = func() error {
				checks++
				if tc.veto {
					return errors.New("reconfigured")
				}
				return nil
			}
			client := newDingSessionHTTP()
			client.Transport = sessionReplyTransport(func(r *http.Request) (*http.Response, error) {
				outgoing++
				if r.Method != http.MethodPost || r.URL.Host != dingSessionHost || r.URL.Path != dingSessionPath ||
					r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" ||
					r.Header.Get("x-acs-dingtalk-access-token") != "" || r.Header.Get("Idempotency-Key") != "" || checks != 1 {
					t.Error("incorrect, unauthenticated or cross-target session request")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				kind := d.contentFormat()
				var fields map[string]string
				if string(body["msgtype"]) != fmt.Sprintf("%q", kind) || json.Unmarshal(body[kind], &fields) != nil ||
					(kind == "markdown" && (fields["title"] == "" || fields["text"] != d.Text)) ||
					(kind == "text" && fields["content"] != d.Text) {
					t.Error("bad DingTalk format", body)
				}
				if tc.fault {
					return nil, errors.New("fixture-only raw error")
				}
				resp := &http.Response{StatusCode: tc.httpCode, Body: io.NopCloser(strings.NewReader(tc.raw)), Header: make(http.Header)}
				if tc.httpCode == 307 {
					resp.Header.Set("Location", "https://never-follow.example.invalid/")
				}
				return resp, nil
			})
			active := func(context.Context, Binding, Delivery) deliveryReceipt {
				fallback++
				return deliveryReceipt{Status: "accepted"}
			}
			got := sendDingSessionReply(t.Context(), b, d, c, client, active)
			if got.Status != tc.wantStatus || got.EvidenceType != tc.wantEvidence || outgoing != tc.wantOutgoing || fallback != tc.wantFallback ||
				got.MessageID != "" || got.ProviderRequestID != "" || strings.Contains(got.ErrorCode, "fixture-only") {
				t.Fatal("wrong outcome", got, outgoing, fallback)
			}
			if got.Status == "sent" && got.EvidenceID != v.MessageID {
				t.Fatal("unbound evidence", got)
			}
		})
	}
}

func TestDingTalkSessionCacheConcurrentAndBounded(t *testing.T) {
	b, v, d := sessionReplyFixture()
	c := new(dingSessionReplies)
	now := time.Now()
	raw := sessionReplyRaw(syntheticSession, now.Add(time.Hour))
	var wg sync.WaitGroup
	for n := range dingSessionCap + 16 {
		wg.Go(func() {
			other := v
			other.MessageID = fmt.Sprintf("synthetic-%d", n)
			c.remember(other, raw, now)
			_, _ = c.lookup(b, d, now)
			c.prune(now)
		})
	}
	wg.Wait()
	if len(c.items) != dingSessionCap {
		t.Fatal("cache exceeded bound", len(c.items))
	}
	c.reset()
	if len(c.items) != 0 {
		t.Fatal("term ended but cache survived")
	}
}

func TestDingTalkSessionReplyUsesAuthorizationHintNotClaim(t *testing.T) {
	for _, authorizedEligible := range []bool{false, true} {
		t.Run(fmt.Sprintf("authorize_%t", authorizedEligible), func(t *testing.T) {
			b, v, d := sessionReplyFixture()
			b.Credentials = Credentials{AppID: "fake-app", AppSecret: "fake-secret"}
			d.ID, d.LeaseToken, d.IdempotencyKey, d.TargetDigest = "d1", "lease1", "idem1", "digest1"
			d.SessionWebhookEligible = !authorizedEligible
			completedCh := make(chan deliveryReceipt, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/internal/v1/channel-deliveries/d1/authorize":
					auth := d
					auth.SessionWebhookEligible = authorizedEligible
					if e := json.NewEncoder(w).Encode(struct {
						Delivery
						Allowed bool `json:"allowed"`
					}{auth, true}); e != nil {
						t.Error(e)
					}
				case "/api/internal/v1/channel-deliveries/d1/complete":
					var completed deliveryReceipt
					if e := json.NewDecoder(r.Body).Decode(&completed); e != nil {
						t.Error(e)
					}
					completedCh <- completed
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Error("unexpected backend path", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			backend, err := NewBackend(srv.URL, "internal")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			backend.dingReplies.remember(v, sessionReplyRaw(syntheticSession, now.Add(time.Minute)), now)
			client := newDingSessionHTTP()
			sessionWrites, proactiveWrites := 0, 0
			client.Transport = sessionReplyTransport(func(*http.Request) (*http.Response, error) {
				sessionWrites++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
			})
			active := func(context.Context, Binding, Delivery) deliveryReceipt {
				proactiveWrites++
				return deliveryReceipt{Status: "accepted"}
			}
			err = deliver(t.Context(), backend, func(string) (Binding, bool) { return b, true }, func(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
				return sendDingSessionReply(ctx, b, d, backend.dingReplies, client, active)
			}, d)
			if err != nil {
				t.Fatal(err)
			}
			var completed deliveryReceipt
			select {
			case completed = <-completedCh:
			default:
				t.Fatal("receipt not persisted")
			}
			wantStatus := "accepted"
			if authorizedEligible {
				wantStatus = "sent"
			}
			if completed.Status != wantStatus || (sessionWrites == 1) != authorizedEligible || (proactiveWrites == 1) == authorizedEligible {
				t.Fatal("routing used stale claim hint", completed.Status, sessionWrites, proactiveWrites)
			}
		})
	}
}
