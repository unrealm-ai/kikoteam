package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDingTalkSendAcceptedAndQuery(t *testing.T) {
	for _, test := range []struct{ kind, format string }{{"direct", "text"}, {"group", "text"}, {"direct", "markdown"}, {"group", "markdown"}} {
		t.Run(test.kind+"_"+test.format, func(t *testing.T) {
			const text = "**完成** ✅ 👩🏽‍💻"
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.URL.Path == "/v1.0/oauth2/accessToken" {
					if _, err := w.Write([]byte(`{"accessToken":"token","expireIn":7200}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Header.Get("x-acs-dingtalk-access-token") != "token" {
					t.Error("missing token")
				}
				if strings.HasSuffix(r.URL.Path, "batchSend") || strings.HasSuffix(r.URL.Path, "/send") {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					key, field := "sampleText", "content"
					if test.format == "markdown" {
						key, field = "sampleMarkdown", "text"
					}
					var param map[string]string
					err := json.Unmarshal([]byte(body["msgParam"].(string)), &param)
					if body["robotCode"] != "robot" || body["msgKey"] != key || err != nil || param[field] != text || test.format == "markdown" && param["title"] != "AI 员工回复" {
						t.Error(body)
					}
					if test.kind == "group" && body["openConversationId"] != "chat" {
						t.Error(body)
					}
					if _, err := w.Write([]byte(`{"processQueryKey":"query-key"}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if _, err := w.Write([]byte(`{"sendStatus":"SUCCESS"}`)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			b, _ := dingFixture()
			b.BotOpenID = "robot"
			b.TenantID = "corp"
			d := Delivery{ChatType: test.kind, ChatID: "chat", Text: text, Format: test.format, ReplyToMessageID: "original"}
			if test.kind == "direct" {
				d.RecipientID = "user"
			}
			h := dingHTTP{client: server.Client(), origin: server.URL}
			got := sendDingTalkWithHTTP(context.Background(), b, d, h)
			if got.Status != "accepted" || got.ProviderRequestID != "query-key" || got.MessageID != "" {
				t.Fatal(got)
			}
			d.ProviderRequestID = got.ProviderRequestID
			got = queryDingTalkWithHTTP(context.Background(), b, d, h)
			if got.Status != "sent" || got.EvidenceType != "status_query" || got.EvidenceID != "query-key" || got.MessageID != "" {
				t.Fatal(got)
			}
			if len(calls) != 4 {
				t.Fatal(calls)
			}
		})
	}
}
func TestDingTalkSendDoesNotRetryOrInventEvidence(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`{}`, "unknown"}, {`{"processQueryKey":"key","invalidStaffIdList":["other"]}`, "unknown"},
		{`{"processQueryKey":"key","invalidStaffIdList":["user"]}`, "failed"},
		{`{"processQueryKey":"key","flowControlledStaffIdList":["user"]}`, "failed"},
		{`{"errcode":123}`, "unknown"}, {`not-json`, "unknown"},
	} {
		t.Run(test.body, func(t *testing.T) {
			sends := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "accessToken") {
					if _, err := w.Write([]byte(`{"accessToken":"token","expireIn":7200}`)); err != nil {
						t.Error(err)
					}
					return
				}
				sends++
				if _, err := w.Write([]byte(test.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			b, _ := dingFixture()
			b.BotOpenID = "robot"
			b.TenantID = "corp"
			d := Delivery{ChatType: "direct", RecipientID: "user", Text: "hello"}
			got := sendDingTalkWithHTTP(context.Background(), b, d, dingHTTP{server.Client(), server.URL})
			if sends != 1 || got.Status != test.want || got.MessageID != "" {
				t.Fatal(got, sends)
			}
			d.ThreadID = "topic"
			got = sendDingTalkWithHTTP(context.Background(), b, d, dingHTTP{server.Client(), server.URL})
			if got.Status != "failed" || sends != 1 {
				t.Fatal("topic fallback", got, sends)
			}
		})
	}
}
func TestDingTalkQueryStatuses(t *testing.T) {
	for _, test := range []struct{ body, want string }{{`{"sendStatus":"PROCESSING"}`, "accepted"}, {`{"sendStatus":"RECALLED"}`, "failed"}, {`{"readCount":1}`, "unknown"}, {`{"code":1,"sendStatus":"SUCCESS"}`, "unknown"}} {
		t.Run(test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "accessToken") {
					if _, err := w.Write([]byte(`{"accessToken":"token","expireIn":7200}`)); err != nil {
						t.Error(err)
					}
					return
				}
				if _, err := w.Write([]byte(test.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			b, _ := dingFixture()
			b.BotOpenID = "robot"
			b.TenantID = "corp"
			got := queryDingTalkWithHTTP(context.Background(), b, Delivery{ChatType: "group", ChatID: "chat", ProviderRequestID: "key"}, dingHTTP{server.Client(), server.URL})
			if got.Status != test.want {
				t.Fatal(got)
			}
		})
	}
}

func TestDingTalkRevokedDuringTokenLookupNeverDispatches(t *testing.T) {
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "accessToken") {
			if _, err := w.Write([]byte(`{"accessToken":"token","expireIn":7200}`)); err != nil {
				t.Error(err)
			}
			return
		}
		sends++
	}))
	defer server.Close()
	b, _ := dingFixture()
	b.BotOpenID = "robot"
	b.TenantID = "corp"
	d := Delivery{ChatType: "group", ChatID: "chat", Text: "hello", BeforeDispatch: func() error { return context.Canceled }}
	got := sendDingTalkWithHTTP(context.Background(), b, d, dingHTTP{server.Client(), server.URL})
	if got.Status != "failed" || got.EvidenceType != "not_dispatched" || sends != 0 {
		t.Fatal(got, sends)
	}
}

