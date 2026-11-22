package repository

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	repo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestWebhookCreationCredentialsHTTP(t *testing.T) {
	db, m, mux, w, _, employee, cookie := managerFixture(t)
	st := m.Store.(*repo.Store)
	base := "/api/v1/workspaces/" + w + "/agents/" + employee + "/triggers"
	request := func(method, path, body string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("X-CSRF-Protection", "1")
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != status || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: status %d, cache control %q", method, path, out.Code, out.Header().Get("Cache-Control"))
		}
		return out.Body.String()
	}
	checkCount := func(table string, want int) {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s count %d, want %d: %v", table, n, want, err)
		}
	}
	enabled := `{"kind":"webhook","enabled":true,"config":{"adapter":"event","source":"github","prompt":"汇总"},"allowed_actions":["create_task"]}`
	request("POST", base, enabled, 503)
	checkCount("manager_triggers", 0)
	checkCount("manager_trigger_credentials", 0)
	st.TriggerSecret = bytes.Repeat([]byte("t"), 32)
	for i, value := range []string{"true", "false"} {
		body := strings.Replace(enabled, "true", value, 1)
		raw := request("POST", base, body, 201)
		var created struct {
			manager.Trigger
			First manager.TriggerCredential `json:"initial_credential"`
		}
		if err := json.Unmarshal([]byte(raw), &created); err != nil || created.First.Secret == "" || created.First.Status != "active" || created.Enabled != (value == "true") || created.Endpoint != "http://localhost/api/triggers/events/"+created.ID {
			t.Fatal("incorrect creation response", err)
		}
		path := base + "/" + created.ID
		update := strings.Replace(body, "{", `{"version":"1",`, 1)
		for _, result := range []string{request("GET", base, "", 200), request("GET", path, "", 200), request("GET", path+"/credentials", "", 200), request("PUT", path, update, 200)} {
			if strings.Contains(result, created.First.Secret) || strings.Contains(result, "initial_credential") {
				t.Fatal("initial credential was exposed again")
			}
		}
		checkCount("manager_trigger_credentials", i+1)
	}
	gitlab := strings.Replace(enabled, "github", "gitlab", 1)
	request("POST", base, gitlab, 409)
	var hook manager.Trigger
	raw := request("POST", base, strings.Replace(gitlab, "true", "false", 1), 201)
	if err := json.Unmarshal([]byte(raw), &hook); err != nil || hook.Enabled || strings.Contains(raw, "initial_credential") {
		t.Fatal("incorrect GitLab creation response", err)
	}
	path := base + "/" + hook.ID
	token := "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	activate := strings.Replace(gitlab, "{", `{"version":"1","signing_token":"`+token+`",`, 1)
	request("PUT", path, strings.Replace(activate, token, "invalid", 1), 400)
	checkCount("manager_trigger_credentials", 2)
	raw = request("PUT", path, activate, 200)
	if err := json.Unmarshal([]byte(raw), &hook); err != nil || !hook.Enabled || strings.Contains(raw, token) {
		t.Fatal("incorrect GitLab activation response", err)
	}
	request("PUT", path, activate, 409)
	checkCount("manager_trigger_credentials", 3)
}

