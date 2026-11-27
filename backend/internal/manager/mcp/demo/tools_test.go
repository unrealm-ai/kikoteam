package demo

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPaymentScenarioUsesTraceAndStatelessRecoveryReceipt(t *testing.T) {
	const key, task, inc = "synthetic-receipt-test-key", "task-1", "INC-207"
	if _, err := Run(key, task, "get_incident_logs", Args{CaseID: inc, TraceID: "incorrect"}); err == nil {
		t.Fatal("unrelated trace returned logs")
	}
	log, err := Run(key, task, "get_incident_logs", Args{CaseID: inc, TraceID: "tr-pay-207-a81f"})
	if err != nil || !strings.Contains(log["result"].(string), "SIGNATURE_MISMATCH") {
		t.Fatal(log, err)
	}
	rollback, err := Run(key, task, "rollback_release", Args{CaseID: inc})
	if err != nil {
		t.Fatal(err)
	}
	args := Args{CaseID: inc, Receipt: rollback["rollback_receipt"].(string), Window: "1"}
	if _, err = Run(key, "another-task", "get_incident_metrics", args); err == nil {
		t.Fatal("cross-task receipt accepted")
	}
	for w, value := range map[string]string{"1": "99.4%", "2": "99.6%"} {
		args.Window = w
		metrics, err := Run(key, task, "get_incident_metrics", args)
		if err != nil || !strings.Contains(metrics["result"].(string), value) {
			t.Fatal(metrics, err)
		}
	}
	baseline, err := Run(key, task, "get_incident_metrics", Args{CaseID: inc})
	if err != nil || !strings.Contains(baseline["result"].(string), "91.6%") {
		t.Fatal("stateful baseline", baseline, err)
	}
}

func TestDemoToolSchemaScopesCaseIDsAndKeepsOrderLookupWorking(t *testing.T) {
	ctx := t.Context()
	a, b := sdk.NewInMemoryTransports()
	server, err := Server("fixture-secret").Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	list, err := client.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != len(Specs) {
		t.Fatal(list, err)
	}
	for _, tool := range list.Tools {
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		wire := string(data)
		switch tool.Name {
		case "get_order":
			if !strings.Contains(wire, "ORD-1042") || strings.Contains(wire, "INC-207") || strings.Contains(wire, "trace_id") {
				t.Fatal("order schema crosses scenarios", wire)
			}
		case "get_incident_logs":
			if !strings.Contains(wire, "INC-207") || strings.Contains(wire, "ORD-1042") || !strings.Contains(wire, `"required":["case_id","trace_id"]`) {
				t.Fatal("logs schema missing scope or trace requirement", wire)
			}
		}
	}
	result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: "get_order", Arguments: map[string]string{"case_id": "ORD-1042"}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	result, err = client.CallTool(ctx, &sdk.CallToolParams{Name: "get_meeting_notes", Arguments: map[string]string{"case_id": "ORD-1042"}})
	if err == nil && !result.IsError {
		t.Fatal("meeting lookup accepted an order number")
	}
}
