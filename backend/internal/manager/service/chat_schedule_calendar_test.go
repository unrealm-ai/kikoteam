package service

import (
	"testing"
	"time"
)

func scheduleInstant(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestChatScheduleCalendar(t *testing.T) {
	for _, tt := range []struct {
		name, after, want string
		rule              ChatScheduleRule
	}{
		{"daily multi-time", "2026-11-28T09:00:00+08:00", "2026-11-28T18:00:00+08:00", ChatScheduleRule{Frequency: "daily", Times: []string{"18:00", "09:00"}}},
		{"weekly chosen days", "2026-11-30T09:00:00+08:00", "2026-12-02T09:00:00+08:00", ChatScheduleRule{Frequency: "weekly", Times: []string{"09:00"}, Weekdays: []int{1, 3, 5}}},
		{"monthly 31 skips", "2026-01-31T09:00:00+08:00", "2026-03-31T09:00:00+08:00", ChatScheduleRule{Frequency: "monthly", Times: []string{"09:00"}, MonthDays: []int{31}}},
		{"last day", "2028-02-01T09:00:00+08:00", "2028-02-29T09:00:00+08:00", ChatScheduleRule{Frequency: "monthly", Times: []string{"09:00"}, MonthDays: []int{-1}}},
		{"interval", "2026-11-28T09:17:20+08:00", "2026-11-28T11:17:00+08:00", ChatScheduleRule{Frequency: "hourly", Interval: 2}},
		{"hourly", "2026-11-28T09:17:20+08:00", "2026-11-28T10:00:00+08:00", ChatScheduleRule{Frequency: "hourly"}},
		{"DST missing", "2026-03-08T00:00:00-05:00", "2026-03-09T02:30:00-04:00", ChatScheduleRule{Frequency: "daily", Timezone: "America/New_York", Times: []string{"02:30"}}},
		{"DST repeated once", "2026-11-01T01:30:00-04:00", "2026-11-02T01:30:00-05:00", ChatScheduleRule{Frequency: "daily", Timezone: "America/New_York", Times: []string{"01:30"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			after := scheduleInstant(t, tt.after)
			r, err := tt.rule.Normalize(after)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.Next(after)
			if err != nil || !got.Equal(scheduleInstant(t, tt.want)) {
				t.Fatalf("next: %s %v; want %s", got, err, tt.want)
			}
		})
	}
	at := scheduleInstant(t, "2026-11-28T14:30:00+08:00")
	once, err := (ChatScheduleRule{Frequency: "once", At: &at}).Normalize(at.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	next, err := once.Next(at)
	if err != nil || !next.IsZero() {
		t.Fatal(next, err)
	}
	if _, err = (ChatScheduleRule{Frequency: "weekly", Times: []string{"09:00"}}).Normalize(at); err == nil {
		t.Fatal("weekly without weekdays accepted")
	}
}