func TestWebhookCredentialAcceptsEmptyChunkedBody(t *testing.T) {
	_, m, mux, w, actor, employee, cookie := managerFixture(t)
	st := m.Store.(*repo.Store)
	st.TriggerSecret = bytes.Repeat([]byte("t"), 40)
	hook, err := st.SaveTrigger(t.Context(), w, actor, employee, "", manager.TriggerWrite{
		Kind: "webhook", Config: json.RawMessage(`{"adapter":"generic"}`), AllowedActions: []string{"create_task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/workspaces/" + w + "/agents/" + employee + "/triggers/" + hook.ID + "/credentials"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-CSRF-Protection", "1")
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, req)
	if out.Code != http.StatusCreated {
		t.Fatalf("empty credential request returned %d", out.Code)
	}
}

func TestNativeWebhookAdmission(t *testing.T) {
	db, m, mux, w, actor, employee, _ := managerFixture(t)
	st := m.Store.(*repo.Store)
	st.TriggerSecret = []byte(strings.Repeat("t", 40))
	ctx := context.Background()
	server := httptest.NewServer(mux)
	defer server.Close()
	type binding struct{ id, secret string }
	create := func(source string) binding {
		t.Helper()
		cfg := manager.EventHookConfig{Adapter: "event", Source: source, Prompt: "逐字核对通知内容"}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		q := manager.TriggerWrite{Kind: "webhook", Enabled: source != "gitlab", Config: raw, AllowedActions: []string{"create_task"}}
		b, err := st.SaveTrigger(ctx, w, actor, employee, "", q)
		if err != nil {
			t.Fatal(err)
		}
		if source != "gitlab" {
			if b.InitialCredential == nil || b.InitialCredential.Secret == "" {
				t.Fatal("missing initial credential")
			}
			return binding{b.ID, b.InitialCredential.Secret}
		}
		token := "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))
		q.Version, q.Enabled, q.SigningToken = b.Version, true, &token
		if _, err = st.SaveTrigger(ctx, w, actor, employee, b.ID, q); err != nil {
			t.Fatal(err)
		}
		return binding{b.ID, token}
	}
	git := create("github")
	push := `{"repository":{"id":123},"ref":"refs/heads/main","action":"cancel_task","prompt":"external",
  "unknown":{"items":[1,null,"原文"],"integer":9007199254740993}}`
	headers := githubHeaders(git.secret, "push", manager.NewID(), push)
	first := sendEventRequest(t, server, git.id, headers, push, 202)
	tls := httptest.NewTLSServer(mux)
	defer tls.Close()
	sendEventRequest(t, tls, git.id, headers, push, 200)
	for _, adapter := range []string{"webhook", "github"} {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/api/triggers/"+adapter+"/"+git.id, strings.NewReader(push)))
		if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), "trigger_https_required") {
			t.Fatalf("legacy %s transport guard: %d %s", adapter, out.Code, out.Body)
		}
	}
	resend := githubHeaders(git.secret, "push", manager.NewID(), push)
	dup := sendEventRequest(t, server, git.id, resend, push, 200)
	if !dup.Duplicate || dup.TaskID != first.TaskID {
		t.Fatal("changed delivery header repeated signed body")
	}
	changed := push + " "
	sendEventRequest(t, server, git.id, githubHeaders(git.secret, "push", resend.Get("X-GitHub-Delivery"), changed), changed, 409)
	sendEventRequest(t, server, git.id, githubHeaders("wrong", "push", manager.NewID(), push), push, 401)
	other := strings.Replace(push, "123", "456", 1)
	// A previously saved repositoryId is ignored even before the first configuration edit.
	reviewExec(t, db, `UPDATE manager_triggers SET config=config || '{"repositoryId":123}'::jsonb WHERE id=$1`, git.id)
	sendEventRequest(t, server, git.id, githubHeaders(git.secret, "push", manager.NewID(), other), other, 202)
	legacy, err := st.GetTrigger(ctx, w, actor, employee, git.id)
	if err != nil {
		t.Fatal(err)
	}
	edit := manager.TriggerWrite{Kind: "webhook", Config: legacy.Config, Enabled: true, Version: legacy.Version, AllowedActions: []string{"create_task"}}
	legacy, err = st.SaveTrigger(ctx, w, actor, employee, git.id, edit)
	if err != nil {
		t.Fatal("edit with legacy ID", err)
	}
	edit.Config = json.RawMessage(`{"adapter":"event","source":"github","prompt":"更新后的说明"}`)
	edit.Version = legacy.Version
	if _, err = st.SaveTrigger(ctx, w, actor, employee, git.id, edit); err != nil {
		t.Fatal("edit without legacy ID", err)
	}
	var body []byte
	var text string
	if err := db.QueryRow(`SELECT p.body,i.text FROM manager_webhook_payloads p JOIN manager_inputs i ON i.trigger_event_id=p.event_id WHERE i.id=$1`, first.InputID).Scan(&body, &text); err != nil || string(body) != push || !strings.Contains(text, push) || !strings.Contains(text, "逐字核对") || strings.Contains(text, headers.Get("X-Hub-Signature-256")) {
		t.Fatal("incorrect input snapshot", err)
	}

	generic := create("custom")
	headers = make(http.Header)
	headers.Set("Authorization", "Bearer "+generic.secret)
	headers.Set("Content-Type", "application/octet-stream")
	binary := strings.Repeat("\x00\xff", 128*1024)
	first = sendEventRequest(t, server, generic.id, headers, binary, 202)
	second := sendEventRequest(t, server, generic.id, headers, binary, 202)
	if second.TaskID == first.TaskID {
		t.Fatal("distinct unkeyed events merged")
	}
	var file bool
	if err := db.QueryRow(`SELECT p.body,p.as_file,i.text FROM manager_webhook_payloads p JOIN manager_inputs i ON i.trigger_event_id=p.event_id WHERE i.id=$1`, first.InputID).Scan(&body, &file, &text); err != nil || !file || string(body) != binary || !strings.Contains(text, manager.EventBodyPath(first.EventID)) || strings.Contains(text, generic.secret) {
		t.Fatal("incomplete binary attachment", err)
	}
	engine := manager.Engine{ID: manager.NewID(), HostID: "event-test", Endpoint: "http://localhost:9000", Capacity: 8}
	if err := st.RegisterEngine(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if err := st.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var assignment string
	var generation int64
	if err := db.QueryRow(`SELECT assignment_id,generation FROM manager_sessions WHERE id=$1`, first.SessionID).Scan(&assignment, &generation); err != nil {
		t.Fatal(err)
	}
	reviewStartTurn(t, db, w, first.SessionID, first.InputID, assignment, generation)
	cfg, _, err := st.HarnessConfig(ctx, engine.ID, first.SessionID, assignment, generation, first.InputID)
	if err != nil || cfg.WebhookFile == nil || cfg.WebhookFile.EventID != first.EventID || !bytes.Equal(cfg.WebhookFile.Body, body) {
		t.Fatal("assigned input lost its attachment", err)
	}
	if _, _, err = st.HarnessConfig(ctx, engine.ID, first.SessionID, assignment, generation, second.InputID); err == nil {
		t.Fatal("assignment could read another task's input")
	}
	sendEventRequest(t, server, generic.id, headers, binary+"x", 413)
	headers.Set("Content-Type", "text/plain")
	headers.Set("Idempotency-Key", "alarm-123")
	sendEventRequest(t, server, generic.id, headers, "温度过高\n", 202)
	sendEventRequest(t, server, generic.id, headers, "温度过高\n", 200)
	headers.Del("Idempotency-Key")
	for _, sample := range []struct{ media, data string }{
		{"text/plain; charset=utf-8", strings.Repeat("通知\n", 2300)},
		{"multipart/form-data; boundary=example", "--example\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nhello\r\n--example--\r\n"},
	} {
		headers.Set("Content-Type", sample.media)
		r := sendEventRequest(t, server, generic.id, headers, sample.data, 202)
		if err := db.QueryRow(`SELECT body,as_file,content_type FROM manager_webhook_payloads WHERE event_id=$1`, r.EventID).Scan(&body, &file, &text); err != nil || string(body) != sample.data || !file || text != sample.media {
			t.Fatal("attachment failed to preserve content type or bytes", err)
		}
	}

	for _, source := range []string{"gitlab", "gitlab_token"} {
		b := create(source)
		payload := `{"object_kind":"push","extra":[1,2]}`
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		h.Set("X-Gitlab-Event", "Push Hook")
		if source == "gitlab" {
			id, ts := manager.NewID(), strconv.FormatInt(time.Now().Unix(), 10)
			key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(b.secret, "whsec_"))
			if err != nil {
				t.Fatal(err)
			}
			mac := hmac.New(sha256.New, key)
			_, _ = mac.Write([]byte(id + "." + ts + "." + payload))
			h.Set("webhook-id", id)
			h.Set("webhook-timestamp", ts)
			h.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		} else {
			h.Set("X-Gitlab-Token", b.secret)
			h.Set("Idempotency-Key", "42")
		}
		sendEventRequest(t, server, b.id, h, payload, 202)
		sendEventRequest(t, server, b.id, h, payload, 200)
		h.Set("webhook-signature", "v1,wrong")
		h.Set("X-Gitlab-Token", "wrong")
		sendEventRequest(t, server, b.id, h, payload, 401)
	}
	reviewExec(t, db, `UPDATE platform_agents SET enabled=false WHERE id=$1`, employee)
	sendEventRequest(t, server, git.id, resend, push, http.StatusConflict)
}

