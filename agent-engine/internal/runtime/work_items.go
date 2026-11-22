package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func (w *Worker) fenceWorkBoundary(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, steer steering) (string, error) {
	id, err := w.Store.WorkBoundaryToFence(ctx, work, turn.ID)
	if err != nil || id == "" {
		return "", err
	}
	if err = w.finishSteering(ctx, work, turn, client, steer, false); err != nil {
		return "", err
	}
	return id, w.Store.FenceWorkBoundary(ctx, work, turn.ID, id)
}

func (w *Worker) finishWorkBoundary(ctx context.Context, writer *eventWriter, client *pi.Client, runner *integration.Runner, id string, problem error) (string, string, string) {
	writer.flush("interrupted")
	writer.finish()
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := w.stopBoundaryRuntime(finish, writer, client, runner); err != nil || problem != nil || writer.err != nil || id == "" || id != writer.workBoundary {
		return "interrupted", "work_boundary_unresolved", ""
	}
	return "interrupted", "awaiting_work_commit", ""
}

const (
	humanBoundaryKind = "human"
	workBoundaryKind  = "work"
)

type executionBoundary struct{ id, name string }

func (b executionBoundary) kind(writer *eventWriter, stopped bool) string {
	if stopped {
		return ""
	}
	if b.name == workBoundaryKind || writer.workUnresolved {
		return workBoundaryKind
	}
	if b.name == humanBoundaryKind || writer.humanUnresolved {
		return humanBoundaryKind
	}
	return ""
}

func boundaryError(promptErr, confirmationErr error) error {
	if errors.Is(promptErr, pi.ErrToolBatchSettled) {
		promptErr = nil
	}
	return errors.Join(promptErr, confirmationErr)
}

func (w *Worker) fenceBoundary(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, steer steering, humanEnabled, workEnabled bool) (executionBoundary, error) {
	if work.Mode != "task" {
		return executionBoundary{}, nil
	}
	if humanEnabled {
		id, err := w.fenceHumanRequest(ctx, work, turn, client, steer)
		if id != "" || err != nil {
			return executionBoundary{id: id, name: humanBoundaryKind}, err
		}
	}
	if workEnabled {
		id, err := w.fenceWorkBoundary(ctx, work, turn, client, steer)
		if id != "" || err != nil {
			return executionBoundary{id: id, name: workBoundaryKind}, err
		}
	}
	return executionBoundary{}, nil
}
