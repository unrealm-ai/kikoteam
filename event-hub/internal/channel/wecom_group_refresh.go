package channel

import (
	"context"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
	"log/slog"
	"net/url"
	"reflect"
	"time"
)

type wecomGroupJob struct {
	ID            string `json:"id"`
	LeaseToken    string `json:"lease_token"`
	BindingID     string `json:"binding_id"`
	Generation    int64  `json:"connection_generation"`
	Revision      int64  `json:"config_revision"`
	IdentityEpoch int64  `json:"identity_epoch"`
	TenantID      string `json:"external_tenant_id"`
}
type WecomGroupRefreshWorker struct {
	Backend *Backend
	Lookup  func(string) (Binding, bool)
	Logger  *slog.Logger
}

func (*WecomGroupRefreshWorker) Recover(context.Context, *election.Session) error { return nil }
func (w *WecomGroupRefreshWorker) Run(ctx context.Context, _ *election.Session) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := w.poll(ctx); err != nil && w.Logger != nil {
				w.Logger.Warn("Wecom recent group refresh pending", "event", "wecom.groups_retry")
			}
		}
	}
}
func (w *WecomGroupRefreshWorker) poll(ctx context.Context) error {
	var batch struct {
		Items []wecomGroupJob `json:"items"`
	}
	if e := w.Backend.request(ctx, "POST", "/api/internal/v1/wecom-group-refreshes/claim", struct{}{}, &batch); e != nil {
		return e
	}
	for _, j := range batch.Items {
		if e := w.refresh(ctx, j, ListWecomRecentGroups); e != nil {
			return e
		}
	}
	return nil
}

type wecomGroupsList func(context.Context, Binding, *WecomGroupRange, func(context.Context) error) (WecomRecentGroups, error)

func (w *WecomGroupRefreshWorker) refresh(ctx context.Context, j wecomGroupJob, list wecomGroupsList) error {
	path := "/api/internal/v1/wecom-group-refreshes/" + url.PathEscape(j.ID)
	var binding Binding
	gate := func(call context.Context) error {
		b, ok := w.Lookup(j.BindingID)
		if !ok || !b.Enabled || b.Provider != "wecom" || b.Generation != j.Generation || b.Revision != j.Revision || b.TenantID != j.TenantID {
			return errors.New("wecom_group_binding_changed")
		}
		binding = b
		var authorized wecomGroupJob
		if e := w.Backend.request(call, "POST", path+"/authorize", map[string]string{"lease_token": j.LeaseToken}, &authorized); e != nil {
			return e
		}
		if !reflect.DeepEqual(j, authorized) {
			return errors.New("wecom_group_authorization_changed")
		}
		return nil
	}
	if e := gate(ctx); e != nil {
		return e
	}
	budget, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	items, err := collectWecomGroups(budget, binding, list, gate)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	status := "completed"
	if err != nil {
		status = "failed"
		items = []WecomRecentGroup{}
	}
	// Only complete bounded snapshots are persisted. Provider cursors remain internal.
	return w.Backend.request(ctx, "POST", path+"/complete", struct {
		LeaseToken string             `json:"lease_token"`
		Status     string             `json:"status"`
		Items      []wecomRefreshItem `json:"items"`
	}{j.LeaseToken, status, wecomRefreshItems(items)}, nil)
}
func collectWecomGroups(ctx context.Context, b Binding, list wecomGroupsList, gate func(context.Context) error) ([]WecomRecentGroup, error) {
	out := []WecomRecentGroup{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	var next *WecomGroupRange
	for range 10 {
		page, e := list(ctx, b, next, gate)
		if e != nil {
			return nil, e
		}
		for _, g := range page.Items {
			if !seen[g.ID] {
				seen[g.ID] = true
				out = append(out, g)
			}
		}
		if len(out) > 10000 {
			return nil, errors.New("wecom_group_budget_exceeded")
		}
		if page.Next == nil {
			return out, nil
		}
		if page.Next.Cursor == "" || cursors[page.Next.Cursor] {
			return nil, errors.New("wecom_group_cursor_invalid")
		}
		if next != nil && (next.BeginMS != page.Next.BeginMS || next.EndMS != page.Next.EndMS) {
			return nil, errors.New("wecom_group_range_changed")
		}
		cursors[page.Next.Cursor] = true
		next = page.Next
	}
	return nil, errors.New("wecom_group_page_budget_exceeded")
}

type wecomRefreshItem struct {
	ID   string `json:"chat_id"`
	Name string `json:"chat_name"`
}

func wecomRefreshItems(items []WecomRecentGroup) []wecomRefreshItem {
	out := []wecomRefreshItem{}
	for _, g := range items {
		out = append(out, wecomRefreshItem{g.ID, g.Name})
	}
	return out
}
