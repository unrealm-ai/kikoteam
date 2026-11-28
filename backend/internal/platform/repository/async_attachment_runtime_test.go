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
	asyncResume      = "继续上次任务并读取本次附件"
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
		if (strings.Contains(user, asyncTaskRequest) && !strings.Contains(user, asyncChatRequest)) ||
			(strings.Contains(user, asyncRetry) && !strings.Contains(user, asyncResume)) ||
			!strings.Contains(user, `"forwarded":true`) {
			t.Errorf("task did not receive the source message and its selected attachment: %s", user)
		}
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
	isAppend := strings.Contains(user, asyncResume)
	if !strings.Contains(user, asyncChatRequest) && !isAppend {
		return false
	}
	id := asyncRef.FindStringSubmatch(user)
	if len(id) != 2 || strings.Contains(user, `"status"`) || strings.Contains(user, "downloading") {
		t.Errorf("chat must see a file label without transfer status: %s", user)
		humanTextStream(t, w, "fixture failure")
		return true
	}
	args := map[string]any{"instruction": asyncTaskRequest, "attachment_ids": []string{id[1]}}
	tool := "mcp__task_delegation__task_delegation_create_session"
	if isAppend {
		tool = "mcp__task_delegation__task_delegation_supplement_task"
		_, suffix, _ := strings.Cut(user, "task_id=")
		fields := strings.Fields(suffix)
		if len(fields) == 0 {
			t.Error("fixture missing target task")
			humanTextStream(t, w, "fixture failure")
			return true
		}
		args = map[string]any{"task_id": fields[0], "instruction": asyncRetry, "attachment_ids": []string{id[1]}}
	}
	switch {
	case last.Role != "tool":
		args["attachment_paths"] = []string{"outputs/unreferenced.csv"}
	case strings.Contains(lastText, "permission_denied"):
		delete(args, "attachment_ids")
		args["source_files_unused"] = true
	case strings.Contains(lastText, "attachment_retry_required"):
		// Correct the rejected selector while keeping the user's actual file.
	case strings.Contains(lastText, "accepted") || isAppend && strings.Contains(lastText, "receipt"):
		humanTextStream(t, w, "attachment task queued")
		return true
	default:
		t.Errorf("unexpected delegation receipt: %s", lastText)
		humanTextStream(t, w, "fixture failure")
		return true
	}
	humanToolStream(t, w, []string{tool}, []any{args})
	return true
}

func asyncTaskAttachmentID(user string, body runtimeRequest) string {
	if id := asyncRef.FindStringSubmatch(user); len(id) == 2 {
		return id[1]
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
	var task, taskID string
	wait(func() bool {
		return db.QueryRowContext(ctx, `SELECT s.id,s.task_id FROM manager_sessions s JOIN manager_inputs i ON i.id=s.initial_input_id WHERE s.conversation_id=$1 AND s.mode='task' AND i.text LIKE $2||'%'`, conversation, asyncTaskRequest).Scan(&task, &taskID) == nil
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
	call("POST", base+"/sessions/"+chat+"/inputs", map[string]any{"text": asyncResume + " task_id=" + taskID, "attachment_ids": []string{id}}, "retry-async-e2e")
	waitForText(task, asyncComplete)
}
