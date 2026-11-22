package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
	"strings"
	"testing"
)

func runtimeChannelAcceptance(t *testing.T, ctx context.Context, db *sql.DB, store *managerrepo.Store, svc *manager.Service, w, agent, backend, hubToken string, wait func(func() bool)) {
	t.Helper()
	binding := manager.NewID()
	if _, e := db.ExecContext(ctx, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,credential_ciphertext,trigger,history,access) VALUES($1,$2,$3,'feishu',1,1,1,true,'cli_e2e','tenant',decode('00','hex'),'{"mention":true,"keywords":[],"regexes":[]}','{"enabled":true,"limit":100}','{"mode":"all","allow_ids":[],"deny_ids":[]}')`, binding, w, agent); e != nil {
		t.Fatal(e)
	}
	post := func(path string, value, out any) {
		t.Helper()
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.NewRequestWithContext(ctx, "POST", backend+path, bytes.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+hubToken)
		if path == "/api/internal/v1/channel-deliveries/claim" {
			r.Header.Set("X-Kiko-Delivery-Markdown", "1")
		}
		r.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close() //nolint:errcheck // JSON decoding reports read errors.
		if resp.StatusCode != 200 {
			var failure any
			if decodeErr := json.NewDecoder(resp.Body).Decode(&failure); decodeErr != nil {
				failure = "unreadable error response"
			}
			t.Fatalf("%s returned %d: %v", path, resp.StatusCode, failure)
		}
		if out != nil {
			if e = json.NewDecoder(resp.Body).Decode(out); e != nil {
				t.Fatal(e)
			}
		}
	}
	incoming := inboundFixture(binding, "e2e-trigger-1", "open-independent")
	incoming.ChatType = "group"
	incoming.ExternalChatID = "e2e-group"
	incoming.BotMentioned = true
	incoming.CreatedAtMS = 1000
	incoming.Text = "channel first message"
	var receipt manager.ChannelReceipt
	post("/api/internal/v1/channel-inbox", incoming, &receipt)
	if e := store.ProcessChannelInbox(ctx, svc); e != nil {
		t.Fatal(e)
	}
	var jobs struct {
		Items []manager.ChannelHistoryJob `json:"items"`
	}
	post("/api/internal/v1/channel-history/claim", map[string]int{"limit": 1}, &jobs)
	if len(jobs.Items) != 1 {
		t.Fatal(jobs)
	}
	job := jobs.Items[0]
	post("/api/internal/v1/channel-history/"+job.ID+"/authorize", map[string]string{"lease_token": job.LeaseToken}, &job)
	post("/api/internal/v1/channel-history/"+job.ID+"/complete", manager.ChannelHistoryCompletion{LeaseToken: job.LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{}}}, nil)
	if e := store.ProcessChannelInbox(ctx, svc); e != nil {
		t.Fatal(e)
	}
	wait(func() bool {
		var state, status string
		e := db.QueryRowContext(ctx, `SELECT COALESCE(i.context_state,''),COALESCE(t.status,'') FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE n.id=$1`, receipt.ID).Scan(&state, &status)
		return e == nil && state == "committed" && status == "completed"
	})
	if e := store.MaterializeChannelReplies(ctx); e != nil {
		t.Fatal(e)
	}
	var deliveries struct {
		Items []manager.ChannelDelivery `json:"items"`
	}
	post("/api/internal/v1/channel-deliveries/claim", map[string]int{"limit": 5}, &deliveries)
	if len(deliveries.Items) != 1 {
		t.Fatal(deliveries)
	}
	delivery := deliveries.Items[0]
	post("/api/internal/v1/channel-deliveries/"+delivery.ID+"/authorize", map[string]string{"lease_token": delivery.LeaseToken}, &delivery)
	if !delivery.Allowed || delivery.ChatID != "e2e-group" || delivery.Format != "markdown" || !strings.Contains(delivery.Text, "reply:") {
		t.Fatal(delivery)
	}
	post("/api/internal/v1/channel-deliveries/"+delivery.ID+"/complete", manager.DeliveryCompletion{LeaseToken: delivery.LeaseToken, Status: "sent", TargetDigest: delivery.TargetDigest, EvidenceType: "send_response", EvidenceID: "mock-provider-sent", ProviderMessageID: "mock-provider-sent"}, nil)
	// A second trigger uses the native-committed first trigger, excluding it.
	incoming.ProviderEventID = "e2e-trigger-2"
	incoming.ProviderMessageID = "e2e-trigger-2"
	incoming.CreatedAtMS = 2000
	incoming.Text = "channel next message"
	post("/api/internal/v1/channel-inbox", incoming, &receipt)
	if e := store.ProcessChannelInbox(ctx, svc); e != nil {
		t.Fatal(e)
	}
	jobs.Items = nil
	post("/api/internal/v1/channel-history/claim", map[string]int{"limit": 1}, &jobs)
	if len(jobs.Items) != 1 || jobs.Items[0].PreviousTriggerMessageID != "e2e-trigger-1" {
		t.Fatal("native receipt not reflected", jobs)
	}
	job = jobs.Items[0]
	post("/api/internal/v1/channel-history/"+job.ID+"/complete", manager.ChannelHistoryCompletion{LeaseToken: job.LeaseToken, Snapshot: manager.ChannelHistorySnapshot{Status: "complete", Items: []manager.ChannelHistoryItem{{MessageID: "between", SenderID: "other", SenderType: "user", MessageType: "text", Text: "quoted interval", CreatedAtMS: 1500}}}}, nil)
	if e := store.ProcessChannelInbox(ctx, svc); e != nil {
		t.Fatal(e)
	}
	wait(func() bool {
		var state, status string
		e := db.QueryRowContext(ctx, `SELECT COALESCE(i.context_state,''),COALESCE(t.status,'') FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE n.id=$1`, receipt.ID).Scan(&state, &status)
		return e == nil && state == "committed" && status == "completed"
	})
}
