package channel

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
)

type historyItem struct {
	RecordKey    string                   `json:"record_key,omitempty"`
	SenderName   string                   `json:"sender_name,omitempty"`
	SenderTenant string                   `json:"sender_tenant,omitempty"`
	SenderIDType string                   `json:"sender_id_type,omitempty"`
	Mentions     []channelhistory.Mention `json:"mentions,omitempty"`
	ID           string                   `json:"message_id"`
	SenderID     string                   `json:"sender_id"`
	SenderType   string                   `json:"sender_type,omitempty"`
	Type         string                   `json:"message_type,omitempty"`
	Text         string                   `json:"text"`
	TimeMS       int64                    `json:"created_at_ms"`
}
type historyResult struct {
	Status    string        `json:"status"`
	Items     []historyItem `json:"items"`
	Digest    string        `json:"digest,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	ErrorCode string        `json:"error_code"`
}
type historyFetch func(context.Context, Binding, channelhistory.Job, channelhistory.Gate) (channelhistory.Snapshot, error)

func RunHistory(ctx context.Context, b *Backend, lookup func(string) (Binding, bool)) error {
	return runHistory(ctx, b, lookup, fetchHistory)
}
func runHistory(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), fetch historyFetch) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var batch struct {
			Items []channelhistory.Job `json:"items"`
		}
		if err := b.request(ctx, "POST", "/api/internal/v1/channel-history/claim", map[string]int{"limit": 1}, &batch); err != nil {
			return err
		}
		for _, j := range batch.Items {
			if err := processHistory(ctx, b, lookup, fetch, j); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func historyBinding(ctx context.Context, lookup func(string) (Binding, bool), j channelhistory.Job) (Binding, bool) {
	if ctx.Err() != nil {
		return Binding{}, false
	}
	v, ok := lookup(j.BindingID)
	return v, ok && v.Enabled && (v.Provider == "feishu" || v.Provider == "wecom") && v.Generation == j.Generation && v.Revision == j.Revision && v.RulesVersion == j.RulesVersion && v.History.Enabled && j.TenantID != "" && v.TenantID == j.TenantID
}
func processHistory(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), fetch historyFetch, j channelhistory.Job) error {
	binding, ok := historyBinding(ctx, lookup, j)
	if !ok {
		return ctx.Err()
	}
	path := "/api/internal/v1/channel-history/" + url.PathEscape(j.ID)
	gate := func(call context.Context, job channelhistory.Job) error {
		if _, ok := historyBinding(call, lookup, job); !ok {
			return errors.New("history_binding_changed")
		}
		var authorized struct {
			channelhistory.Job
			Allowed bool `json:"allowed"`
		}
		if err := b.request(call, "POST", path+"/authorize", map[string]string{"lease_token": job.LeaseToken}, &authorized); err != nil {
			return err
		}
		if !authorized.Allowed || !reflect.DeepEqual(authorized.Job, job) {
			return errors.New("history_authorization_mismatch")
		}
		return nil
	}
	// Authorize before even obtaining a provider token; Reader also gates each page.
	if err := gate(ctx, j); err != nil {
		var rejected *BackendError
		if errors.As(err, &rejected) && rejected.Status == 409 {
			return nil
		}
		return err
	}
	budget, cancel := context.WithTimeout(ctx, 15*time.Second)
	snapshot, err := fetch(budget, binding, j, gate)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result := historyResult{Status: "unavailable", Items: []historyItem{}, ErrorCode: "history_unavailable"}
	if err == nil {
		result.Status = "complete"
		if snapshot.Truncated {
			result.Status = "partial"
		}
		result.ErrorCode = ""
		result.Digest = snapshot.Digest
		result.Truncated = snapshot.Truncated
		for _, m := range snapshot.Messages {
			result.Items = append(result.Items, historyItem{RecordKey: m.RecordKey, ID: m.ID, SenderID: m.SenderID, SenderType: m.SenderType, Type: m.Type, Text: m.Text, TimeMS: m.TimeMS, SenderName: m.SenderName, SenderTenant: m.SenderTenant, SenderIDType: m.SenderIDType, Mentions: m.Mentions})
		}
	}
	return b.request(ctx, "POST", path+"/complete", struct {
		LeaseToken string        `json:"lease_token"`
		Snapshot   historyResult `json:"snapshot"`
	}{j.LeaseToken, result}, nil)
}
func fetchHistory(ctx context.Context, b Binding, j channelhistory.Job, gate channelhistory.Gate) (channelhistory.Snapshot, error) {
	if b.Provider == "wecom" {
		return fetchWecomHistory(ctx, b, j, gate)
	}
	if b.Provider != "feishu" {
		return channelhistory.Snapshot{}, errors.New("history_provider_unsupported")
	}
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10*time.Second))
	reader := channelhistory.Reader{Authorize: gate, Get: func(call context.Context, path string) ([]byte, error) {
		resp, err := api.Get(call, path, nil, larkcore.AccessTokenTypeTenant)
		if err != nil {
			return nil, errors.New("history provider unavailable")
		}
		return resp.RawBody, nil
	}}
	return reader.Fetch(ctx, j)
}
