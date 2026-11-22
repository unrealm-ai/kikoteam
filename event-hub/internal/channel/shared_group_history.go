package channel

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"log/slog"
	"net/url"
	"reflect"
	"time"
)

type sharedHistorySource struct {
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	BindingID  string `json:"binding_id,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`
	Generation int64  `json:"connection_generation,omitempty"`
	Revision   int64  `json:"config_revision,omitempty"`
	ExternalID string `json:"external_id"`
}
type sharedHistoryItem struct {
	ID          string `json:"record_id"`
	MessageID   string `json:"provider_message_id,omitempty"`
	SenderID    string `json:"sender_id,omitempty"`
	SenderName  string `json:"sender_name,omitempty"`
	SenderType  string `json:"sender_type"`
	CreatedAtMS int64  `json:"created_at_ms"`
	Text        string `json:"text"`
	MessageType string `json:"message_type"`
}
type sharedHistoryJob struct {
	ID         string              `json:"id"`
	LeaseToken string              `json:"lease_token"`
	Source     json.RawMessage     `json:"source"`
	StartMS    int64               `json:"start_ms"`
	EndMS      int64               `json:"end_ms"`
	Limit      int                 `json:"limit"`
	Cursor     string              `json:"provider_cursor"`
	Pending    []sharedHistoryItem `json:"pending_items,omitempty"`
}
type sharedHistoryPage struct {
	Items    []sharedHistoryItem `json:"items"`
	Pending  []sharedHistoryItem `json:"pending_items,omitempty"`
	Next     string              `json:"next_provider_cursor,omitempty"`
	Coverage string              `json:"coverage"`
}
type SharedGroupHistoryWorker struct {
	Backend *Backend
	Lookup  func(string) (Binding, bool)
	Logger  *slog.Logger
}

func (*SharedGroupHistoryWorker) Recover(context.Context, *election.Session) error { return nil }
func (w *SharedGroupHistoryWorker) Run(ctx context.Context, _ *election.Session) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if e := w.pollSharedHistory(ctx); e != nil && w.Logger != nil {
				w.Logger.Warn("Shared group history pending", "event", "group.history_retry")
			}
		}
	}
}
func (w *SharedGroupHistoryWorker) pollSharedHistory(ctx context.Context) error {
	var batch struct {
		Items []sharedHistoryJob `json:"items"`
	}
	if e := w.Backend.request(ctx, "POST", "/api/internal/v1/shared-group-history/claim", struct{}{}, &batch); e != nil {
		return e
	}
	for _, j := range batch.Items {
		if e := w.readSharedHistory(ctx, j); e != nil {
			return e
		}
	}
	return nil
}
func (w *SharedGroupHistoryWorker) readSharedHistory(ctx context.Context, j sharedHistoryJob) error {
	var source sharedHistorySource
	if json.Unmarshal(j.Source, &source) != nil || source.Kind != "group" || j.Limit < 1 || j.Limit > 50 || j.EndMS <= j.StartMS || (source.Provider == "wecom" && j.EndMS-j.StartMS > 604800000) {
		return errors.New("shared_history_scope_invalid")
	}
	path := "/api/internal/v1/shared-group-history/" + url.PathEscape(j.ID)
	gate := func(call context.Context) error {
		b, ok := w.Lookup(source.BindingID)
		if !ok || !b.Enabled || b.Provider != source.Provider || b.TenantID != source.TenantID || b.Generation != source.Generation || b.Revision != source.Revision {
			return errors.New("shared_history_binding_changed")
		}
		var current sharedHistoryJob
		if e := w.Backend.request(call, "POST", path+"/authorize", map[string]string{"lease_token": j.LeaseToken}, &current); e != nil {
			return e
		}
		if !reflect.DeepEqual(current, j) {
			return errors.New("shared_history_authorization_changed")
		}
		return nil
	}
	if e := gate(ctx); e != nil {
		return e
	}
	binding, _ := w.Lookup(source.BindingID)
	budget, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	page := sharedHistoryPage{Items: []sharedHistoryItem{}, Coverage: "authorized_query"}
	var err error
	if len(j.Pending) > 0 {
		page.Items = j.Pending
		page.Next = j.Cursor
	} else {
		switch source.Provider {
		case "feishu":
			page, err = fetchSharedFeishuHistory(budget, binding, source, j, gate)
		case "wecom":
			page, err = fetchSharedWecomHistory(budget, binding, source, j, gate)
		default:
			err = errors.New("shared_history_provider_unsupported")
		}
	}
	if len(page.Items) > j.Limit {
		page.Pending = append([]sharedHistoryItem{}, page.Items[j.Limit:]...)
		page.Items = page.Items[:j.Limit]
	}
	if e := gate(ctx); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := "completed"
	if err != nil {
		status = "failed"
		page = sharedHistoryPage{Items: []sharedHistoryItem{}, Coverage: "unavailable"}
	}
	return w.Backend.request(ctx, "POST", path+"/complete", struct {
		LeaseToken string            `json:"lease_token"`
		Status     string            `json:"status"`
		Page       sharedHistoryPage `json:"page"`
	}{j.LeaseToken, status, page}, nil)
}
