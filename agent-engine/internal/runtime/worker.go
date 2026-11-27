package runtime

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

type Worker struct {
	Store              *repository.Store
	Backend            *integration.Backend
	Sandboxes          *Sandboxes
	Root, Pi, Launcher string
	Idle               time.Duration
	Log                *slog.Logger
}

func (w *Worker) Run(ctx context.Context, a repository.Assignment) {
	var client *pi.Client
	var priorModel string
	var runner *integration.Runner
	harnessID := NewID()
	var current repository.Work
	lastWork := time.Now()
	defer func() {
		w.cleanupSession(ctx, current, client, runner)
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		value, err := w.Store.Refresh(ctx, a)
		if errors.Is(err, repository.ErrStale) {
			return
		}
		if err != nil {
			w.Log.Warn("Session state temporarily unavailable", "event", "session.read_failed", "session_id", a.SessionID)
			if !wait(ctx, ticker) {
				return
			}
			continue
		}
		current = value
		if w.reconcilePreviousTurn(ctx, current) {
			wait(ctx, ticker)
			continue
		}
		client, err = closeUnresolvedPi(current, client)
		if err != nil {
			w.Log.Error("Pi close unresolved after remote call", "event", "session.cleanup_failed", "session_id", a.SessionID)
			return
		}
		if current.Mode == "task" && runner == nil && current.Lifecycle == "active" && current.ParentLifecycle == "active" {
			runner, err = w.prepareRunner(ctx, current)
			if err != nil {
				runner = nil
				w.Log.Warn("Sandbox preparation pending", "event", "sandbox.prepare_pending", "session_id", a.SessionID)
				if e := w.recordActivity(ctx, current, integration.Activity{}, err); e != nil {
					w.Log.Warn("Sandbox activity report pending", "event", "session.activity_pending", "session_id", a.SessionID)
				}
				if !wait(ctx, ticker) {
					return
				}
				continue
			}
		}
		if current.StopRevision > current.AppliedStopRevision && current.ActiveTurnID == nil {
			if runner != nil {
				if err = w.stopRunner(ctx, current, runner); err != nil {
					if !wait(ctx, ticker) {
						return
					}
					continue
				}
			}
			if err = w.Store.AppliedStop(ctx, current, current.StopRevision); err != nil {
				w.Log.Warn("Stop confirmation pending", "event", "session.stop_pending", "session_id", a.SessionID)
			}
			continue
		}
		if current.Lifecycle == "archived" || current.ParentLifecycle == "archived" {
			return
		}
		if !w.ready(ctx, &current, runner) {
			wait(ctx, ticker)
			continue
		}
		turn, err := w.Store.Claim(ctx, current, NewID())
		if err != nil {
			if errors.Is(err, repository.ErrStale) {
				return
			}
			w.Log.Warn("Input claim temporarily unavailable", "event", "input.claim_failed", "session_id", a.SessionID)
			if !wait(ctx, ticker) {
				return
			}
			continue
		}
		if turn == nil {
			if time.Since(lastWork) > w.Idle && current.BackgroundCount != nil && *current.BackgroundCount == 0 {
				return
			}
			if !wait(ctx, ticker) {
				return
			}
			continue
		}
		w.logInputClaim(current, *turn, turn.Input, "turn")
		lastWork = time.Now()
		var ok bool
		client, priorModel, harnessID, ok = w.execute(ctx, current, *turn, client, priorModel, harnessID, runner)
		if !ok {
			return
		}
	}
}

func (w *Worker) reconcilePreviousTurn(ctx context.Context, current repository.Work) bool {
	if current.ActiveTurnID == nil {
		return false
	}
	interrupted, err := w.Store.InterruptPreviousTurn(ctx, current)
	if err != nil {
		w.Log.Warn("Interrupted Turn reconciliation pending", "event", "turn.recovery_pending", "session_id", current.SessionID)
		return true
	}
	if interrupted && current.ReportToChat != nil {
		if err := w.Backend.ReportTask(ctx, *current.ActiveTurnID); err != nil {
			w.Log.Warn("Task report awaits Hub reconciliation", "event", "task.report_pending", "turn_id", *current.ActiveTurnID)
		}
	}
	return interrupted
}

