package demo

import (
	"strings"
	"testing"
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
