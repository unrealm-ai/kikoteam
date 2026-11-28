package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type progressRecord struct {
	service.ReportedProgress
	Digest    string `json:"request_digest,omitempty"`
	MetricKey string `json:"metric_key,omitempty"`
}

func readProgress(ctx context.Context, tx *sql.Tx, src workSource, version string) (progressRecord, error) {
	query := `SELECT event FROM manager_session_events WHERE session_id=$1 AND event->>'turn_id'=$2 AND event->>'type'='task.progress_updated'`
	args := []any{src.session, src.turn}
	if version != "" {
		query += " AND event->>'source'='agent' AND event->>'plan_version'=$3"
		args = append(args, version)
	}
	var data []byte
	err := tx.QueryRowContext(ctx, query+" ORDER BY seq DESC LIMIT 1", args...).Scan(&data)
	var rec progressRecord
	if errors.Is(err, sql.ErrNoRows) {
		rec.PlanVersion = "0"
		rec.Steps = []service.ProgressStep{}
		return rec, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &rec)
	}
	return rec, err
}

func appendProgress(ctx context.Context, tx *sql.Tx, src workSource, rec *progressRecord, source string) error {
	fields := map[string]any{"source": source, "plan_version": rec.PlanVersion, "summary": rec.Summary, "steps": rec.Steps, "metric": rec.Metric}
	if rec.MetricKey != "" {
		fields["metric_key"] = rec.MetricKey
	}
	if source == "agent" {
		fields["request_digest"] = rec.Digest
	}
	writer := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: src.workspace, Session: src.session}}
	if err := writer.Event(service.ProgressEvent, &src.input, &src.turn, fields); err != nil {
		return err
	}
	rec.Seq = fields["seq"].(string)
	rec.UpdatedAt = fields["occurred_at"].(time.Time)
	rec.TurnID = src.turn
	return nil
}

func (s *Store) UpdateTaskProgress(ctx context.Context, scope service.WorkScope, update service.ProgressUpdate) (*service.ReportedProgress, error) {
	next, err := update.NextVersion()
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit releases the transaction.
	src, err := lockWorkSource(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	current, err := readProgress(ctx, tx, src, "")
	if err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(canonical)
	digest := hex.EncodeToString(hash[:])
	if current.PlanVersion != update.BaseVersion {
		original, err := readProgress(ctx, tx, src, next)
		if err != nil {
			return nil, err
		}
		if original.Digest == digest {
			return &original.ReportedProgress, nil
		}
		return &current.ReportedProgress, service.Fail("progress_conflict", 409, "计划版本已变化，请按当前计划更新")
	}
	current.Digest = digest
	current.PlanVersion = next
	current.Summary = update.Summary
	if update.Steps != nil {
		current.Steps = *update.Steps
	}
	if current.Metric != nil && !current.activeStep(current.Metric.StepID) {
		current.Metric = nil
		current.MetricKey = ""
	}
	if err = appendProgress(ctx, tx, src, &current, "agent"); err != nil {
		return nil, err
	}
	return &current.ReportedProgress, tx.Commit()
}

func (r progressRecord) activeStep(id string) bool {
	for _, step := range r.Steps {
		if step.ID == id {
			return step.Status == "in_progress"
		}
	}
	return false
}

// metricObservation is produced by a measuring executor, never from the Agent's
// plan input. paged readers supply from so jumping over a range cannot count it.
type metricObservation struct {
	planVersion, executionID string
	metric                   service.StepMetric
	from                     *int64
}

func (r *progressRecord) applyMetric(o metricObservation) bool {
	m := o.metric
	if o.planVersion != r.PlanVersion || !r.activeStep(m.StepID) || o.executionID == "" || m.Unit == "" || m.Total <= 0 || m.Done < 0 || m.Done > m.Total {
		return false
	}
	var previous int64
	if r.Metric != nil {
		if r.Metric.StepID == m.StepID {
			if r.MetricKey != o.executionID || r.Metric.Total != m.Total || r.Metric.Unit != m.Unit || m.Done <= r.Metric.Done {
				return false
			}
			previous = r.Metric.Done
		} else if r.Metric.Done < r.Metric.Total {
			return false
		}
	}
	if o.from != nil && *o.from != previous {
		return false
	}
	r.Metric = &m
	r.MetricKey = o.executionID
	return true
}

func recordStepMetric(ctx context.Context, tx *sql.Tx, src workSource, o metricObservation) error {
	current, err := readProgress(ctx, tx, src, "")
	if err != nil {
		return err
	}
	if !current.applyMetric(o) {
		return nil
	}
	return appendProgress(ctx, tx, src, &current, "executor")
}