func (w *Worker) prepareRunner(ctx context.Context, current repository.Work) (*integration.Runner, error) {
	runner, err := w.Sandboxes.For(ctx, current)
	if err == nil && current.Generation > 1 {
		// Open the persistent activity ledger before observing the state of a
		// replacement Runner, whose in-memory bindings start empty.
		_, err = runner.Prepare(ctx, current.SessionID)
	}
	return runner, err
}

func closeUnresolvedPi(work repository.Work, client *pi.Client) (*pi.Client, error) {
	if client == nil || work.BlockReason == nil || *work.BlockReason != "remote_tool_outcome_unknown" {
		return client, nil
	}
	if err := client.Close(); err != nil {
		return client, err
	}
	return nil, nil
}

func (w *Worker) ready(ctx context.Context, current *repository.Work, runner *integration.Runner) bool {
	if runner != nil {
		state, e := runner.State(ctx, current.SessionID)
		if err := w.recordActivity(ctx, *current, state, e); err != nil || e != nil || !state.Verified || state.Unknown > 0 {
			return false
		}
		count := state.Active
		current.BackgroundCount = &count
	}
	return current.BlockReason == nil && current.BackgroundCount != nil
}

func (w *Worker) cleanupSession(ctx context.Context, current repository.Work, client *pi.Client, runner *integration.Runner) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if client != nil {
		if err := client.Close(); err != nil {
			w.Log.Error("Pi cleanup failed", "event", "session.cleanup_failed", "session_id", current.SessionID)
			return
		}
	}
	if current.SessionID == "" {
		return
	}
	if runner != nil {
		state, releaseErr := runner.Release(cleanup, current.SessionID)
		if err := w.recordActivity(cleanup, current, state, releaseErr); err != nil || releaseErr != nil || !state.Quiet() {
			w.Log.Error("Session tool cleanup unresolved", "event", "session.cleanup_failed", "session_id", current.SessionID)
			return
		}
	}
	latest, err := w.Store.Refresh(cleanup, current.Assignment)
	if err != nil {
		return
	}
	if latest.StopRevision > latest.AppliedStopRevision {
		if err = w.Store.AppliedStop(cleanup, latest, latest.StopRevision); err != nil {
			w.Log.Warn("Stop confirmation pending", "event", "session.stop_pending", "session_id", current.SessionID)
			return
		}
	}
	paths := []string{"temp"}
	if current.Mode == "task" {
		paths = append(paths, "context")
	}
	for _, kind := range paths {
		dir, err := sessionNodePath(kind, current.SessionID)
		if err == nil {
			err = os.RemoveAll(dir)
		}
		if err != nil {
			w.Log.Warn("Session directory cleanup failed", "kind", kind, "session_id", current.SessionID, "error", err)
		}
	}
	if err := w.Backend.Release(cleanup, current); err != nil {
		w.Log.Warn("Assignment release awaits reconciliation", "event", "session.release_pending", "session_id", current.SessionID)
	}
}

