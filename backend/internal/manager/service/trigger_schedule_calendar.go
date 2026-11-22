package service

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type TriggerBusinessKeyRule struct {
	Mode   string `json:"mode"`
	Value  string `json:"value,omitempty"`
	Period string `json:"period,omitempty"`
}
type TriggerScheduleConfig struct {
	Action      string                 `json:"action,omitempty"`
	Cron        string                 `json:"cron"`
	Timezone    string                 `json:"timezone"`
	Description string                 `json:"description"`
	BusinessKey TriggerBusinessKeyRule `json:"businessKey"`
}

// ScheduleLocation accepts stable IANA names, never the host-dependent Local zone.
func ScheduleLocation(zone string) (*time.Location, error) {
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	if zone == "Local" {
		return nil, Fail("invalid_trigger_timezone", 400, "请使用明确IANA时区")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, Fail("invalid_trigger_timezone", 400, "时区无效")
	}
	return loc, nil
}
func NextTriggerSchedule(expression, zone string, after time.Time) (time.Time, error) {
	loc, err := ScheduleLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(expression)
	if len(expression) > 200 || len(fields) != 5 || strings.ContainsAny(strings.ToUpper(expression), "@#H") || len(fields) == 5 && (strings.Contains(strings.ToUpper(fields[2]), "L") || strings.Contains(strings.ToUpper(fields[4]), "L")) {
		return time.Time{}, Fail("unsupported_trigger_schedule", 400, "仅支持标准五字段cron，不支持L、#、H或宏")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse("CRON_TZ=" + loc.String() + " " + expression)
	if err != nil {
		return time.Time{}, Fail("invalid_trigger_schedule", 400, "cron表达式无效")
	}
	next := schedule.Next(after.In(loc))
	if next.IsZero() {
		return time.Time{}, Fail("invalid_trigger_schedule", 400, "cron没有可计算的下一执行时间")
	}
	return next.UTC(), nil
}
func TriggerScheduleKey(rule TriggerBusinessKeyRule, at time.Time, zone string) (*string, error) {
	switch rule.Mode {
	case "", "empty":
		if rule.Value != "" || rule.Period != "" {
			return nil, Fail("invalid_trigger_key_rule", 400, "空业务键不接受其他参数")
		}
		return nil, nil
	case "fixed":
		if rule.Period != "" || strings.TrimSpace(rule.Value) == "" || len(rule.Value) > 200 || strings.ContainsRune(rule.Value, 0) {
			return nil, Fail("invalid_trigger_key_rule", 400, "固定业务键无效")
		}
		return &rule.Value, nil
	case "period":
		if rule.Value != "" {
			return nil, Fail("invalid_trigger_key_rule", 400, "周期业务键不接受固定值")
		}
	default:
		return nil, Fail("invalid_trigger_key_rule", 400, "业务键模式无效")
	}
	loc, err := ScheduleLocation(zone)
	if err != nil {
		return nil, err
	}
	t := at.In(loc)
	var start, end time.Time
	switch rule.Period {
	case "minute":
		start = t.Add(-time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
		end = start.Add(time.Minute)
	case "hour":
		start = t.Add(-time.Duration(t.Minute())*time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
		end = start.Add(time.Hour)
	case "day", "week":
		start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		if rule.Period == "week" {
			start = start.AddDate(0, 0, -(int(t.Weekday())+6)%7)
			end = start.AddDate(0, 0, 7)
		} else {
			end = start.AddDate(0, 0, 1)
		}
	case "month":
		start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		end = start.AddDate(0, 1, 0)
	case "year":
		start = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc)
		end = start.AddDate(1, 0, 0)
	default:
		return nil, Fail("invalid_trigger_key_rule", 400, "业务键周期无效")
	}
	raw, err := json.Marshal([]string{rule.Period, start.UTC().Format("2006-01-02T15:04:05.000Z"), end.UTC().Format("2006-01-02T15:04:05.000Z")})
	if err != nil {
		return nil, err
	}
	key := string(raw)
	return &key, nil
}
func ParseTriggerSchedule(raw json.RawMessage) (TriggerScheduleConfig, error) {
	var v TriggerScheduleConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return v, Fail("invalid_trigger_configuration", 400, "定时配置无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return v, Fail("invalid_trigger_configuration", 400, "定时配置必须为单个JSON对象")
	}
	if v.Action == "" {
		v.Action = "create_task"
	}
	if v.Action != "create_task" && v.Action != "send_message" {
		return v, Fail("invalid_trigger_configuration", 400, "定时动作无效")
	}
	if v.Action == "send_message" && (v.BusinessKey.Mode != "" && v.BusinessKey.Mode != "empty" || v.BusinessKey.Value != "" || v.BusinessKey.Period != "") {
		return v, Fail("invalid_trigger_configuration", 400, "固定提醒不创建任务或业务空间")
	}
	if v.Timezone == "" {
		v.Timezone = "Asia/Shanghai"
	}
	if !validText(v.Description) || len(v.Description) > 32000 {
		return v, Fail("invalid_trigger_configuration", 400, "任务描述无效")
	}
	if _, err := NextTriggerSchedule(v.Cron, v.Timezone, time.Now()); err != nil {
		return v, err
	}
	_, err := TriggerScheduleKey(v.BusinessKey, time.Now(), v.Timezone)
	return v, err
}
