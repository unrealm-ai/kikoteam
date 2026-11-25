package service

import "testing"

func TestTaskProgressPhase(t *testing.T) {
	for _, c := range []struct {
		name, planning, lifecycle, execution, last, want string
		preparing, stopped                               bool
	}{
		{"queued after previous finish", "started", "active", "queued", "completed", "queued", false, false},
		{"environment assigned but not ready", "started", "active", "queued", "", "preparing_environment", true, false},
		{"active turn overrides environment", "started", "active", "running", "", "running", true, false},
		{"recovery is not execution", "started", "active", "blocked", "completed", "blocked", false, false},
		{"result succeeded", "started", "active", "idle", "completed", "completed", false, false},
		{"result failed", "started", "active", "idle", "failed", "failed", false, false},
		{"explicit stop", "started", "active", "idle", "canceled", "stopped", false, true},
		{"todo", "planned", "active", "idle", "", "planned", false, false},
		{"archived", "started", "archived", "idle", "completed", "archived", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := TaskProgressPhase(c.planning, c.lifecycle, c.execution, c.last, c.preparing, c.stopped)
			if got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}