func (w *Worker) execute(ctx context.Context, current repository.Work, turn repository.Turn, client *pi.Client, prior string, harness string, runner *integration.Runner) (*pi.Client, string, string, bool) {
	cfg, err := w.Backend.Config(ctx, current, turn.Input.ID)
	var endpoint integration.Endpoint
	if err == nil && runner != nil && !turn.Input.CorrectionOnly {
		endpoint, err = runner.Prepare(ctx, current.SessionID)
	}
	var prepared pi.Config
	var signature string
	if err == nil {
		prepared, signature, err = w.prepareConfiguration(ctx, current, turn, cfg, endpoint)
	}
	starting := err == nil && (client == nil || !client.Alive() || prior != signature)
	if starting {
		if client != nil {
			err = client.Close()
			client = nil
		}
		if err == nil {
			err = prepared.PrepareHumanTools()
		}
		if err == nil {
			harness = NewID()
			nativeID := ""
			if current.NativeSessionID != nil {
				nativeID = *current.NativeSessionID
			}
			client, err = pi.Start(ctx, prepared, nativeID)
			if err == nil {
				prior = signature
				err = w.Store.SetNative(ctx, current, client.SessionID())
				if err != nil {
					reason := "native_identity_uncommitted"
					if e := w.Store.Activity(ctx, current, nil, &reason); e != nil {
						w.Log.Error("Native identity commit unresolved", "event", "session.native_unresolved", "session_id", current.SessionID)
					}
				}
			}
		}
	}
	status, code, summary := "failed", "configuration_unusable", ""
	if err == nil && runner != nil && !turn.Input.CorrectionOnly {
		err = runner.Open(ctx, current, turn.ID, harness)
	}
	if err == nil && !starting {
		// Publish the next credentials only after the previous prompt settled
		// and Runner admitted this Turn in the same harness.
		err = prepared.PrepareHumanTools()
	}
	if err == nil {
		status, code, summary = w.prompt(ctx, current, turn, client, runner, renderPromptInput(current, cfg, turn.Input.Text), prepared.HumanTools.Token != "", prepared.HumanTools.WorkToken != "", current.Mode == "chat" && cfg.HumanMCPToken != "")
	}
	if ctx.Err() != nil {
		status = "interrupted"
		code = "engine_shutdown"
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if runner != nil {
		state, e := runner.State(finish, current.SessionID)
		if e = w.recordActivity(finish, current, state, e); e != nil {
			w.Log.Warn("Activity report pending", "event", "session.activity_pending", "session_id", current.SessionID)
		}
	}
	if err != nil && turn.Input.NeedsContextReceipt {
		if err = w.Store.ContextReceipt(finish, current, turn.Input.ID, "absent"); err != nil {
			w.Log.Error("Context receipt commit failed", "event", "input.context_pending", "session_id", current.SessionID)
			return client, prior, harness, false
		}
	}
	if err = w.Store.Complete(finish, current, turn, status, code, summary); err != nil {
		w.Log.Error("Turn result commit failed", "event", "turn.commit_failed", "session_id", current.SessionID, "turn_id", turn.ID)
		return client, prior, harness, false
	}
	w.Log.Info("Turn finished", "event", "turn.finished", "session_id", current.SessionID, "turn_id", turn.ID, "input_id", turn.Input.ID, "status", status, "error_code", code)
	if current.Mode == "task" && current.ReportToChat != nil && code != "awaiting_work_commit" && code != "work_boundary_unresolved" {
		if err = w.Backend.ReportTask(finish, turn.ID); err != nil {
			w.Log.Warn("Task report awaits Hub reconciliation", "event", "task.report_pending", "session_id", current.SessionID, "turn_id", turn.ID)
		}
	}
	return client, prior, harness, true
}

func (w *Worker) recordActivity(ctx context.Context, work repository.Work, state integration.Activity, err error) error {
	if err != nil || !state.Verified || state.Unknown > 0 || state.Active == 0 && state.PendingWrites > 0 {
		reason := "sandbox_activity_unknown"
		return w.Store.Activity(ctx, work, nil, &reason)
	}
	return w.Store.Activity(ctx, work, &state.Active, nil)
}
func (w *Worker) stopRunner(ctx context.Context, work repository.Work, runner *integration.Runner) error {
	state, err := runner.Stop(ctx, work.SessionID)
	if e := w.recordActivity(ctx, work, state, err); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	if !state.Quiet() {
		return errors.New("runner cleanup is not quiet")
	}
	return nil
}
func wait(ctx context.Context, t *time.Ticker) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (w *Worker) prompt(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, runner *integration.Runner, inputText string, humanEnabled, workEnabled, humanReplyEnabled bool) (string, string, string) {
	latest, err := w.Store.Refresh(ctx, work.Assignment)
	if err != nil {
		return "failed", "execution_state_unavailable", ""
	}
	if promptStopped(latest, turn) {
		return "canceled", "", ""
	}
	promptCtx, cancel := context.WithCancelCause(ctx)
	executionDeadline := time.Now().Add(10 * time.Minute)
	defer cancel(nil)
	steeringState := &steering{}
	root := turn.Input
	root.Text = inputText
	steeringState.pending.Store(&root)
	writer := &eventWriter{ctx: ctx, store: w.Store, work: work, turn: turn, tools: map[string]*toolState{}}
	done := make(chan error, 1)
	requests := make(chan pi.Update)
	pending := make(map[string]nativeConfirmation)
	parked := false
	var confirmErr error
	var boundary executionBoundary
	go func() {
		done <- w.readPrompt(ctx, promptCtx, cancel, writer, client, inputText, requests, steeringState)
	}()
	check := time.NewTicker(250 * time.Millisecond)
	defer check.Stop()
	stopped := false
	for {
		select {
		case update := <-requests:
			request, e := openConfirmation(promptCtx, w.Store, work, turn.ID, update)
			if e != nil {
				confirmErr = e
				cancel(promptFailure("confirmation_open", e))
				continue
			}
			pending[request.action.RequestID] = request
		case err := <-done:
			switch boundary.kind(writer, stopped) {
			case workBoundaryKind:
				return w.finishWorkBoundary(ctx, writer, client, runner, boundary.id, boundaryError(err, confirmErr))
			case humanBoundaryKind:
				return w.finishHumanRequest(ctx, writer, client, runner, boundary.id, boundaryError(err, confirmErr))
			}
			status, code, summary := w.finishPrompt(ctx, writer, client, steeringState, len(pending) > 0, parked, stopped, err, confirmErr)
			if status == "failed" {
				w.logPromptFailure(ctx, work, turn, context.Cause(promptCtx), errors.Join(err, confirmErr, writer.err))
			}
			if turn.Input.CorrectionOnly && status == "completed" {
				return "failed", "human_reapproval_required", summary
			}
			return status, code, summary
		case <-check.C:
			latest, err := w.Store.Refresh(ctx, work.Assignment)
			if err != nil {
				cancel(promptFailure("assignment_refresh", err))
				if errors.Is(err, repository.ErrStale) {
					stopped = true
				}
				continue
			}
			if promptStopped(latest, turn) {
				stopped = true
				cancel(nil)
			}
			if stopped || promptCtx.Err() != nil {
				continue
			}
			switch {
			case boundary.id != "":
				if time.Now().After(executionDeadline) {
					cancel(promptFailure("execution_deadline", context.DeadlineExceeded))
				}
			case len(pending) > 0:
				var retire bool
				retire, confirmErr = w.checkConfirmations(promptCtx, latest, turn, client, runner, pending)
				if confirmErr != nil {
					cancel(promptFailure("confirmation_poll", confirmErr))
					continue
				}
				executionDeadline = time.Now().Add(10 * time.Minute)
				if retire {
					// Clear explicitly unconsumed steers before closing the native queue.
					confirmErr = w.finishSteering(promptCtx, work, turn, client, steeringState, false)
					if confirmErr == nil {
						confirmErr = client.Close()
					}
					parked = confirmErr == nil
					cancel(promptFailure("confirmation_park", confirmErr))
				}
			default:
				if humanReplyEnabled && time.Now().After(executionDeadline) {
					cancel(promptFailure("execution_deadline", context.DeadlineExceeded))
					continue
				}
				boundary, confirmErr = w.fenceBoundary(promptCtx, latest, turn, client, steeringState, humanEnabled, workEnabled)
				if confirmErr != nil {
					cancel(promptFailure("boundary_poll", confirmErr))
					continue
				}
				if boundary.id == "" && !humanReplyEnabled {
					if steerErr := w.steerOrExpire(promptCtx, latest, turn, client, steeringState, executionDeadline); steerErr != nil {
						cancel(promptFailure("steering", steerErr))
					}
				}
			}
		case <-ctx.Done():
			cancel(nil)
		}
	}
}
func promptStopped(work repository.Work, turn repository.Turn) bool {
	return work.ParentLifecycle == "archived" || work.Lifecycle == "archived" ||
		(work.StopRevision > work.AppliedStopRevision && turn.Input.Seq <= work.StopSeq)
}

func (w *Worker) steerOrExpire(ctx context.Context, work repository.Work, turn repository.Turn, client *pi.Client, state *steering, deadline time.Time) error {
	if time.Now().After(deadline) {
		return context.DeadlineExceeded
	}
	if turn.Input.CorrectionOnly {
		return nil
	}
	steerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return w.steer(steerCtx, work, turn, client, state)
}

func (w *Worker) finishPrompt(ctx context.Context, writer *eventWriter, client *pi.Client, steeringState *steering, pending, parked, stopped bool, err, confirmErr error) (string, string, string) {
	if pending && !client.Alive() && !stopped && confirmErr == nil {
		// EOF alone is insufficient evidence that the old process is gone.
		confirmErr = client.Close()
		parked = confirmErr == nil
	}
	if parked && !stopped {
		writer.flush("interrupted")
		writer.finish()
		if writer.err != nil {
			return "failed", "event_persistence_failed", ""
		}
		return "interrupted", "awaiting_confirmation", writer.answer
	}
	err = errors.Join(err, confirmErr)
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	steerErr := w.finishSteering(finishCtx, writer.work, writer.turn, client, steeringState, stopped || err != nil)
	finishCancel()
	if steerErr != nil {
		if err == nil {
			w.logPromptFailure(ctx, writer.work, writer.turn, promptFailure("steering_cleanup", steerErr), nil)
		}
		err = errors.Join(err, steerErr)
	}
	status, code := "completed", ""
	var setupFailure promptSetupError
	if stopped {
		status = "canceled"
	} else if errors.As(err, &setupFailure) {
		status, code = "failed", string(setupFailure)
	} else if err != nil || writer.err != nil {
		status = "failed"
		code = "execution_failed"
	}
	messageStatus := "completed"
	if status != "completed" {
		messageStatus = "interrupted"
	}
	writer.flush(messageStatus)
	writer.finish()
	if writer.err != nil {
		status = "failed"
		code = "event_persistence_failed"
	}
	return status, code, writer.answer
}

type promptSetupError string

func (e promptSetupError) Error() string { return "[kiko_" + string(e) + "]" }

func promptSetupFailure(u pi.Update) error {
	if u["type"] != "extension_error" || u["event"] != "before_agent_start" {
		return nil
	}
	for _, err := range [...]promptSetupError{"work_mcp_unavailable", "assistant_mcp_unavailable", "human_mcp_unavailable", "task_space_history_mcp_unavailable"} {
		if u["error"] == err.Error() {
			return err
		}
	}
	return nil
}

func (w *Worker) readPrompt(ctx, promptCtx context.Context, cancel context.CancelCauseFunc, writer *eventWriter, client *pi.Client, promptText string, requests chan<- pi.Update, state *steering) error {
	var setupErr error
	recordRoot := func(status string) {
		if writer.turn.Input.NeedsContextReceipt {
			if e := w.Store.ContextReceipt(ctx, writer.work, writer.turn.Input.ID, status); e != nil {
				setupErr = e
				cancel(promptFailure("input_delivery", e))
			}
		}
	}
	_, err := client.Prompt(promptCtx, promptText, func() { recordRoot("committed") }, func(u pi.Update) {
		if e := promptSetupFailure(u); e != nil {
			setupErr = errors.Join(setupErr, e)
		}
		if u["type"] == "extension_ui_request" && u["method"] == "confirm" {
			select {
			case requests <- u:
			case <-promptCtx.Done():
			}
			return
		}
		if input := state.matchingUser(u); input != nil {
			if input.ID != writer.turn.Input.ID {
				if e := w.Store.ContextReceipt(ctx, writer.work, input.ID, "committed"); e != nil {
					setupErr = e
					cancel(promptFailure("input_delivery", e))
					return
				}
			}
			state.pending.CompareAndSwap(input, nil)
		}
		writer.update(u)
		if writer.err != nil {
			cancel(promptFailure("event_persistence", writer.err))
		}
	})
	if errors.Is(err, pi.ErrCommandRejected) {
		recordRoot("absent")
	}
	return errors.Join(err, setupErr)
}
