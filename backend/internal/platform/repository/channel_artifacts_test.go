package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
	db, m, store, w, _, _, binding := channelManagerFixture(t)
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
		args := manager.DelegationArgs{Instruction: "produce " + p}
		if appendFiles {
			args.AttachmentIDs = []string{x.box + ":0"}
		}
		res, e := d.Call(ctx, scope, "create_session", args)
		must(e)
		task := res.(map[string]any)["session_id"].(string)
		var input string
		must(db.QueryRow(`SELECT initial_input_id FROM manager_sessions WHERE id=$1`, task).Scan(&input))
		if appendFiles {
			ids := []string{x.box + ":1", x.box + ":0"}
			args := map[string]any{"session_id": task, "attachment_ids": ids}
			first := call(id, args)
			args["text"] = ""
			second := call(id, args)
			if first.ID != second.ID || first.Text != "" || len(first.Attachments) != 2 || first.Attachments[0].ID != ids[0] || first.Attachments[0].Path != "" || second.Receipt == nil || !second.Receipt.Duplicate {
				t.Fatal("attachment-only append", first, second)
			}
		}
		if appendFiles {
			next := receive("alice", "repeat-request-new-message", false)
			reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, next.input, ct)
			fresh := call(id, map[string]any{"session_id": task, "attachment_ids": []string{x.box + ":1", x.box + ":0"}})
			if fresh.Receipt == nil || fresh.Receipt.Duplicate {
				t.Fatal("new authorized input incorrectly deduplicated", fresh)
			}
			reviewExec(t, db, `UPDATE manager_inputs SET status='processed',finished_at=now() WHERE id=$1`, next.input)
		}
		finish(x.chat, x.input, ct)
		tid, tt := start(task, input)
		if appendFiles {
			attachment, conv, err := store.ReadInputAttachment(ctx, tid, x.box+":0")
			must(err)
			if attachment.Status != "ready" || attachment.Path != "uploads/first.csv" {
				t.Fatal(attachment)
			}
			file, _, err := m.OpenProjectFile(conv, attachment.Path)
			must(err)
			body, err := io.ReadAll(file)
			must(file.Close())
			must(err)
			if string(body) != "first.csv" {
				t.Fatal("incorrect channel attachment bytes")
			}
			_, _, err = store.ReadInputAttachment(ctx, tid, x.box+":1")
			assertChannelError(t, err, "attachment_forbidden") // A queued supplement has not yet granted it.
			var lookup *manager.InputAttachmentLookupError
			if !errors.As(err, &lookup) || len(lookup.Recovery.Candidates) != 1 ||
				lookup.Recovery.Candidates[0] != (manager.InputAttachmentCandidate{ID: x.box + ":0", Name: "first.csv"}) {
				t.Fatal("channel recovery included an ungranted or queued attachment", err)
			}
			// The same durable task grant stops being usable when its media source is revoked.
			reviewExec(t, db, `UPDATE manager_channel_inbox SET status='rejected' WHERE id=$1`, x.box)
			_, _, err = store.ReadInputAttachment(ctx, tid, "wrong-attachment-id")
			if !errors.As(err, &lookup) || len(lookup.Recovery.Candidates) != 0 {
				t.Fatal("revoked media was still suggested", err)
			}
			reviewExec(t, db, `UPDATE manager_channel_inbox SET status='accepted' WHERE id=$1`, x.box)
		}
		scopeP := manager.WorkScope{Identity: tid, InputID: input}
		dir := filepath.Join(m.ProjectRoot, "workspaces", w, "projects", "conv-"+x.conv)
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
	assertCandidates := func(err error, code string, want ...manager.Artifact) {
		t.Helper()
		assertChannelError(t, err, code)
		var recovery *manager.ArtifactSelectionError
		if !errors.As(err, &recovery) || len(recovery.Candidates) != len(want) {
			t.Fatalf("incorrect authorized recovery candidates: %v %+v", err, recovery)
		}
		for i, expected := range want {
			if recovery.Candidates[i] != (manager.ArtifactSelectionCandidate{ID: expected.ID, Name: expected.Name}) {
				t.Fatal("candidate outside the current reply or out of order", recovery.Candidates)
			}
		}
		projection, err := json.Marshal(recovery.Candidates)
		must(err)
		for _, secret := range []string{foreign.ID, a.Version, b.Version, "size_bytes", "path", "media_type"} {
			if strings.Contains(string(projection), secret) {
				t.Fatalf("unsafe candidate projection: %s", projection)
			}
		}
	}
	_, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{foreign.ID}})
	assertCandidates(e, "artifact_forbidden", a) // The second same-actor report is still queued.
	reviewExec(t, db, `UPDATE manager_inputs SET status='started',turn_id=$2,context_state='committed' WHERE id=$1`, *q.TargetInputID, chatTurn)
	_, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{a.ID, foreign.ID}})
	assertCandidates(e, "artifact_forbidden", a, b)
	_, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{"not-an-artifact-id"}})
	assertCandidates(e, "invalid_artifact_selection", a, b)
	_, e = d.Call(ctx, scope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{a.ID, a.ID}})
	assertChannelError(t, e, "invalid_artifact_selection")
	var noDuplicates *manager.ArtifactSelectionError
	if errors.As(e, &noDuplicates) {
		t.Fatal("duplicate-only error changed")
	}
	var selected int
	must(db.QueryRow(`SELECT count(*) FROM manager_artifact_selections WHERE chat_turn_id=$1`, chatTurn).Scan(&selected))
	if selected != 0 {
		t.Fatal("a failed selection committed a partial artifact list")
	}
	stale := scope
	stale.Identity.AssignmentID = manager.NewID()
	if _, e = store.SelectArtifacts(ctx, stale, []string{foreign.ID}); e == nil || errors.As(e, &noDuplicates) {
		t.Fatal("stale reply authority recovered candidates")
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
	// A later user query may explicitly offer a prior artifact, but only that
	// exact same-actor offer can be recovered for the new reply.
	resend := receive("alice", "ask-to-resend", false)
	resendID, resendTurn := start(resend.chat, resend.input)
	resendScope, e := store.DelegationSource(ctx, resendID)
	must(e)
	var sourceTurn string
	must(db.QueryRow(`SELECT turn_id FROM manager_task_artifacts WHERE id=$1`, a.ID).Scan(&sourceTurn))
	offered, e := store.OfferArtifacts(ctx, resendScope, sourceTurn)
	must(e)
	if len(offered) != 1 || offered[0].ID != a.ID {
		t.Fatal("valid same-conversation offer missing", offered)
	}
	reviewExec(t, db, `INSERT INTO manager_artifact_offers VALUES($1,$2,$3)`, resendTurn, resend.input, foreign.ID)
	_, e = d.Call(ctx, resendScope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{foreign.ID}})
	assertCandidates(e, "artifact_forbidden", a)
	_, e = d.Call(ctx, resendScope, "artifact_select", manager.DelegationArgs{ArtifactIDs: []string{a.ID}})
	must(e)

}