func TestDingTalkProactiveSendDiagnosticsPreserveUnknownOutcome(t *testing.T) {
	const permission = `{"Code":"Forbidden.AccessDenied.AccessTokenPermissionDenied","Message":"raw-provider-message"}`
	for _, tc := range []struct {
		name, kind, body, code, evidence string
		status                           int
	}{
		{"group scope denied", "group", permission, "dingtalk_proactive_permission_denied", "send_response", http.StatusForbidden},
		{"private scope denied", "direct", permission, "dingtalk_proactive_permission_denied", "send_response", http.StatusForbidden},
		{"scope denied in legacy response", "direct", `{"errcode":"Forbidden.AccessDenied.AccessTokenPermissionDenied"}`, "dingtalk_proactive_permission_denied", "send_response", http.StatusOK},
		{"bare forbidden", "group", "", "dingtalk_send_unknown", "", http.StatusForbidden},
		{"HTML forbidden", "group", "<html>Forbidden</html>", "dingtalk_send_unknown", "", http.StatusForbidden},
		{"contradictory send evidence", "direct", `{"processQueryKey":"conflict","code":"Forbidden.AccessDenied.AccessTokenPermissionDenied"}`, "dingtalk_send_unknown", "", http.StatusOK},
		{"IP restriction not scope", "direct", `{"code":"Forbidden.AccessDenied.IpNotInWhiteList"}`, "dingtalk_send_unknown", "", http.StatusForbidden},
		{"provider rate denial not scope", "group", `{"code":"Forbidden.AccessDenied.QpsLimitForApi"}`, "dingtalk_send_unknown", "", http.StatusForbidden},
		{"definitive bad request", "group", `{"code":"invalidParameter"}`, "dingtalk_send_unknown", "", http.StatusBadRequest},
		{"legacy rejection", "group", `{"errcode":123}`, "dingtalk_send_rejected", "", http.StatusOK},
		{"malformed business code", "group", `{"errcode":null}`, "dingtalk_send_rejected", "", http.StatusOK},
		{"ambiguous timeout", "direct", permission, "dingtalk_send_unknown", "", http.StatusRequestTimeout},
		{"temporarily rate limited", "group", "", "dingtalk_send_unknown", "", http.StatusTooManyRequests},
		{"ambiguous server error", "group", permission, "dingtalk_send_unknown", "", http.StatusBadGateway},
		{"ambiguously invalid reply", "group", "{", "dingtalk_send_unknown", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/v1.0/robot/groupMessages/send"
			d := Delivery{ChatType: tc.kind, ChatID: "group", Text: "test"}
			if tc.kind == "direct" {
				path = "/v1.0/robot/oToMessages/batchSend"
				d.ChatID, d.RecipientID = "", "recipient"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1.0/oauth2/accessToken" {
					if _, err := w.Write([]byte(`{"accessToken":"token","expireIn":7200}`)); err != nil {
						t.Error("write token fixture:", err)
					}
					return
				}
				calls++
				if r.URL.Path != path || r.Header.Get("x-acs-dingtalk-access-token") != "token" {
					t.Error("wrong proactive endpoint or authentication")
				}
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error("write send fixture:", err)
				}
			}))
			defer server.Close()
			b, _ := dingFixture()
			b.BotOpenID, b.TenantID = "robot", "tenant"
			got := sendDingTalkWithHTTP(t.Context(), b, d, dingHTTP{server.Client(), server.URL})
			if want := (deliveryReceipt{Status: "unknown", ErrorCode: tc.code, EvidenceType: tc.evidence}); calls != 1 || got != want {
				t.Fatalf("unexpected delivery state, evidence or retry: %+v, calls=%d", got, calls)
			}
		})
	}
}

func TestDingTalkProactiveSendDoesNotClassifyPermissionAtTokenOrQuery(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, want string
		query                bool
	}{
		{"token", "/v1.0/oauth2/accessToken", "dingtalk_authentication_failed", false},
		{"query", "/v1.0/robot/groupMessages/query", "dingtalk_status_unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var raw string
				switch {
				case r.URL.Path == tc.endpoint:
					w.WriteHeader(http.StatusForbidden)
					raw = `{"code":"Forbidden.AccessDenied.AccessTokenPermissionDenied"}`
				case tc.query && r.URL.Path == "/v1.0/oauth2/accessToken":
					raw = `{"accessToken":"token","expireIn":7200}`
				default:
					t.Error("unexpected endpoint")
				}
				if _, err := w.Write([]byte(raw)); err != nil {
					t.Error("write fixture:", err)
				}
			}))
			defer server.Close()
			b, _ := dingFixture()
			b.BotOpenID, b.TenantID = "robot", "tenant"
			d := Delivery{ChatType: "group", ChatID: "group", Text: "test", ProviderRequestID: "accepted-key"}
			h := dingHTTP{server.Client(), server.URL}
			send := sendDingTalkWithHTTP
			if tc.query {
				send = queryDingTalkWithHTTP
			}
			if got := send(t.Context(), b, d, h); got.ErrorCode != tc.want {
				t.Fatalf("non-send endpoint mislabeled as an active send failure: %+v", got)
			}
		})
	}
}
