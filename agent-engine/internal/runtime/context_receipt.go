package runtime

import (
	"context"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/pi"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

// Recovery only promotes durable native evidence. It never redispatches an
// uncertain input, infers absence from a missing file, or runs a model to check.
func (w *Worker) reconcileContextReceipts(ctx context.Context, work repository.Work, after int64) (int64, error) {
	if work.NativeSessionID == nil {
		return after, nil
	}
	inputs, err := w.Store.UnresolvedContextInputs(ctx, work, after)
	if err != nil {
		return after, err
	}
	if len(inputs) == 0 {
		return 0, nil
	}
	for _, candidate := range inputs {
		id := candidate.ID
		after = candidate.Seq
		state := pi.NativeInputState(w.nativeDirectory(work), *work.NativeSessionID, id)
		if err = w.Store.ContextReceipt(ctx, work, id, state); err != nil {
			return after, err
		}
	}
	return after, nil
}
