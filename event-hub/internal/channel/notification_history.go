package channel

import (
	"context"
	"errors"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
	"net/url"
	"reflect"
	"time"
)

func (w *TriggerNotifications) runContexts(ctx context.Context, b *Backend) error {
	var batch struct {
		Items []channelhistory.Job `json:"items"`
	}
	if err := b.request(ctx, "POST", "/api/internal/v1/trigger-notification-contexts/claim", map[string]int{"limit": 1}, &batch); err != nil {
		return err
	}
	for _, j := range batch.Items {
		if err := w.processContext(ctx, b, j); err != nil {
			return err
		}
	}
	return nil
}
func (w *TriggerNotifications) processContext(ctx context.Context, b *Backend, j channelhistory.Job) error {
	path := "/api/internal/v1/trigger-notification-contexts/" + url.PathEscape(j.ID)
	gate := func(call context.Context, _ channelhistory.Job) error {
		binding, ok := w.Lookup(j.BindingID)
		if !ok || !binding.Enabled || binding.Provider != "feishu" || binding.Generation != j.Generation || binding.Revision != j.Revision || binding.RulesVersion != j.RulesVersion || binding.TenantID != j.TenantID {
			return errors.New("notification_binding_changed")
		}
		var authorized struct {
			channelhistory.Job
			Allowed bool `json:"allowed"`
		}
		if err := b.request(call, "POST", path+"/authorize", map[string]string{"lease_token": j.LeaseToken}, &authorized); err != nil {
			return err
		}
		if !authorized.Allowed || !reflect.DeepEqual(authorized.Job, j) {
			return errors.New("notification_context_authorization_changed")
		}
		return nil
	}
	if err := gate(ctx, j); err != nil {
		return err
	}
	binding, _ := w.Lookup(j.BindingID)
	api := lark.NewClient(binding.Credentials.AppID, binding.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10*time.Second))
	reader := channelhistory.Reader{Authorize: gate, Get: func(call context.Context, path string) ([]byte, error) {
		r, e := api.Get(call, path, nil, larkcore.AccessTokenTypeTenant)
		if e != nil {
			return nil, errors.New("notification_provider_unavailable")
		}
		return r.RawBody, nil
	}}
	budget, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	at, err := reader.ResolveNotificationAnchor(budget, j, binding.Credentials.AppID)
	result := historyResult{Status: "unavailable", Items: []historyItem{}, ErrorCode: "notification_anchor_unavailable"}
	if err == nil {
		resolved := j
		resolved.TriggerTimeMS = at
		snapshot := channelhistory.Snapshot{}
		if j.Limit > 0 {
			snapshot, err = reader.Fetch(budget, resolved)
		}
		if err == nil {
			result.Status = "complete"
			result.ErrorCode = ""
			result.Digest = snapshot.Digest
			result.Truncated = snapshot.Truncated
			if snapshot.Truncated {
				result.Status = "partial"
			}
			for _, m := range snapshot.Messages {
				result.Items = append(result.Items, historyItem{ID: m.ID, SenderID: m.SenderID, SenderType: m.SenderType, Type: m.Type, Text: m.Text, TimeMS: m.TimeMS, SenderName: m.SenderName, SenderTenant: m.SenderTenant, SenderIDType: m.SenderIDType, Mentions: m.Mentions})
			}
		} else {
			result.ErrorCode = "notification_history_unavailable"
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return b.request(ctx, "POST", path+"/complete", struct {
		LeaseToken string        `json:"lease_token"`
		Snapshot   historyResult `json:"snapshot"`
		AnchorID   string        `json:"anchor_message_id"`
		AnchorTime int64         `json:"anchor_created_at_ms"`
		ChatID     string        `json:"chat_id"`
		ThreadID   string        `json:"thread_id"`
	}{j.LeaseToken, result, j.TriggerID, at, j.ChatID, j.ThreadID}, nil)
}
