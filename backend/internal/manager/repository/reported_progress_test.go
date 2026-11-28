package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestMeasuredProgressRequiresCurrentPlanAndContiguousEvidence(t *testing.T) {
	r := progressRecord{ReportedProgress: service.ReportedProgress{PlanVersion: "2", Steps: []service.ProgressStep{{ID: "read", Status: "in_progress"}}}}
	offset := int64(0)
	o := metricObservation{planVersion: "2", executionID: "file-A", metric: service.StepMetric{StepID: "read", Done: 4, Total: 10, Unit: "字节"}, from: &offset}
	if !r.applyMetric(o) || r.applyMetric(o) {
		t.Fatal("first observation and replay")
	}
	offset = 4
	for _, test := range []struct {
		reason string
		change func(*metricObservation)
	}{
		{"old plan", func(v *metricObservation) { v.planVersion = "1" }},
		{"wrong step", func(v *metricObservation) { v.metric.StepID = "other" }},
		{"different execution", func(v *metricObservation) { v.executionID = "file-B" }},
		{"wrong total", func(v *metricObservation) { v.metric.Total = 3 }},
		{"skipped range", func(v *metricObservation) { gap := int64(9); v.from = &gap }},
	} {
		invalid := o
		invalid.metric.Done = 10
		test.change(&invalid)
		if r.applyMetric(invalid) {
			t.Error(test.reason)
		}
	}
	o.metric.Done = 10
	if !r.applyMetric(o) || r.Metric.Done != 10 {
		t.Fatal("last exact range was not counted")
	}
}
