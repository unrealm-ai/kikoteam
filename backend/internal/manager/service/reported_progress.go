package service

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const ProgressEvent = "task.progress_updated"
const ProgressInputHeader = "X-Kiko-Progress-Input"
const ProgressAudience = "task-progress"

type ProgressStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

type StepMetric struct {
	StepID string `json:"step_id"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
	Unit   string `json:"unit"`
}

type ReportedProgress struct {
	TurnID      string         `json:"turn_id"`
	Seq         string         `json:"seq"`
	UpdatedAt   time.Time      `json:"occurred_at"`
	PlanVersion string         `json:"plan_version"`
	Summary     string         `json:"summary"`
	Steps       []ProgressStep `json:"steps"`
	Metric      *StepMetric    `json:"metric"`
}

type ProgressUpdate struct {
	BaseVersion string          `json:"base_plan_version"`
	Summary     string          `json:"summary"`
	Steps       *[]ProgressStep `json:"steps,omitempty"`
}

type ProgressStore interface {
	UpdateTaskProgress(context.Context, WorkScope, ProgressUpdate) (*ReportedProgress, error)
}

var progressID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (u ProgressUpdate) NextVersion() (string, error) {
	version, err := strconv.ParseUint(u.BaseVersion, 10, 63)
	invalid := Fail("invalid_progress", 400, "进展摘要、版本或步骤无效")
	if err != nil || version == math.MaxInt64 || strconv.FormatUint(version, 10) != u.BaseVersion || strings.TrimSpace(u.Summary) == "" || utf8.RuneCountInString(u.Summary) > 200 {
		return "", invalid
	}
	if u.Steps == nil {
		return strconv.FormatUint(version+1, 10), nil
	}
	if len(*u.Steps) > 12 {
		return "", invalid
	}
	seen := map[string]bool{}
	for _, step := range *u.Steps {
		if !progressID.MatchString(step.ID) || seen[step.ID] || strings.TrimSpace(step.Label) == "" || utf8.RuneCountInString(step.Label) > 80 {
			return "", invalid
		}
		switch step.Status {
		case "pending", "in_progress", "done", "skipped":
		default:
			return "", invalid
		}
		seen[step.ID] = true
	}
	return strconv.FormatUint(version+1, 10), nil
}
