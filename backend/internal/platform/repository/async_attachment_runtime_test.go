package repository

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

const (
	asyncChatRequest = "核对还在传输的附件"
	asyncLaterText   = "附件还在传，先答这条文字"
	asyncTaskRequest = "read-async-attachment"
	asyncRetry       = "async-attachment-upload-complete"
	asyncTaskPending = "attachment-status-proof-downloading"
	asyncChatLater   = "text-replied-before-upload"
	asyncComplete    = "attachment-runtime-proof-completed"
	asyncFileName    = "async-proof.txt"
	asyncFilePath    = "uploads/" + asyncFileName
	asyncFileContent = "round-trip-file-proof-kiko-834"
)

var asyncRef = regexp.MustCompile(`\[附件 名称="async-proof\.txt" attachment_id=([0-9a-f-]{36})\]`)

func asyncAttachmentModelReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) bool {
	t.Helper()
	if len(body.Messages) == 0 {
		return false
	}
	user, task, retry := "", false, false
	for _, msg := range body.Messages {
		if msg.Role == "user" {
			user = fmt.Sprint(msg.Content)
			task = task || strings.Contains(user, asyncTaskRequest)
			retry = retry || strings.Contains(user, asyncRetry)
		}
	}
	last := body.Messages[len(body.Messages)-1]
	lastText := fmt.Sprint(last.Content)
	if task {
		if last.Role == "tool" {
			switch {
			case strings.Contains(lastText, asyncFileContent):
				humanTextStream(t, w, asyncComplete)
			case strings.Contains(lastText, asyncFilePath):
				humanToolStream(t, w, []string{"mcp__builtin__read"}, []any{map[string]string{"path": asyncFilePath}})
			case strings.Contains(lastText, "downloading") && !retry:
				if strings.Contains(lastText, `"path"`) {
					t.Error("pending attachment exposed a path")
				}
				humanTextStream(t, w, asyncTaskPending)
			default:
				t.Errorf("unexpected attachment status: %s", lastText)
				humanTextStream(t, w, "fixture failure")
			}
			return true
		}
		humanToolStream(t, w, []string{"mcp__input_attachments__attachment_get"}, []any{map[string]string{"attachment_id": asyncTaskAttachmentID(user, body)}})
		return true
	}
	if strings.Contains(user, asyncLaterText) {
		humanTextStream(t, w, asyncChatLater)
		return true
	}
	if !strings.Contains(user, asyncChatRequest) {
		return false
	}
	if last.Role == "tool" {
		humanTextStream(t, w, "attachment task queued")
		return true
	}
	id := asyncRef.FindStringSubmatch(user)
	if len(id) != 2 || strings.Contains(user, `"status"`) || strings.Contains(user, "downloading") {
		t.Errorf("chat must see a file label without transfer status: %s", user)
		humanTextStream(t, w, "fixture failure")
		return true
	}
	humanToolStream(t, w, []string{"mcp__task_delegation__task_delegation_create_session"}, []any{map[string]any{"instruction": asyncTaskRequest + " attachment-ref=" + id[1], "attachment_ids": []string{id[1]}}})
	return true
}

func asyncTaskAttachmentID(user string, body runtimeRequest) string {
	if id := asyncRef.FindStringSubmatch(user); len(id) == 2 {
		return id[1]
	}
	if _, tail, ok := strings.Cut(user, " attachment-ref="); ok {
		if fields := strings.Fields(tail); len(fields) > 0 {
			return fields[0]
		}
	}
	for _, m := range body.Messages {
		if m.Role == "user" {
			if id := asyncRef.FindStringSubmatch(fmt.Sprint(m.Content)); len(id) == 2 {
				return id[1]
			}
		}
	}
	return ""
}

func testRuntimeAsyncAttachment(t *testing.T, ctx context.Context, db *sql.DB, base, agent, server, token string, call func(string, string, any, string) map[string]any, wait func(func() bool)) {
	t.Helper()
	conversation := call("POST", base+"/conversations/web-direct", map[string]string{"agent_id": agent}, "")["id"].(string)
	baseAttachment := base + "/conversations/" + conversation + "/attachments"
	id := call("POST", baseAttachment, map[string]any{"name": asyncFileName, "size": len(asyncFileContent)}, "reserve-async-e2e")["id"].(string)
	var chat string
	if err := db.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat'`, conversation).Scan(&chat); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/sessions/"+chat+"/inputs", map[string]any{"text": asyncChatRequest, "attachment_ids": []string{id}}, "initial-async-e2e")
	waitForText := func(session, marker string) {
		t.Helper()
		wait(func() bool {
			var found bool
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_session_events WHERE session_id=$1 AND event->>'type'='agent.message' AND event::text LIKE '%'||$2||'%')`, session, marker).Scan(&found)
			return err == nil && found
		})
	}
	var task string
	wait(func() bool {
		return db.QueryRowContext(ctx, `SELECT s.id FROM manager_sessions s JOIN manager_inputs i ON i.id=s.initial_input_id WHERE s.conversation_id=$1 AND s.mode='task' AND i.text LIKE $2||'%'`, conversation, asyncTaskRequest).Scan(&task) == nil
	})
	waitForText(task, asyncTaskPending)
	call("POST", base+"/sessions/"+chat+"/inputs", map[string]string{"text": asyncLaterText}, "later-text-e2e")
	waitForText(chat, asyncChatLater)
	var writer bytes.Buffer
	mp := multipart.NewWriter(&writer)
	f, err := mp.CreateFormFile("file", asyncFileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, asyncFileContent); err != nil {
		t.Fatal(err)
	}
	if err = mp.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+baseAttachment+"/"+id+"/upload", &writer)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "kiko_session="+token)
	req.Header.Set("X-CSRF-Protection", "1")
	req.Header.Set("Content-Type", mp.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("upload response %s: %s (%v)", res.Status, data, err)
	}
	call("POST", base+"/sessions/"+task+"/inputs", map[string]string{"text": asyncRetry + " attachment-ref=" + id}, "retry-async-e2e")
	waitForText(task, asyncComplete)
}
