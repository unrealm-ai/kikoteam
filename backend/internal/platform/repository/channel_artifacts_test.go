package repository

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
)

func TestChannelMediaOnlyAppendAndMergedReportTargets(t *testing.T) {
	db, m, store, w, _, agent, binding := channelManagerFixture(t)
	ctx := t.Context()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	m.ProjectRoot = t.TempDir()
	store.RuntimeRoot = m.ProjectRoot
	assets, e := skillasset.NewStore(ctx, skillasset.Config{LocalRoot: t.TempDir()})
	must(e)
	store.WorkAssets = assets
	engine := manager.NewID()
	must(store.RegisterEngine(ctx, manager.Engine{ID: engine, HostID: "attachment-test", Endpoint: "http://127.0.0.1:9100", Capacity: 16}))
	type message struct{ box, chat, conv, input, principal string }
	receive := func(sender, event string, files bool) message {
		t.Helper()
		v := inboundFixture(binding, event, sender)
		if files {
			v.MessageType = "file"
			v.Text = ""
			v.Media = []manager.MediaReference{{Kind: "file", Name: "first.csv", Locator: "sealed1"}, {Kind: "file", Name: "second.csv", Locator: "sealed2"}}
		}
		box, e := store.ReceiveChannel(ctx, v)
		must(e)
		again, e := store.ReceiveChannel(ctx, v)
		must(e)
		if again.ID != box.ID || again.Status != "duplicate" {
			t.Fatal("duplicate callback", again)
		}
		must(store.ProcessChannelInbox(ctx, m))
		if files {
			for i := range 2 {
				j, e := store.ClaimMedia(ctx, m)
				must(e)
				if j == nil || j.Ordinal != i {
					t.Fatal("media job", j)
				}
				must(store.FinishMedia(ctx, m, j.InboxID, j.Ordinal, j.Lease, []byte(j.Name), false))
			}
			reviewExec(t, db, `UPDATE manager_channel_inbox SET lease_until=NULL WHERE id=$1`, box.ID)
			must(store.ProcessChannelInbox(ctx, m))
		}
		x := message{box: box.ID}
		must(db.QueryRow(`SELECT n.conversation_id,n.principal_id,n.input_id,i.session_id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, x.box).Scan(&x.conv, &x.principal, &x.input, &x.chat))
		return x
	}
	start := func(sess, input string) (manager.DelegationIdentity, string) {
		t.Helper()
		id := manager.DelegationIdentity{SessionID: sess, EngineID: engine, AssignmentID: manager.NewID(), Generation: 1}
		turn := manager.NewID()
		reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status) SELECT $1,$2,$3,$4,last_turn_no+1,$5,1,'running' FROM manager_sessions WHERE id=$3`, turn, w, sess, input, id.AssignmentID)
		reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, input, turn)
		reviewExec(t, db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1,active_turn_id=$4,last_turn_no=last_turn_no+1 WHERE id=$1`, sess, engine, id.AssignmentID, turn)
		return id, turn
	}
	finish := func(sess, input, turn string) {
		t.Helper()
		reviewExec(t, db, `UPDATE manager_turns SET status='completed',finished_at=now(),result_summary='done' WHERE id=$1`, turn)
		reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, input)
		reviewExec(t, db, `UPDATE manager_sessions SET active_turn_id=NULL WHERE id=$1`, sess)
	}
	d := manager.Delegation{Manager: m, Store: store, Secret: strings.Repeat("s", 40)}
	h, e := taskdelegation.Handler(&d, nil)
	must(e)
	call := func(id manager.DelegationIdentity, args any) manager.Input {
		t.Helper()
		token, e := manager.SignDelegation(d.Secret, id)
		must(e)
		b, e := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "task_delegation_send_message", "arguments": args}})
		must(e)
		r := httptest.NewRequest("POST", "/mcp/task-delegation", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out struct {
			Result struct {
				Input manager.Input `json:"structuredContent"`
			}
		}
		must(json.Unmarshal(rec.Body.Bytes(), &out))
		if out.Result.Input.ID == "" {
			t.Fatal(rec.Code, rec.Body.String())
		}
		return out.Result.Input
	}
	makeTask := func(x message, p string, appendFiles bool) (manager.Artifact, manager.TaskReport) {
		t.Helper()
		id, ct := start(x.chat, x.input)
		scope, e := store.DelegationSource(ctx, id)
		must(e)
		res, e := d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "produce " + p})
		must(e)
		task := res.(map[string]any)["session_id"].(string)
		var input string
		must(db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task).Scan(&input))
		if appendFiles {
			paths := []string{"uploads/second.csv", "uploads/first.csv"}
			args := map[string]any{"session_id": task, "attachment_paths": paths}
			first := call(id, args)
			args["text"] = ""
			second := call(id, args)
			if first.ID != second.ID || first.Text != "" || len(first.Attachments) != 2 || first.Attachments[0].Path != paths[0] || second.Receipt == nil || !second.Receipt.Duplicate {
				t.Fatal("attachment-only append", first, second)
			}
		}
		if appendFiles {
			next := receive("alice", "repeat-request-new-message", false)
			reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, next.input, ct)
			fresh := call(id, map[string]any{"session_id": task, "attachment_paths": []string{"uploads/second.csv", "uploads/first.csv"}})
			if fresh.Receipt == nil || fresh.Receipt.Duplicate {
				t.Fatal("new authorized input incorrectly deduplicated", fresh)
			}
			reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, next.input)
		}
		finish(x.chat, x.input, ct)
		tid, tt := start(task, input)
		scopeP := manager.WorkScope{Identity: tid, InputID: input}
		dir := filepath.Join(m.ProjectRoot, "state/sandboxes/workspaces", w, "employees", agent, "conversations", x.conv, "project")
		must(os.MkdirAll(dir, 0700))
		must(os.WriteFile(filepath.Join(dir, p), []byte(p), 0600))
		mux := http.NewServeMux()
		must(workitems.Register(mux, store, d.Secret))
		publishToken, e := manager.SignWorkTool(d.Secret, "mcp-ordinary", scopeP.InputID, tid)
		must(e)
		body, e := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "artifact_publish", "arguments": map[string]string{"path": p}}})
		must(e)
		req := httptest.NewRequest("POST", "/mcp/work-items/ordinary", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+publishToken)
		req.Header.Set(manager.WorkInputHeader, input)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var result struct {
			Result struct {
				Artifact manager.Artifact `json:"structuredContent"`
			}
		}
		must(json.Unmarshal(rec.Body.Bytes(), &result))
		a := result.Result.Artifact
		if a.ID == "" {
			t.Fatal("missing published artifact", rec.Body.String())
		}
		must(os.WriteFile(filepath.Join(dir, p), []byte("overwritten"), 0600))
		finish(task, input, tt)
		if p == "b.txt" {
			reviewExec(t, db, `UPDATE manager_turns SET status='failed' WHERE id=$1`, tt)
		}
		r, e := store.ReportTaskResult(ctx, manager.TaskResult{TurnID: tt})
		must(e)
		r2, e := store.ReportTaskResult(ctx, manager.TaskResult{TurnID: tt})
		must(e)
		if r.ID != r2.ID || r.TargetInputID == nil {
			t.Fatal("report duplicate", r, r2)
		}
		return a, r
	}
	x := receive("alice", "first", true)
	a, r := makeTask(x, "a.txt", true)
	y := receive("alice", "second", false)
	b, q := makeTask(y, "b.txt", false)
	z := receive("bob", "third", false)
	foreign, _ := makeTask(z, "c.txt", false)
	id, chatTurn := start(x.chat, *r.TargetInputID)
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, *q.TargetInputID, chatTurn)
	scope, e := store.DelegationSource(ctx, id)
	must(e)
	if !scope.ReportOnly {
		t.Fatal("report tool scope", scope)
	}
	var reportBody string
	must(db.QueryRow(`SELECT text FROM manager_inputs WHERE id=$1`, *r.TargetInputID).Scan(&reportBody))
	if !strings.Contains(reportBody, a.ID) || !strings.Contains(reportBody, a.Version) || strings.Contains(reportBody, b.ID) {
		t.Fatal("initial report candidates escaped source turn", reportBody)
	}
	token, e := manager.SignDelegation(d.Secret, id)
	must(e)
	list := httptest.NewRequest("POST", "/mcp/task-delegation?mode=report", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	list.Header.Set("Authorization", "Bearer "+token)
	list.Header.Set("Content-Type", "application/json")
	list.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, list)
	var inventory struct {
		Result struct{ Tools []struct{ Name string } }
	}
	must(json.Unmarshal(rec.Body.Bytes(), &inventory))
	if len(inventory.Result.Tools) != 1 || inventory.Result.Tools[0].Name != "artifact_select" {
		t.Fatal("unexpected report tools", rec.Body.String())
	}
	if _, e = d.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "invalid report write"}); e == nil {
		t.Fatal("pure report delegated another task")
	}
	if _, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{a.ID, foreign.ID}}); e == nil {
		t.Fatal("accepted candidate from another conversation")
	}
	for range 2 {
		_, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{b.ID, a.ID}})
		must(e)
	}
	finish(x.chat, *r.TargetInputID, chatTurn)
	for range 2 {
		must(store.MaterializeChannelReplies(ctx))
	}
	rows, e := db.Query(`SELECT artifact_id,inbox_id FROM manager_channel_deliveries WHERE artifact_id IS NOT NULL`)
	must(e)
	defer func() { _ = rows.Close() }()
	var count int
	wants := map[string]string{a.ID: x.box, b.ID: y.box}
	for rows.Next() {
		var artifact, inbox string
		must(rows.Scan(&artifact, &inbox))
		if wants[artifact] != inbox {
			t.Fatalf("wrong target %s %s", artifact, inbox)
		}
		count++
	}
	must(rows.Err())
	must(rows.Close())
	if count != 2 {
		t.Fatal("artifact delivery count", count)
	}
	legacy, e := store.ClaimChannelDeliveries(ctx, 50, true)
	must(e)
	for _, d := range legacy {
		if d.ArtifactID != "" {
			t.Fatal("legacy hub leased media")
		}
	}
	batch, e := store.ClaimChannelDeliveries(ctx, 50, true, true)
	must(e)
	if len(batch) != 2 {
		t.Fatalf("expected both media deliveries, got %d", len(batch))
	}
	for _, delivery := range batch {
		if delivery.ArtifactID == "" {
			continue
		}
		auth, e := store.AuthorizeChannelDelivery(ctx, delivery.ID, delivery.LeaseToken)
		must(e)
		version, data, e := store.ReadDeliveryArtifact(ctx, auth.ID, auth.LeaseToken, true)
		must(e)
		if string(data) != version.Name || version.ID != auth.ArtifactID || auth.ChatID != "chat-alice" {
			t.Fatal("wrong frozen bytes or target", version, auth)
		}
	}
}

func TestProviderNativeAttachmentsReachProjectInput(t *testing.T) {
	for _, provider := range []string{"dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			db, m, store, _, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			m.ProjectRoot = t.TempDir()
			reviewExec(t, db, `UPDATE platform_channel_bindings SET provider=$2,app_id='bot',external_tenant_id='',bot_open_id='' WHERE id=$1`, binding, provider)
			for _, kind := range []string{"image", "file"} {
				v := inboundFixture(binding, kind, "staff")
				v.Text = ""
				v.BotID = "bot"
				v.SenderIDType = "user_id"
				v.SenderTenant = v.ExternalTenantID
				v.MessageType = kind
				if provider == "dingtalk" && kind == "image" {
					v.MessageType = "picture"
				}
				if provider == "wecom" {
					v.ExternalTenantID = "bot"
					v.ExternalChatID = v.SenderID
					v.ProviderDirected = true
				}
				v.Media = []manager.MediaReference{{Kind: kind, Name: kind + ".bin", Locator: "sealed"}}
				box, e := store.ReceiveChannel(ctx, v)
				if e != nil {
					t.Fatal(e)
				}
				v.Media[0].Locator = "freshly-encrypted-replay"
				dupe, e := store.ReceiveChannel(ctx, v)
				if e != nil || dupe.ID != box.ID || dupe.Status != "duplicate" {
					t.Fatal("callback", dupe, e)
				}
				if e = store.ProcessChannelInbox(ctx, m); e != nil {
					t.Fatal(e)
				}
				j, e := store.ClaimMedia(ctx, m)
				if e != nil || j == nil || j.Locator != "sealed" || j.Kind != kind {
					t.Fatal("claim", j, e)
				}
				if e = store.FinishMedia(ctx, m, j.InboxID, j.Ordinal, j.Lease, []byte(kind), false); e != nil {
					t.Fatal(e)
				}
				reviewExec(t, db, `UPDATE manager_channel_inbox SET lease_until=NULL WHERE id=$1`, box.ID)
				if e = store.ProcessChannelInbox(ctx, m); e != nil {
					t.Fatal(e)
				}
				var path string
				if e = db.QueryRow(`SELECT i.attachments->0->>'path' FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, box.ID).Scan(&path); e != nil || path != "uploads/"+kind+".bin" {
					t.Fatal("project attachment", path, e)
				}
			}
		})
	}
}
