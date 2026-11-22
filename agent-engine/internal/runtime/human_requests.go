package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

func (w *Worker) fenceHumanRequest(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, steer steering) (string, error) {
	id, err := w.Store.HumanRequestToFence(ctx, work, turn.ID)
	if err != nil || id == "" {
		return "", err
	}
	if err = w.finishSteering(ctx, work, turn, client, steer, false); err != nil {
		return "", err
	}
	return id, w.Store.FenceHumanRequest(ctx, work, turn.ID, id)
}

func (w *Worker) finishHumanRequest(ctx context.Context, writer *eventWriter, client *pi.Client, runner *integration.Runner, id string, problem error) (string, string, string) {
	writer.flush("interrupted")
	writer.finish()
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := w.stopBoundaryRuntime(finish, writer, client, runner); err != nil || problem != nil || writer.err != nil || id != writer.humanRequest {
		return "interrupted", "human_confirmation_unresolved", ""
	}
	return "interrupted", "awaiting_confirmation", ""
}

func (w *Worker) stopBoundaryRuntime(ctx context.Context, writer *eventWriter, client *pi.Client, runner *integration.Runner) error {
	if err := client.Close(); err != nil {
		return err
	}
	if runner != nil {
		if err := w.stopRunner(ctx, writer.work, runner); err != nil {
			return err
		}
	}
	quiet, err := w.Store.CanRetireConfirmation(ctx, writer.work, writer.turn.ID)
	if !quiet && err == nil {
		err = errors.New("session tools have unresolved results")
	}
	return err
}
