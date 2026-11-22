package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Trigger struct {
	InitialCredential   *TriggerCredential `json:"-"` // Included only in the successful creation response.
	Endpoint            string             `json:"endpoint,omitempty"`
	LastVerifiedAt      *time.Time         `json:"last_verified_at,omitempty"`
	Name                string             `json:"name"`
	NotificationTargets json.RawMessage    `json:"notification_targets"`
	ID                  string             `json:"id"`
	WorkspaceID         string             `json:"workspace_id"`
	AgentID             string             `json:"agent_id"`
	Kind                string             `json:"kind"`
	Enabled             bool               `json:"enabled"`
	Version             int64              `json:"version,string"`
	Config              json.RawMessage    `json:"config"`
	AllowedActions      []string           `json:"allowed_actions"`
	CreatedBy           string             `json:"created_by"`
	CreatedAt           time.Time          `json:"created_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
	NextDue             *time.Time         `json:"next_due"`
}
type TriggerWrite struct {
	SigningToken        *string         `json:"signing_token,omitempty"`
	Name                string          `json:"name"`
	NotificationTargets json.RawMessage `json:"notification_targets"`
	Version             int64           `json:"version,string"`
	Kind                string          `json:"kind"`
	Enabled             bool            `json:"enabled"`
	Config              json.RawMessage `json:"config"`
	AllowedActions      []string        `json:"allowed_actions"`
}
type TriggerCommand struct {
	Version     int     `json:"version"`
	EventID     string  `json:"eventId"`
	Action      string  `json:"action"`
	Description string  `json:"description,omitempty"`
	BusinessKey *string `json:"businessKey,omitempty"`
	TaskID      string  `json:"taskId,omitempty"`
	EntityID    string  `json:"entityId,omitempty"`
}
type TriggerAcceptance struct {
	Native         *EventPayload
	TriggerID      string
	BindingVersion int64
	EventID        string
	BodyHash       string
	Command        TriggerCommand
	OccurredAt     time.Time
}
type TriggerReceipt struct {
	EventID     string `json:"event_id"`
	Status      string `json:"status"`
	TaskID      string `json:"task_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	TaskSpaceID string `json:"task_space_id,omitempty"`
	InputID     string `json:"input_id,omitempty"`
	Duplicate   bool   `json:"duplicate"`
}
type TriggerCredential struct {
	KeyID     string    `json:"key_id"`
	Status    string    `json:"status"`
	Secret    string    `json:"secret,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
type TriggerEvent struct {
	EventName       string          `json:"event_name,omitempty"`
	ExecutionStatus string          `json:"execution_status,omitempty"`
	ID              string          `json:"id"`
	EventID         string          `json:"event_id"`
	Action          string          `json:"action"`
	BindingVersion  int64           `json:"binding_version,string"`
	Status          string          `json:"status"`
	ReceivedAt      time.Time       `json:"received_at"`
	Receipt         *TriggerReceipt `json:"receipt"`
}
type TriggerStore interface {
	ReceiveNativeEvent(context.Context, string, EventRequest, time.Time) (TriggerReceipt, bool, error)
	ImportTriggerCredential(context.Context, string, string, string, string, string) (TriggerCredential, error)
	SaveTrigger(context.Context, string, string, string, string, TriggerWrite) (Trigger, error)
	GetTrigger(context.Context, string, string, string, string) (Trigger, error)
	ListTriggers(context.Context, string, string, string) ([]Trigger, error)
	TriggerCredentials(context.Context, string, string, string, string) ([]TriggerCredential, error)
	CreateTriggerCredential(context.Context, string, string, string, string) (TriggerCredential, error)
	RevokeTriggerCredential(context.Context, string, string, string, string, string) error
	TriggerEvents(context.Context, string, string, string, string, time.Time, string, int) ([]TriggerEvent, error)
	ReceiveTriggerWebhook(context.Context, string, string, string, string, []byte, time.Time) (TriggerReceipt, error)
	ReceiveTriggerGithub(context.Context, string, string, string, string, []byte, time.Time) (TriggerReceipt, bool, error)
}

func (s *Service) Triggers() (TriggerStore, error) {
	st, ok := s.Store.(TriggerStore)
	if !ok {
		return nil, Fail("dependency_unavailable", 503, "触发器服务不可用")
	}
	return st, nil
}
func ValidateTriggerCommand(q TriggerCommand) error {
	if q.Version != 1 || strings.TrimSpace(q.EventID) == "" || len(q.EventID) > 160 || strings.ContainsAny(q.EventID, "\x00\r\n") {
		return Fail("invalid_trigger_command", 400, "事件参数无效")
	}
	for _, r := range q.EventID {
		if r < 32 || r == 127 {
			return Fail("invalid_trigger_command", 400, "事件编号无效")
		}
	}
	if q.EntityID != "" && !ValidID(q.EntityID) || q.TaskID != "" && !ValidID(q.TaskID) {
		return Fail("invalid_trigger_command", 400, "目标无效")
	}
	if q.BusinessKey != nil && (len(*q.BusinessKey) > 200 || strings.TrimSpace(*q.BusinessKey) == "" || strings.ContainsRune(*q.BusinessKey, 0)) {
		return Fail("invalid_trigger_command", 400, "业务键无效")
	}
	switch q.Action {
	case "create_task":
		if q.TaskID != "" {
			return Fail("invalid_trigger_command", 400, "创建不能指定任务")
		}
	case "supplement_task", "cancel_task":
		if q.TaskID == "" {
			return Fail("invalid_trigger_command", 400, "缺少任务")
		}
	default:
		return Fail("invalid_trigger_command", 400, "动作无效")
	}
	if q.Action != "cancel_task" && (!validText(q.Description) || utf8.RuneCountInString(q.Description) > 8000) {
		return Fail("invalid_trigger_command", 400, "任务描述无效")
	}
	return nil
}

// Fixed protocol signs raw bytes, including path and millisecond timestamp.
func VerifyTriggerSignature(id, key, timestamp, signature string, body []byte, now time.Time) (string, error) {
	bad := func() (string, error) { return "", Fail("trigger_authentication_failed", 401, "触发器认证失败") }
	ts, e := strconv.ParseInt(timestamp, 10, 64)
	if e != nil || strconv.FormatInt(ts, 10) != timestamp || ts < 0 || len(body) == 0 || len(body) > 256*1024 {
		return bad()
	}
	delta := now.UnixMilli() - ts
	if delta > 300000 || delta < -300000 {
		return bad()
	}
	raw, e := hex.DecodeString(signature)
	if e != nil || len(raw) != 32 || strings.ToLower(signature) != signature {
		return bad()
	}
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	canonical := strings.Join([]string{"v1", "POST", "/api/triggers/webhook/" + id, timestamp, digest}, "\n")
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(raw, mac.Sum(nil)) {
		return bad()
	}
	return digest, nil
}

func (s *Service) ListTriggerEvents(ctx context.Context, w, actor, agent, id, raw string, n int) (Page[TriggerEvent], error) {
	out := Page[TriggerEvent]{Items: []TriggerEvent{}}
	n, e := pageLimit(n)
	if e != nil {
		return out, e
	}
	scope := "trigger-events:" + w + ":" + actor + ":" + agent + ":" + id
	var before time.Time
	var beforeID string
	if raw != "" {
		c, err := s.decodeCursor(raw, scope)
		if err != nil {
			return out, err
		}
		before = c.Time
		beforeID = c.ID
		if !ValidID(beforeID) || before.IsZero() {
			return out, Fail("invalid_cursor", 400, "游标无效")
		}
	}
	st, e := s.Triggers()
	if e != nil {
		return out, e
	}
	items, e := st.TriggerEvents(ctx, w, actor, agent, id, before, beforeID, n+1)
	if e != nil {
		return out, e
	}
	if len(items) > n {
		items = items[:n]
		last := items[n-1]
		next, err := s.encodeCursor(cursor{Scope: scope, Time: last.ReceivedAt, ID: last.ID})
		if err != nil {
			return out, err
		}
		out.NextCursor = &next
	}
	out.Items = items
	return out, nil
}
