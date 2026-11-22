package integration

import (
	"context"
	"time"
)

// ReportTask notifies only after execution facts commit. Hub reconciliation
// supplies durability when this bounded best-effort call fails.
func (b *Backend) ReportTask(ctx context.Context, turn string) error {
	var err error
	for range 2 {
		call, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = b.Call(call, "POST", "/task-results/callback", map[string]string{"turn_id": turn}, nil)
		cancel()
		if err == nil || ctx.Err() != nil {
			return err
		}
	}
	return err
}