func TestProviderNativeAttachmentsReachProjectInput(t *testing.T) {
	for _, provider := range []string{"feishu", "dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			db, m, store, _, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			m.ProjectRoot = t.TempDir()
			if provider != "feishu" {
				reviewExec(t, db, `UPDATE platform_channel_bindings SET provider=$2,app_id='bot',external_tenant_id='',bot_open_id='' WHERE id=$1`, binding, provider)
			}
			for _, kind := range []string{"image", "file"} {
				v := inboundFixture(binding, kind, "staff")
				v.Text = ""
				v.BotID = "bot"
				v.SenderIDType = "user_id"
				v.SenderTenant = v.ExternalTenantID
				if provider == "feishu" {
					v.BotID = ""
					v.SenderIDType = ""
				}
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
				var actualPath, id string
				var outputPath *string
				e = db.QueryRow(`SELECT mm.path,i.attachments->0->>'id',i.attachments->0->>'path' FROM manager_channel_inbox n
 JOIN manager_inputs i ON i.id=n.input_id JOIN manager_channel_media mm ON mm.inbox_id=n.id WHERE n.id=$1`, box.ID).Scan(&actualPath, &id, &outputPath)
				if e != nil || actualPath != "uploads/"+kind+".bin" || id != box.ID+":0" || outputPath != nil {
					t.Fatal("project attachment", actualPath, id, outputPath, e)
				}
			}
		})
	}
}
