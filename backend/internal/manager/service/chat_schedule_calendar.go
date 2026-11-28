package service

import (
	"slices"
	"time"
)

// ChatScheduleRule keeps calendar choices separate from the delegated instruction.
type ChatScheduleRule struct {
	Frequency string     `json:"frequency"`
	Timezone  string     `json:"timezone,omitempty"`
	At        *time.Time `json:"at,omitempty"`
	Anchor    *time.Time `json:"anchor,omitempty"`
	Interval  int        `json:"interval,omitempty"`
	Minute    *int       `json:"minute,omitempty"`
	Times     []string   `json:"times,omitempty"`
	Weekdays  []int      `json:"weekdays,omitempty"`
	MonthDays []int      `json:"month_days,omitempty"`
}

func (r ChatScheduleRule) Normalize(now time.Time) (ChatScheduleRule, error) {
	bad := Fail("invalid_chat_schedule", 400, "定时规则无效，请检查频率、时间及重复日期")
	if r.Timezone == "" {
		r.Timezone = "Asia/Shanghai"
	}
	if _, err := ScheduleLocation(r.Timezone); err != nil {
		return r, err
	}
	if !r.validCommonChoices() {
		return r, bad
	}
	switch r.Frequency {
	case "once":
		if r.At == nil || r.At.Second() != 0 || r.At.Nanosecond() != 0 || !r.At.After(now) || r.Anchor != nil || r.Interval != 0 || r.Minute != nil || len(r.Times)+len(r.Weekdays)+len(r.MonthDays) != 0 {
			return r, bad
		}
		at := r.At.UTC()
		r.At = &at
	case "hourly":
		if r.At != nil || len(r.Times)+len(r.Weekdays)+len(r.MonthDays) != 0 || r.Anchor != nil || r.Interval > 1 && r.Minute != nil {
			return r, bad
		}
		if r.Interval > 1 {
			anchor := now.UTC().Truncate(time.Minute)
			r.Anchor = &anchor
		}
	case "daily", "weekly", "monthly":
		if r.At != nil || r.Anchor != nil || r.Interval != 0 || r.Minute != nil || len(r.Times) == 0 || (r.Frequency == "weekly") != (len(r.Weekdays) > 0) || (r.Frequency == "monthly") != (len(r.MonthDays) > 0) {
			return r, bad
		}
		if !r.validCalendarChoices() {
			return r, bad
		}
	default:
		return r, bad
	}
	r.Times = sortedScheduleValues(r.Times)
	r.Weekdays = sortedScheduleValues(r.Weekdays)
	r.MonthDays = sortedScheduleValues(r.MonthDays)
	return r, nil
}

func (r ChatScheduleRule) validCommonChoices() bool {
	return len(r.Times) <= 24 && len(r.Weekdays) <= 7 && len(r.MonthDays) <= 32 &&
		(r.Minute == nil || (*r.Minute >= 0 && *r.Minute <= 59)) && r.Interval >= 0 && r.Interval <= 168
}

func (r ChatScheduleRule) validCalendarChoices() bool {
	for _, v := range r.Times {
		t, err := time.Parse("15:04", v)
		if err != nil || len(v) != 5 || t.Format("15:04") != v {
			return false
		}
	}
	for _, d := range r.Weekdays {
		if d < 1 || d > 7 {
			return false
		}
	}
	for _, d := range r.MonthDays {
		if d != -1 && (d < 1 || d > 31) {
			return false
		}
	}
	return true
}

func sortedScheduleValues[T ~int | ~string](v []T) []T {
	out := slices.Clone(v)
	slices.Sort(out)
	return slices.Compact(out)
}

// Next returns zero after a one-time occurrence. Calendar times in a DST gap
// are skipped; a repeated wall time fires only once.
func (r ChatScheduleRule) Next(after time.Time) (time.Time, error) {
	loc, err := ScheduleLocation(r.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	if r.Frequency == "once" {
		if r.At != nil && r.At.After(after) {
			return *r.At, nil
		}
		return time.Time{}, nil
	}
	if r.Frequency == "hourly" && r.Anchor != nil && r.Interval > 1 {
		step := time.Duration(r.Interval) * time.Hour
		elapsed := max(after.Sub(*r.Anchor), 0)
		return r.Anchor.Add((elapsed/step + 1) * step), nil
	}
	day := after.In(loc)
	// Use an independent noon calendar cursor, so a DST gap at midnight cannot
	// change the day number. An impossible 31st is simply not visited.
	date := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)
	for range 400 {
		for _, wall := range r.wallTimes(date) {
			c := wallScheduleInstant(date, wall.Hour(), wall.Minute(), loc)
			if c.After(after) {
				return c.UTC(), nil
			}
		}
		date = date.AddDate(0, 0, 1)
	}
	return time.Time{}, Fail("invalid_chat_schedule", 400, "无法计算下一次执行时间")
}

func (r ChatScheduleRule) wallTimes(date time.Time) []time.Time {
	switch r.Frequency {
	case "weekly":
		if !slices.Contains(r.Weekdays, (int(date.Weekday())+6)%7+1) {
			return nil
		}
	case "monthly":
		lastDay := slices.Contains(r.MonthDays, -1) && date.AddDate(0, 0, 1).Day() == 1
		if !slices.Contains(r.MonthDays, date.Day()) && !lastDay {
			return nil
		}
	case "hourly":
		minute := 0
		if r.Minute != nil {
			minute = *r.Minute
		}
		hours := make([]time.Time, 24)
		for h := range hours {
			hours[h] = time.Date(2000, 1, 1, h, minute, 0, 0, time.UTC)
		}
		return hours
	}
	times := make([]time.Time, 0, len(r.Times))
	for _, v := range r.Times {
		t, err := time.Parse("15:04", v)
		if err == nil {
			times = append(times, t)
		}
	}
	return times
}

func wallScheduleInstant(date time.Time, hour, minute int, loc *time.Location) time.Time {
	wall := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, time.UTC)
	local := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
	var best time.Time
	for _, probe := range []time.Time{local.Add(-24 * time.Hour), local, local.Add(24 * time.Hour)} {
		_, offset := probe.Zone()
		v := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		if v.Year() == date.Year() && v.Month() == date.Month() && v.Day() == date.Day() && v.Hour() == hour && v.Minute() == minute && (best.IsZero() || v.Before(best)) {
			best = v
		}
	}
	return best
}
