package service

import (
	"strings"
	"testing"
	"time"
)

func TestScheduleCalendarAndKeys(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := NextTriggerSchedule("0 9 * * *", "Asia/Shanghai", after)
	if err != nil || next.Format(time.RFC3339) != "2026-01-01T01:00:00Z" {
		t.Fatal(next, err)
	}
	for _, expr := range []string{"@daily", "0 0 9 * * *", "0 9 L * *", "0 9 * * 1#2", "0 9 * * 5L", "H * * * *"} {
		if _, err = NextTriggerSchedule(expr, "UTC", after); err == nil {
			t.Fatal("unsupported schedule accepted", expr)
		}
	}
	if _, err = NextTriggerSchedule("* * * * *", "Local", after); err == nil {
		t.Fatal("host timezone accepted")
	}
	at := time.Date(2026, 1, 4, 20, 0, 0, 0, time.UTC)
	key, err := TriggerScheduleKey(TriggerBusinessKeyRule{Mode: "period", Period: "week"}, at, "Asia/Shanghai")
	if err != nil || key == nil || !strings.Contains(*key, "2026-01-04T16:00:00.000Z") {
		t.Fatal(key, err)
	}
	empty, err := TriggerScheduleKey(TriggerBusinessKeyRule{Mode: "empty"}, at, "UTC")
	if err != nil || empty != nil {
		t.Fatal(empty, err)
	}
	fixed, err := TriggerScheduleKey(TriggerBusinessKeyRule{Mode: "fixed", Value: "project"}, at, "UTC")
	if err != nil || fixed == nil || *fixed != "project" {
		t.Fatal(fixed, err)
	}
}
func TestSchedulePeriodUsesCalendarDSTDays(t *testing.T) {
	at := time.Date(2026, 3, 8, 16, 0, 0, 0, time.UTC)
	key, err := TriggerScheduleKey(TriggerBusinessKeyRule{Mode: "period", Period: "day"}, at, "America/New_York")
	if err != nil || key == nil || *key != `["day","2026-03-08T05:00:00.000Z","2026-03-09T04:00:00.000Z"]` {
		t.Fatal(key, err)
	}
}
