package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type commandResult struct {
	data json.RawMessage
	err  error
}

type State struct {
	SessionID           string `json:"sessionId"`
	SessionFile         string `json:"sessionFile"`
	IsStreaming         bool   `json:"isStreaming"`
	PendingMessageCount int    `json:"pendingMessageCount"`
}

func (c *Client) GetState(ctx context.Context) (State, error) {
	data, err := c.call(ctx, "get_state", nil)
	var state State
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	return state, err
}

// Abort acknowledges the native command; the Prompt owner still waits for
// agent_settled. Clearing queues prevents accepted follow-ups from running next.
func (c *Client) Abort(ctx context.Context) error {
	if err := c.ClearQueue(ctx); err != nil {
		return err
	}
	_, err := c.call(ctx, "abort", nil)
	return err
}

// Prompt owns the event stream until Pi has settled, not merely acknowledged input.
// Other callers may submit steering/follow-up commands while this owner drains events.
func (c *Client) Prompt(ctx context.Context, text string, onUpdate func(Update)) (string, error) {
	if !c.promptMu.TryLock() {
		return "", errors.New("pi prompt already active")
	}
	defer c.promptMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.active = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active = false; c.mu.Unlock() }()
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan commandResult, 1)
	go func() {
		data, err := c.call(requestCtx, "prompt", map[string]any{"message": text})
		result <- commandResult{data, err}
	}()
	return c.waitPrompt(ctx, result, onUpdate)
}

func (c *Client) waitPrompt(ctx context.Context, result <-chan commandResult, onUpdate func(Update)) (string, error) {
	accepted, settled, aborted := false, false, false
	lastStop := ""
	for {
		if accepted && settled {
			return promptOutcome(lastStop, aborted)
		}
		select {
		case r := <-result:
			if r.err != nil {
				if ctx.Err() != nil {
					return c.cancelPrompt(ctx, onUpdate)
				}
				return "", r.err
			}
			var ack struct {
				Disposition string `json:"disposition"`
			}
			if json.Unmarshal(r.data, &ack) != nil {
				return "", errors.Join(errors.New("invalid Pi prompt acknowledgement"), c.Close())
			}
			switch ack.Disposition {
			case "handled":
				return "handled", nil
			case "started":
				accepted = true
			case "queued":
				return "", errors.Join(errors.New("unexpected queued starting prompt"), c.Close())
			default:
				return "", errors.Join(errors.New("unknown Pi prompt disposition"), c.Close())
			}
		case u := <-c.updates:
			onUpdate(u)
			if u["type"] == "message_end" {
				if m, ok := u["message"].(map[string]any); ok && m["role"] == "assistant" {
					lastStop, _ = m["stopReason"].(string)
				}
			}
			if u["type"] == "agent_settled" {
				settled = true
				aborted, _ = u["aborted"].(bool)
			}
		case <-ctx.Done():
			return c.cancelPrompt(ctx, onUpdate)
		case <-c.readDone:
			for {
				select {
				case u := <-c.updates:
					onUpdate(u)
				default:
					return "", c.readErr
				}
			}
		case <-c.closing:
			return "", errors.New("pi closed during prompt")
		}
	}
}

// ErrToolBatchSettled distinguishes Pi ending after tool results from a
// completed assistant reply. A platform tool must prove its own boundary.
var ErrToolBatchSettled = errors.New("pi settled after a terminal tool batch")

var (
	ErrPromptAborted   = errors.New("pi execution aborted")
	ErrPromptTruncated = errors.New("pi model output truncated")
	ErrPromptModel     = errors.New("pi model execution failed")
)

func promptOutcome(stop string, aborted bool) (string, error) {
	if aborted || stop == "aborted" {
		return "cancelled", ErrPromptAborted
	}
	switch stop {
	case "stop":
		return "end_turn", nil
	case "toolUse":
		return "tool_batch_end", ErrToolBatchSettled
	case "length":
		return "max_tokens", ErrPromptTruncated
	case "error":
		return "", ErrPromptModel
	default:
		return "", fmt.Errorf("pi settled without a final assistant result (%s)", stop)
	}
}
func (c *Client) cancelPrompt(ctx context.Context, onUpdate func(Update)) (string, error) {
	// Retire this process even after abort settles: cancellation can race with
	// prompt preflight, whose acceptance may not yet have reached the caller.
	defer func() { _ = c.Close() }() //nolint:errcheck // Cancellation result already fails; retirement is mandatory.
	// A cancelled command wait is not evidence that execution stopped. Abort has
	// no request ID here: only agent_settled (or process death) releases the owner.
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := c.sendCommand(abortCtx, map[string]any{"type": "clear_queue"}); err != nil {
		return "cancelled", errors.Join(ctx.Err(), err)
	}
	if err := c.sendCommand(abortCtx, map[string]any{"type": "abort"}); err != nil {
		return "cancelled", errors.Join(ctx.Err(), err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case u := <-c.updates:
			onUpdate(u)
			if u["type"] == "agent_settled" {
				return "cancelled", ctx.Err()
			}
		case <-c.readDone:
			return "cancelled", errors.Join(ctx.Err(), c.readErr)
		case <-timer.C:
			return "cancelled", errors.Join(ctx.Err(), c.Close())
		case <-c.closing:
			return "cancelled", ctx.Err()
		}
	}
}

// Steer queues native user input; success is acknowledgement, not consumption.
func (c *Client) Steer(ctx context.Context, text string) error {
	_, err := c.call(ctx, "steer", map[string]any{"message": text})
	return err
}

// FollowUp uses Pi's in-memory queue, not the platform's durable Input queue.
func (c *Client) FollowUp(ctx context.Context, text string) error {
	_, err := c.call(ctx, "follow_up", map[string]any{"message": text})
	return err
}
func (c *Client) ClearQueue(ctx context.Context) error {
	_, err := c.TakeQueue(ctx)
	return err
}

// TakeQueue returns positive evidence of messages removed before consumption.
func (c *Client) TakeQueue(ctx context.Context) ([]string, error) {
	data, err := c.call(ctx, "clear_queue", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Steering []string `json:"steering"`
		FollowUp []string `json:"followUp"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return append(result.Steering, result.FollowUp...), nil
}
func (c *Client) SetQueueMode(ctx context.Context, queue, mode string) error {
	if queue != "steering" && queue != "follow_up" {
		return errors.New("invalid Pi queue")
	}
	if mode != "all" && mode != "one-at-a-time" {
		return errors.New("invalid Pi queue mode")
	}
	_, err := c.call(ctx, "set_"+queue+"_mode", map[string]any{"mode": mode})
	return err
}
