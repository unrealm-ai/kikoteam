// Package channelhistory prepares bounded historical context only for jobs
// already admitted by Manager. It does not listen, create inputs, or advance
// conversation watermarks. Callers must durably freeze a successful snapshot
// before submitting the original input; retries reuse that frozen snapshot.
package channelhistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"
	"net/url"
	"strconv"
)

// Job is a Manager-issued authorization scope, never a browser/model request.
// Since-last-trigger selects strictly after the previous effective trigger and
// before this trigger, capped to the latest Limit messages. Only a first trigger
// (no previous anchor) falls back to recent messages.
type Job struct {
	TriggerFormat         string `json:"trigger_format,omitempty"`
	TriggerText           string `json:"trigger_text,omitempty"`
	ID                    string `json:"id"`
	LeaseToken            string `json:"lease_token"`
	BindingID             string `json:"binding_id"`
	Generation            int64  `json:"connection_generation"`
	Revision              int64  `json:"config_revision"`
	RulesVersion          int64  `json:"rules_version"`
	TenantID              string `json:"external_tenant_id"`
	ChatID                string `json:"chat_id"`
	ThreadID              string `json:"thread_id,omitempty"`
	TriggerID             string `json:"trigger_message_id"`
	TriggerTimeMS         int64  `json:"trigger_created_at_ms"`
	Limit                 int    `json:"limit"`
	Mode                  string `json:"strategy"`
	PreviousTriggerID     string `json:"previous_trigger_message_id,omitempty"`
	PreviousTriggerTimeMS int64  `json:"previous_trigger_created_at_ms,omitempty"`
}

// Gate must call Manager authorization for the exact job/lease and binding.
// An absent or failing Gate prohibits all provider requests.
type Gate func(context.Context, Job) error

