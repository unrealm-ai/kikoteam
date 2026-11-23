package repository

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestWebAttachmentReservedBeforeUpload(t *testing.T) {
	db, svc, mux, w, actor, agent, cookie := managerFixture(t)
	svc.ProjectRoot = t.TempDir()
	ctx := t.Context()
	conv, err := svc.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	ws := "/api/v1/workspaces/" + w
	base := ws + "/conversations/" + conv.ID + "/attachments"
	request := func(method, address, key, contentType string, body []byte, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, address, bytes.NewReader(body))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Content-Type", contentType)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != want {
			t.Fatalf("%s %s: %d want %d (%s)", method, address, out.Code, want, out.Body.String())
		}
		return out.Body.Bytes()
	}
	parse := func(data []byte) manager.Attachment {
		t.Helper()
		var a manager.Attachment
		if e := json.Unmarshal(data, &a); e != nil {
			t.Fatal(e)
		}
		return a
	}
	reserve := func(key string, want int) manager.Attachment {
		t.Helper()
		return parse(request("POST", base, key, "application/json", []byte(`{"name":"sales.csv","size":5}`), want))
	}
	upload := func(id, body string, want int) []byte {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		file, e := mw.CreateFormFile("file", "sales.csv")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.WriteString(file, body); e != nil {
			t.Fatal(e)
		}
		if e = mw.Close(); e != nil {
			t.Fatal(e)
		}
		return request("POST", base+"/"+id+"/upload", "", mw.FormDataContentType(), buf.Bytes(), want)
	}

	a := reserve("first", 201)
	if a.ID == "" || a.Status != "downloading" || a.Path != "" || reserve("first", 200) != a {
		t.Fatalf("bad pending attachment: %+v", a)
	}
	request("POST", base, "first", "application/json", []byte(`{"name":"other.csv","size":5}`), 409)
	request("GET", ws+"/conversations/"+manager.NewID()+"/attachments/"+a.ID, "", "", nil, 404)
	body, err := json.Marshal(map[string]any{"conversation_id": conv.ID, "mode": "chat", "input": map[string]any{"attachment_ids": []string{a.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	var session manager.Session
	if err = json.Unmarshal(request("POST", ws+"/sessions", "pending-message", "application/json", body, 201), &session); err != nil {
		t.Fatal(err)
	}
	if session.InitialInputID == nil {
		t.Fatal("file-only session did not create input")
	}
	v, err := svc.Store.Input(ctx, w, actor, session.ID, *session.InitialInputID)
	if err != nil || len(v.Attachments) != 1 || v.Attachments[0] != a || v.Text != "" {
		t.Fatalf("did not persist pending attachment %#v: %v", v.Attachments, err)
	}
	ready := parse(upload(a.ID, "sales", 201))
	if ready.ID != a.ID || ready.Status != "ready" || ready.Path != "uploads/sales.csv" || parse(upload(a.ID, "sales", 200)) != ready || parse(request("GET", base+"/"+a.ID, "", "", nil, 200)) != ready {
		t.Fatalf("bad published attachment: %+v", ready)
	}
	if got := string(request("GET", ws+"/conversations/"+conv.ID+"/files/content?path="+ready.Path, "", "", nil, 200)); got != "sales" {
		t.Fatalf("file bytes not published: %q", got)
	}
	failed := reserve("bad-size", 201)
	upload(failed.ID, "x", 400)
	if got := parse(request("GET", base+"/"+failed.ID, "", "", nil, 200)); got.ID != failed.ID || got.Status != "failed" || got.Path != "" {
		t.Fatalf("malformed authenticated upload did not fail the same ID: %+v", got)
	}
	upload(failed.ID, "sales", 409)
	expired := reserve("expired", 201)
	if _, err = db.ExecContext(ctx, `UPDATE manager_web_attachments SET expires_at=now()-interval '1 minute' WHERE id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if got := parse(request("GET", base+"/"+expired.ID, "", "", nil, 200)); got.Status != "failed" || got.ID != expired.ID {
		t.Fatalf("expired reservation still pending: %+v", got)
	}
	upload(expired.ID, "sales", 409)
}

func TestWebAttachmentPublicationRevalidatesClaim(t *testing.T) {
	db, svc, _, w, actor, agent, _ := managerFixture(t)
	svc.ProjectRoot = t.TempDir()
	conv, err := svc.ResolveWebConversation(t.Context(), w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, mutate, restore string
		args                  []any
	}{
		{"expired", `UPDATE manager_web_attachments SET expires_at=now()-interval '1 second' WHERE id=$1`, "", nil},
		{"membership-revoked", `UPDATE platform_members SET status='disabled' WHERE workspace_id=$1 AND user_id=$2`, `UPDATE platform_members SET status='active' WHERE workspace_id=$1 AND user_id=$2`, []any{w, actor}},
		{"agent-disabled", `UPDATE platform_agents SET enabled=false WHERE id=$1`, `UPDATE platform_agents SET enabled=true WHERE id=$1`, []any{agent}},
		{"archived", `UPDATE manager_conversations SET lifecycle='archived',archived_at=now() WHERE id=$1`, "", []any{conv.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			name := tc.name + ".csv"
			a, _, err := svc.ReserveWebAttachment(ctx, w, actor, conv.ID, tc.name, name, 5)
			if err != nil {
				t.Fatal(err)
			}
			u, err := svc.BeginWebAttachment(ctx, w, actor, conv.ID, a.ID)
			if err != nil || u.Token == "" {
				t.Fatalf("failed to claim: %v", err)
			}
			args := tc.args
			if args == nil {
				args = []any{a.ID}
			}
			reviewExec(t, db, tc.mutate, args...)
			if _, err = svc.PublishWebAttachment(ctx, u, name, 5, strings.NewReader("sales")); err == nil {
				t.Fatal("stale authorization published file")
			}
			if err = svc.FailWebAttachment(ctx, u); err != nil {
				t.Fatal(err)
			}
			if tc.restore != "" {
				reviewExec(t, db, tc.restore, args...)
			}
			current, err := svc.WebAttachment(ctx, w, actor, conv.ID, a.ID)
			if err != nil || current.Status != "failed" || current.Path != "" {
				t.Fatalf("bad terminal status: %+v, %v", current, err)
			}
			d, err := svc.ListProject(conv, "")
			if err != nil || len(d.Entries) != 0 {
				t.Fatalf("unauthorized content became visible: %+v, %v", d, err)
			}
		})
	}
}