func TestGitHubFormEmployeeInput(t *testing.T) {
	payload := `{"action":"opened","issue":{"title":"新功能 + 邮件 & 归档"}}`
	body := url.Values{"payload": {payload}}.Encode()
	headers := githubHeaders("test", "issues", manager.NewID(), body)
	headers.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req := manager.EventRequest{Header: headers, Body: []byte(body)}
	p, id, ping, err := req.Payload("github")
	if err != nil || ping || id != headers.Get("X-GitHub-Delivery") {
		t.Fatalf("form metadata: ping=%t id=%q err=%v", ping, id, err)
	}
	input, err := p.Input("转告工作群", id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(input, "\n", 3)
	var data struct {
		Body    json.RawMessage `json:"body"`
		RawForm string          `json:"raw_form"`
	}
	if len(lines) != 3 || json.Unmarshal([]byte(lines[1]), &data) != nil || string(data.Body) != payload || data.RawForm != body || !strings.Contains(lines[2], "转告工作群") {
		t.Fatal("employee input lost the decoded GitHub payload, original form or prompt")
	}
	for _, invalid := range []string{body + "&" + body, "x=1", "payload=%zz", "payload=%FF", url.Values{"payload": {`{"hook_id":2,"zen":"ping"}`}}.Encode()} {
		req.Body = []byte(invalid)
		if _, _, _, err = req.Payload("github"); err == nil {
			t.Fatal("accepted malformed form or mislabeled handshake")
		}
	}
}