// Get uses the SDK tenant token for this exact employee binding. No external
// destination is accepted: Reader always requests the fixed Feishu API path.
type Get func(context.Context, string) ([]byte, error)
type Mention struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	IDType    string `json:"id_type,omitempty"`
	TenantKey string `json:"tenant_key,omitempty"`
}
type Message struct {
	RecordKey    string    `json:"record_key,omitempty"`
	SenderName   string    `json:"sender_name,omitempty"`
	SenderTenant string    `json:"sender_tenant,omitempty"`
	SenderIDType string    `json:"sender_id_type,omitempty"`
	Mentions     []Mention `json:"mentions,omitempty"`
	ID           string    `json:"message_id"`
	SenderID     string    `json:"sender_id"`
	SenderType   string    `json:"sender_type"`
	TimeMS       int64     `json:"created_at_ms"`
	Type         string    `json:"message_type"`
	Text         string    `json:"text"`
}
type Snapshot struct {
	JobID     string    `json:"job_id"`
	TriggerID string    `json:"trigger_message_id"`
	Messages  []Message `json:"items"`
	Truncated bool      `json:"truncated"`
	Digest    string    `json:"digest"`
}
type Reader struct {
	Authorize Gate
	Get       Get
}
type wireMessage struct {
	Mentions []Mention `json:"mentions"`
	ID       string    `json:"message_id"`
	ChatID   string    `json:"chat_id"`
	ThreadID string    `json:"thread_id"`
	Created  string    `json:"create_time"`
	Type     string    `json:"msg_type"`
	Deleted  bool      `json:"deleted"`
	Sender   struct {
		ID        string `json:"id"`
		Type      string `json:"sender_type"`
		Name      string `json:"sender_name"`
		TenantKey string `json:"tenant_key"`
		IDType    string `json:"id_type"`
	} `json:"sender"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
}
type wirePage struct {
	Code int `json:"code"`
	Data struct {
		Items   []wireMessage `json:"items"`
		HasMore *bool         `json:"has_more"`
		Token   string        `json:"page_token"`
	} `json:"data"`
}

func valid(j Job) bool {
	return j.ID != "" && j.LeaseToken != "" && j.BindingID != "" && j.Generation > 0 && j.Revision > 0 && j.RulesVersion > 0 && j.TenantID != "" && j.ChatID != "" && j.TriggerID != "" && j.TriggerTimeMS > 0 && j.Limit >= 1 && j.Limit <= 100 && j.Mode == "since_last_trigger" && validPrevious(j)
}
func validPrevious(j Job) bool {
	if j.PreviousTriggerID == "" {
		return j.PreviousTriggerTimeMS == 0
	}
	return j.PreviousTriggerID != j.TriggerID && j.PreviousTriggerTimeMS > 0 && j.PreviousTriggerTimeMS <= j.TriggerTimeMS
}

type selection struct {
	out          Snapshot
	seen         map[string]bool
	found        bool
	size         int
	previousTime int64
}

func (s *selection) consume(j Job, m wireMessage) (bool, error) {
	if m.ID == "" || m.ChatID != j.ChatID {
		return false, errors.New("history_scope_mismatch")
	}
	if j.ThreadID != "" && m.ThreadID != j.ThreadID {
		return false, errors.New("history_thread_scope_mismatch")
	}
	if j.ThreadID == "" && m.ThreadID != "" && (m.ID == j.TriggerID || m.ID == j.PreviousTriggerID) {
		return false, errors.New("history_thread_scope_mismatch")
	}
	if s.seen[m.ID] {
		return false, nil
	}
	s.seen[m.ID] = true
	at, err := strconv.ParseInt(m.Created, 10, 64)
	if err != nil || at <= 0 {
		return false, errors.New("history_timestamp_invalid")
	}
	if s.previousTime > 0 && at > s.previousTime {
		return false, errors.New("history_order_invalid")
	}
	s.previousTime = at
	if m.ID == j.TriggerID {
		if at != j.TriggerTimeMS {
			return false, errors.New("history_trigger_timestamp_mismatch")
		}
		s.found = true
		return false, nil
	}
	if m.ID == j.PreviousTriggerID && j.PreviousTriggerID != "" {
		if !s.found || at != j.PreviousTriggerTimeMS {
			return false, errors.New("history_lower_boundary_invalid")
		}
		return true, nil
	}
	if !s.found {
		return false, nil
	}
	if j.PreviousTriggerID != "" && at < j.PreviousTriggerTimeMS {
		return false, errors.New("history_lower_anchor_not_found")
	}
	if at > j.TriggerTimeMS {
		return false, errors.New("history_order_invalid")
	}
	if j.ThreadID == "" && m.ThreadID != "" {
		return false, nil
	}
	if len(s.out.Messages) == j.Limit {
		s.out.Truncated = true
		// A full bounded result strictly newer than the known prior timestamp is
		// proven inside the interval even if the prior ID is outside the page budget.
		return j.PreviousTriggerID == "" || at > j.PreviousTriggerTimeMS, nil
	}
	text, err := messageText(m)
	if err != nil {
		return false, err
	}
	s.size += len(text)
	if s.size > 32768 {
		return false, errors.New("history_context_too_large")
	}
	s.out.Messages = append(s.out.Messages, Message{ID: m.ID, SenderID: m.Sender.ID, SenderType: m.Sender.Type, SenderName: m.Sender.Name, SenderTenant: m.Sender.TenantKey, SenderIDType: m.Sender.IDType, Mentions: m.Mentions, TimeMS: at, Type: m.Type, Text: text})
	return false, nil
}
func messageText(m wireMessage) (string, error) {
	if m.Deleted {
		return "[消息已撤回]", nil
	}
	if m.Type != "text" && m.Type != "post" {
		return "[不支持的消息类型]", nil
	}
	mentions := map[string]string{}
	conflicts := map[string]bool{}
	for _, v := range m.Mentions {
		if v.ID == "" {
			continue
		}
		if old, ok := mentions[v.ID]; ok && old != v.Key {
			conflicts[v.ID] = true
		}
		mentions[v.ID] = v.Key
	}
	for id := range conflicts {
		delete(mentions, id)
	}
	return feishucontent.RenderWithMentions(m.Type, m.Body.Content, mentions)
}

// Thread containers do not support time filters. Scan backwards to the exact
// trigger ID before selecting context; messages posted later are never included.
func historyQuery(j Job) url.Values {
	q := url.Values{"container_id_type": {"chat"}, "container_id": {j.ChatID}, "sort_type": {"ByCreateTimeDesc"}, "page_size": {"50"}}
	if j.ThreadID != "" {
		q.Set("container_id_type", "thread")
		q.Set("container_id", j.ThreadID)
	} else {
		q.Set("end_time", strconv.FormatInt(j.TriggerTimeMS/1000+1, 10))
	}
	return q
}

func (r Reader) Fetch(ctx context.Context, j Job) (Snapshot, error) {
	if !valid(j) || r.Authorize == nil || r.Get == nil {
		return Snapshot{}, errors.New("history_job_invalid")
	}
	q := historyQuery(j)
	st := selection{out: Snapshot{JobID: j.ID, TriggerID: j.TriggerID, Messages: []Message{}}, seen: map[string]bool{}}
	tokens := map[string]bool{}
	for page := 0; page < 10; page++ {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := r.Authorize(ctx, j); err != nil {
			return Snapshot{}, errors.New("history_authorization_revoked")
		}
		raw, err := r.Get(ctx, "/open-apis/im/v1/messages?"+q.Encode())
		if err != nil {
			return Snapshot{}, errors.New("history_provider_unavailable")
		}
		if len(raw) > 1<<20 {
			return Snapshot{}, errors.New("history_page_too_large")
		}
		var p wirePage
		if json.Unmarshal(raw, &p) != nil || p.Code != 0 || p.Data.HasMore == nil {
			return Snapshot{}, errors.New("history_provider_rejected")
		}
		for _, m := range p.Data.Items {
			done, err := st.consume(j, m)
			if err != nil {
				return Snapshot{}, err
			}
			if done {
				return r.finish(ctx, j, st.out)
			}
		}
		if !*p.Data.HasMore {
			if !st.found {
				return Snapshot{}, errors.New("history_trigger_not_found")
			}
			if j.PreviousTriggerID != "" {
				return Snapshot{}, errors.New("history_lower_anchor_not_found")
			}
			return r.finish(ctx, j, st.out)
		}
		if p.Data.Token == "" || tokens[p.Data.Token] {
			return Snapshot{}, errors.New("history_cursor_invalid")
		}
		tokens[p.Data.Token] = true
		q.Set("page_token", p.Data.Token)
	}
	return Snapshot{}, errors.New("history_page_budget_exceeded")
}
func (r Reader) finish(ctx context.Context, j Job, out Snapshot) (Snapshot, error) {
	if err := r.Authorize(ctx, j); err != nil {
		return Snapshot{}, errors.New("history_authorization_revoked")
	}
	for a, b := 0, len(out.Messages)-1; a < b; a, b = a+1, b-1 {
		out.Messages[a], out.Messages[b] = out.Messages[b], out.Messages[a]
	}
	j.LeaseToken = "" // A lease is not part of the immutable snapshot content.
	body, err := json.Marshal(struct {
		Job      Job      `json:"job"`
		Snapshot Snapshot `json:"snapshot"`
	}{j, out})
	if err != nil {
		return Snapshot{}, fmt.Errorf("history_snapshot_encoding: %w", err)
	}
	sum := sha256.Sum256(body)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}
